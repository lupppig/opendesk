# opendesk Terraform

Everything the hosted service runs on: AWS (ECS Fargate, ALB, S3, SES, Secrets
Manager, KMS, ECR, Cloud Map), Cloudflare (DNS, Pages, Email Routing, zone
security, the help center zone) and PlanetScale (Postgres roles, reached over AWS PrivateLink). The architecture and the full first-time runbook
are in [`deploy/README.md`](../README.md); this file is the Terraform-specific part.

## Layout

```
environments/production/   the root: wiring, variables, outputs, backend
modules/
  network/       VPC, public subnets, IGW, S3 gateway endpoint (no NAT Gateway)
  alb/           one ALB for api + gateway, ACM cert validated through Cloudflare
  ecs-service/   a Fargate task definition (+ service, roles, logs, autoscaling)
  nats/          one NATS server or a cluster of three on ECS, or an external NATS
  s3/            private attachments bucket
  ses/           domain identity, DKIM, MAIL FROM, DMARC, configuration set
  secrets/       Secrets Manager entries, generated and operator-supplied
  planetscale/   app and migrate roles, the PrivateLink endpoint, connection strings
  cloudflare/    DNS, Pages projects and domains, Email Routing, zone settings
  cloudflare-help/  the help center zone (opendeskhelp.com): records for the help
                 worker, the custom hostname fallback origin, no-mail records, zone settings
  github-oidc/   the roles deploy.yml and ci.yml assume (no long-lived AWS keys)
```

Names other tooling depends on: cluster `opendesk-production`; services and task
families `opendesk-api`, `opendesk-worker`, `opendesk-gateway`,
`opendesk-nats-{1,2,3}` (only `-1` at the launch size); one-off family `opendesk-migrate`; ECR repositories
`opendesk/opendesk` and `opendesk/realtime`; secrets `opendesk/production/<KEY>`;
Pages projects `opendesk-site`, `opendesk-docs`, `opendesk-app`; Email Worker
`opendesk-inbound-email`; help worker `opendesk-help`.

## Credentials

Terraform reads them from the environment only:

```sh
export AWS_PROFILE=opendesk-admin        # or an SSO session
export CLOUDFLARE_API_TOKEN=...          # Zone: DNS, Zone Settings, Email Routing; Account: Pages, Email Routing
                                         # (help zone: below)
export PLANETSCALE_SERVICE_TOKEN_ID=... PLANETSCALE_SERVICE_TOKEN=...   # access to the database's roles
```

The same token reaches the help center zone (`opendeskhelp.com`) with Zone Read,
DNS Edit, Zone Settings Edit, SSL and Certificates Edit and Workers Routes Edit,
and the account with Workers Scripts Edit (`deploy/README.md`, step 2). The API's
own token for custom hostnames is a different one (`CLOUDFLARE_SAAS_TOKEN`,
below).

## Bootstrap order

1. **Database and state bucket** (once, by hand). Create the PlanetScale
   database in its dashboard: Postgres, AWS us-east-1, single node or high
   availability (`deploy/README.md`, "Sizing and cost"). Terraform never creates
   or deletes it; it manages the roles and the private connection. Then the
   state bucket:

   ```sh
   ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
   aws s3api create-bucket --bucket opendesk-terraform-state-$ACCOUNT --region us-east-1
   aws s3api put-bucket-versioning --bucket opendesk-terraform-state-$ACCOUNT \
     --versioning-configuration Status=Enabled
   aws s3api put-public-access-block --bucket opendesk-terraform-state-$ACCOUNT \
     --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
   ```

2. **Deploy the workers once** (`pnpm exec wrangler deploy` in
   `deploy/cloudflare/email-worker` and in `deploy/cloudflare/help-worker`).
   The catch-all rule points at the email worker by name. If you skip this, the
   first apply fails on the catch-all; deploy the worker and apply again.

   **Enable Cloudflare for SaaS on the help zone** (`opendeskhelp.com`: SSL/TLS >
   Custom Hostnames > Enable). The fallback origin
   (`cloudflare_custom_hostname_fallback_origin`) is refused until it is on.
   Set `help_zone_id` in `terraform.tfvars`, or let Terraform look the zone up
   from `help_domain`.

3. **Apply:**

   ```sh
   cd deploy/terraform/environments/production
   cp terraform.tfvars.example terraform.tfvars   # fill in
   terraform init -backend-config="bucket=opendesk-terraform-state-$ACCOUNT" -backend-config="region=us-east-1"
   terraform apply
   ```

   The services are created with the `bootstrap` image tag, which does not exist
   yet, so their tasks fail until step 5. That is expected.

   Then allow only private addresses on the database (its IP restrictions in
   PlanetScale: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), so the
   PrivateLink endpoint is the only way in. Check that a connection to the
   public host from outside AWS is refused.

4. **Operator secrets.** For each of `GOOGLE_CLIENT_SECRET`,
   `MICROSOFT_CLIENT_SECRET`, `GITHUB_CLIENT_SECRET`, `TURNSTILE_SECRET`,
   `POSTHOG_KEY`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_PRO`,
   `STRIPE_PRICE_PRO_YEARLY`, `STRIPE_PRICE_CREDITS`, `OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`,
   `VOYAGE_API_KEY`, `APNS_KEY`, `MCP_SLACK_CLIENT_SECRET`, `MCP_BOX_CLIENT_SECRET`, `MCP_GITHUB_CLIENT_SECRET`,
   `STRIPE_PRICE_COLLAB`, `STRIPE_PRICE_COLLAB_YEARLY`, `COLLAB_GOOGLE_CLIENT_SECRET` and
   `COLLAB_MICROSOFT_CLIENT_SECRET` you use (`STRIPE_PRICE_CREDITS` holds the
   one-time $10 AI top-up price; the app reads it when `STRIPE_PRICE_AI_TOPUP`
   is unset):

   ```sh
   scripts/put-secret.sh GOOGLE_CLIENT_SECRET                  # prompts, without echo
   scripts/put-secret.sh APNS_KEY < AuthKey_<key id>.p8         # PEM as is
   ```

   then add the key to `optional_secrets` in `terraform.tfvars` and apply again.
   They are opt-in because a task that references a secret with no value does
   not start.

   Help centers: `HELP_PROXY_SECRET` is generated; give the help worker the same
   value (`deploy/README.md`, step 6):

   ```sh
   aws secretsmanager get-secret-value --secret-id opendesk/production/HELP_PROXY_SECRET \
     --query SecretString --output text | (cd deploy/cloudflare/help-worker && pnpm exec wrangler secret put HELP_PROXY_SECRET)
   ```

   `CLOUDFLARE_SAAS_TOKEN` is a token with **SSL and Certificates Edit** on the
   help zone only. Store it like the others and add it to `optional_secrets`:
   the apply then also sets `CLOUDFLARE_SAAS_ZONE_ID`, and custom domains get
   their certificates from Cloudflare.

5. **GitHub configuration** from the outputs (`terraform output`):
   `AWS_DEPLOY_ROLE_ARN` = `github_deploy_role_arn`, `ECS_MIGRATE_NETWORK` =
   `migrate_network_configuration`, and the infrastructure environments
   ([Applying changes](#applying-changes), "One-time setup"). Then
   run the deploy workflow: it pushes images, runs `opendesk-migrate` and updates
   the services.

## Applying changes

Production is applied by the Deploy workflow, from a release tag of a commit on
`main`, never by hand:

- **Terraform code** changes by pull request. Pull requests run fmt, validate
  and the plan summary tests, with no cloud credentials: planning reads the
  state, which holds generated secrets, so only code on `main` plans. The next
  release (a `v*` tag) runs Deploy, which plans (its job summary lists each
  resource address with its action and the counts, never a value) and applies
  before the migrations and the service roll, so new env vars and secrets exist
  before the code that reads them.
- **Approval.** A plan that deletes, replaces or forgets anything other than an
  `aws_ecs_task_definition` (a new revision on every env change is routine)
  waits in Actions for a reviewer of the `infrastructure-approval` environment;
  every other plan applies on its own. The applying job plans again with the
  state lock and applies only a plan with the same addresses and actions as the
  one summarized, and of the class its gate is for.
- **Settings** (`terraform.tfvars`) are not in git. They live in the state
  bucket, at `opendesk/production.tfvars`: `scripts/tfvars.sh pull`, edit,
  `scripts/tfvars.sh push` (it shows the diff and asks), then run Deploy from
  the current release tag with target `backend`, which applies them and rolls
  the services onto the task definitions Terraform registers (target
  `infrastructure`, also from a release tag, leaves the services on their
  current revisions). `scripts/tfvars.sh diff` compares the two copies.
- **Secret values** never pass through GitHub: `scripts/put-secret.sh NAME`
  stores one in Secrets Manager. A new optional secret also goes in
  `optional_secrets` in the settings.

Logs and job summaries are public, so the workflows read a plan
only through `scripts/tfplan.sh` (addresses and actions); Terraform's own output
is discarded and only its errors are printed. No plan file is uploaded.

The roles (module `github-oidc`):

- `opendesk-github-terraform-plan`, for `infrastructure-plan`: AWS read-only,
  the state and settings objects, and the project's secret values (a refresh
  reads the versions Terraform generated), through Secrets Manager only. It
  writes nothing, so its plans run with `-lock=false`, and it reads no object
  outside the state bucket and no log line.
- `opendesk-github-terraform-apply`, for `infrastructure` and
  `infrastructure-approval`, which deploy only from `main` and release tags:
  administrator, for one hour.

Every credential that can read a secret (these roles, the deploy role, and the
environments' Cloudflare and PlanetScale tokens) is given only to a job running
from `main` or a `v*` release tag: each environment allows only those, each
Terraform role trusts only its environments' OIDC subjects, and Deploy refuses a
tag whose commit is not on `main`. Code reaches `main` only through the merge
queue, and a tag ruleset lets only maintainers create `v*` tags. Pull requests,
including those from forks, get no secret and no cloud credential.

### One-time setup

An operator does this once, from a machine with admin AWS credentials:

1. Apply locally once (`terraform apply`, as in the bootstrap) to create the two
   roles; note `github_terraform_plan_role_arn` and
   `github_terraform_apply_role_arn` from `terraform output`.
2. Upload the settings: `scripts/tfvars.sh push`.
3. Create three environments (Settings > Environments):

   | environment | deployment branches and tags | reviewers | `AWS_TERRAFORM_ROLE_ARN` |
   | --- | --- | --- | --- |
   | `infrastructure-plan` | `main`, tags `v*` | none | `github_terraform_plan_role_arn` |
   | `infrastructure` | `main`, tags `v*` | none | `github_terraform_apply_role_arn` |
   | `infrastructure-approval` | `main`, tags `v*` | required reviewer | `github_terraform_apply_role_arn` |

   In each, the variables `AWS_REGION` (`us-east-1`), `TF_STATE_BUCKET`
   (`opendesk-terraform-state-<account id>`) and `AWS_TERRAFORM_ROLE_ARN`, and
   the secrets `CLOUDFLARE_API_TOKEN`, `PLANETSCALE_SERVICE_TOKEN_ID` and
   `PLANETSCALE_SERVICE_TOKEN`. The two apply environments hold the tokens of
   [Credentials](#credentials); `infrastructure-plan` only plans, so it holds
   read-only ones: a Cloudflare token with read on what the apply token edits,
   and a PlanetScale service token that reads the database's branches and roles.

From then on a local `terraform plan -lock=false` is a read-only look; applies
go through Deploy.

## Day two

- **Images never go through Terraform.** `deploy.yml` registers a new revision
  of each family from the latest one, with only the image changed. The services
  ignore `task_definition` and `desired_count`, so `terraform apply` after a
  deploy changes neither. An apply that changes a task definition (new env or
  secret) registers a new revision; Deploy applies before it rolls the services,
  so the roll builds on it.
- **Images are immutable and tagged by commit SHA.** The repositories keep the
  last 30.
- **Secrets never rotate on their own.** A new `CREDENTIALS_KEY` makes every
  stored mailbox credential unreadable; never taint `random_id.credentials_key`.
- **Sizes** are variables (`variables.tf`, "Sizes"): the defaults are the launch
  size, one of each. The database's size and high availability are settings in
  PlanetScale, changed online.
- **Database roles.** The services connect as `opendesk-app` (read and write all
  data, no DDL); only the migration task connects as `opendesk-migrate`.
  Replacing a role drops the old one and its password, and tasks read secrets
  only at start: run a deploy right after such an apply.
- **NATS as a service:** `nats_mode = "external"` and `external_nats_url` remove
  the NATS services; put the provider's token in
  `opendesk/production/NATS_TOKEN`.
- **R2 instead of S3:** see the header of `modules/s3/main.tf`.
- **Help centers** are on with `help_domain` (default `opendeskhelp.com`): the
  api and worker get `HELP_DOMAIN`, `HELP_CNAME_TARGET` (`custom.<help_domain>`)
  and `HELP_PROXY_SECRET`. `help_domain = ""` leaves help centers at
  `API_URL/help/<slug>` and manages no help zone. Rotating `HELP_PROXY_SECRET`
  means the worker secret and a deploy together: the API serves help center
  hosts only to requests that carry its current secret.

## Checks

```sh
scripts/check-tfplan.sh            # the plan summary and risk class
terraform fmt -check -recursive deploy/terraform
for d in deploy/terraform/environments/* deploy/terraform/modules/*; do
  (cd "$d" && terraform init -backend=false -input=false >/dev/null && terraform validate)
done
```

`modules/planetscale/tests` checks the connection strings it builds, with mocked
providers: `cd deploy/terraform/modules/planetscale && terraform init && terraform test`.
