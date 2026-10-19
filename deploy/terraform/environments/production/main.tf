# opendesk production: api.opendesk.sh and gateway.opendesk.sh on ECS Fargate
# behind one ALB proxied by Cloudflare; PlanetScale Postgres over AWS
# PrivateLink; NATS on ECS; S3 attachments; SES platform mail; Cloudflare Pages for the site,
# docs, app and admin panel; Cloudflare Email Routing for forwarding addresses; the help center zone
# (<slug>.opendeskhelp.com and custom domains through Cloudflare for SaaS).
#
# See deploy/README.md for the architecture and the first-time runbook.

data "aws_caller_identity" "current" {}

data "cloudflare_zone" "this" {
  count = var.cloudflare_zone_id == null ? 1 : 0

  filter = {
    name = var.cloudflare_zone_name
  }
}

data "cloudflare_zone" "help" {
  count = var.help_domain != "" && var.help_zone_id == null ? 1 : 0

  filter = {
    name = var.help_domain
  }
}

locals {
  env     = "production"
  cluster = "${var.name}-${local.env}"
  account = data.aws_caller_identity.current.account_id
  zone_id = var.cloudflare_zone_id != null ? var.cloudflare_zone_id : data.cloudflare_zone.this[0].id

  # Help centers (contracts/teams.md "Help center addresses"): off with help_domain = "".
  help         = var.help_domain != ""
  help_zone_id = !local.help ? null : var.help_zone_id != null ? var.help_zone_id : data.cloudflare_zone.help[0].id

  host = {
    site    = var.domain
    app     = "${var.subdomains.app}.${var.domain}"
    admin   = "${var.subdomains.admin}.${var.domain}"
    docs    = "${var.subdomains.docs}.${var.domain}"
    api     = "${var.subdomains.api}.${var.domain}"
    gateway = "${var.subdomains.gateway}.${var.domain}"
    inbound = "${var.subdomains.inbound}.${var.domain}"
  }

  namespace = "${var.name}.internal"
}

# ── Encryption key for logs, secrets, ECR and S3 ──────────────────────────

data "aws_iam_policy_document" "kms" {
  statement {
    sid       = "AccountAdmin"
    actions   = ["kms:*"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${local.account}:root"]
    }
  }

  statement {
    sid       = "CloudWatchLogs"
    actions   = ["kms:Encrypt*", "kms:Decrypt*", "kms:ReEncrypt*", "kms:GenerateDataKey*", "kms:Describe*"]
    resources = ["*"]
    principals {
      type        = "Service"
      identifiers = ["logs.${var.aws_region}.amazonaws.com"]
    }
    condition {
      test     = "ArnLike"
      variable = "kms:EncryptionContext:aws:logs:arn"
      values   = ["arn:aws:logs:${var.aws_region}:${local.account}:log-group:*"]
    }
  }
}

resource "aws_kms_key" "this" {
  description             = "${local.cluster}: logs, secrets, images, attachments"
  enable_key_rotation     = true
  deletion_window_in_days = 30
  policy                  = data.aws_iam_policy_document.kms.json
}

resource "aws_kms_alias" "this" {
  name          = "alias/${local.cluster}"
  target_key_id = aws_kms_key.this.key_id
}

# ── Network ───────────────────────────────────────────────────────────────

module "network" {
  source = "../../modules/network"

  name   = local.cluster
  region = var.aws_region
}

resource "aws_service_discovery_private_dns_namespace" "this" {
  name        = local.namespace
  description = "Service discovery for NATS nodes and gateway clustering"
  vpc         = module.network.vpc_id
}

# ── Security groups ───────────────────────────────────────────────────────

resource "aws_security_group" "api" {
  name        = "${var.name}-api"
  description = "API tasks: 8080 from the ALB only"
  vpc_id      = module.network.vpc_id
}

resource "aws_security_group" "worker" {
  name        = "${var.name}-worker"
  description = "Worker and migration tasks: no ingress"
  vpc_id      = module.network.vpc_id
}

resource "aws_security_group" "gateway" {
  name        = "${var.name}-gateway"
  description = "Gateway tasks: 4000 from the ALB, Erlang distribution between gateways"
  vpc_id      = module.network.vpc_id
}

resource "aws_vpc_security_group_egress_rule" "all" {
  for_each = {
    api     = aws_security_group.api.id
    worker  = aws_security_group.worker.id
    gateway = aws_security_group.gateway.id
  }

  security_group_id = each.value
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "alb_to_api" {
  security_group_id            = aws_security_group.api.id
  referenced_security_group_id = module.alb.security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 8080
  to_port                      = 8080
}

resource "aws_vpc_security_group_ingress_rule" "alb_to_gateway" {
  security_group_id            = aws_security_group.gateway.id
  referenced_security_group_id = module.alb.security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 4000
  to_port                      = 4000
}

# Erlang distribution so Presence spans gateway nodes: EPMD on 4369 and the
# distribution ports, which the realtime release pins to 9100-9155
# (inet_dist_listen_min/max) when CLUSTER_DNS is set.
resource "aws_vpc_security_group_ingress_rule" "gateway_dist" {
  for_each = {
    epmd = [4369, 4369]
    dist = [9100, 9155]
  }

  security_group_id            = aws_security_group.gateway.id
  referenced_security_group_id = aws_security_group.gateway.id
  ip_protocol                  = "tcp"
  from_port                    = each.value[0]
  to_port                      = each.value[1]
}

# ── Registries ────────────────────────────────────────────────────────────

resource "aws_ecr_repository" "this" {
  for_each = toset(["opendesk", "realtime"])

  name                 = "${var.name}/${each.key}"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "KMS"
    kms_key         = aws_kms_key.this.arn
  }
}

resource "aws_ecr_lifecycle_policy" "this" {
  for_each = aws_ecr_repository.this

  repository = each.value.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the last 30 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 30
      }
      action = { type = "expire" }
    }]
  })
}

# ── Data ──────────────────────────────────────────────────────────────────

module "planetscale" {
  source = "../../modules/planetscale"

  name         = var.name
  organization = var.planetscale.organization
  database     = var.planetscale.database
  branch       = var.planetscale.branch
  vpc_id       = module.network.vpc_id
  subnet_ids   = module.network.public_subnet_ids
  # The migration task runs in the worker security group.
  client_security_group_ids = {
    api    = aws_security_group.api.id
    worker = aws_security_group.worker.id
  }
}

module "secrets" {
  source = "../../modules/secrets"

  name                 = var.name
  environment          = local.env
  kms_key_arn          = aws_kms_key.this.arn
  database_url         = module.planetscale.database_url
  database_url_direct  = module.planetscale.database_url_direct
  database_url_migrate = module.planetscale.database_url_migrate
}

module "s3" {
  source = "../../modules/s3"

  bucket_name  = "${local.cluster}-attachments-${local.account}"
  kms_key_arn  = aws_kms_key.this.arn
  cors_origins = ["https://${local.host.app}"]
}

module "ses" {
  source = "../../modules/ses"

  name                = local.cluster
  domain              = var.domain
  mail_from_subdomain = var.subdomains.mail
  cloudflare_zone_id  = local.zone_id
  dmarc_rua           = var.dmarc_rua
}

# ── Edge ──────────────────────────────────────────────────────────────────

module "alb" {
  source = "../../modules/alb"

  name                   = local.cluster
  vpc_id                 = module.network.vpc_id
  vpc_cidr               = module.network.vpc_cidr
  subnet_ids             = module.network.public_subnet_ids
  api_hostname           = local.host.api
  gateway_hostname       = local.host.gateway
  cloudflare_zone_id     = local.zone_id
  idle_timeout           = var.alb_idle_timeout
  restrict_to_cloudflare = var.restrict_alb_to_cloudflare
  deletion_protection    = var.alb_deletion_protection
}

module "cloudflare" {
  source = "../../modules/cloudflare"

  account_id           = var.cloudflare_account_id
  zone_id              = local.zone_id
  alb_dns_name         = module.alb.dns_name
  origin_hostnames     = [local.host.api, local.host.gateway]
  inbound_hostname     = local.host.inbound
  manage_zone_settings = var.manage_zone_settings
  pages_projects = {
    site = { name = "${var.name}-site", domain = local.host.site }
    docs = { name = "${var.name}-docs", domain = local.host.docs }
    app  = { name = "${var.name}-app", domain = local.host.app }
    # The instance admin panel (contracts/admin.md), on its own origin.
    admin = { name = "${var.name}-admin", domain = local.host.admin }
  }
}

# The help center zone: DNS for the help worker, the custom hostname fallback
# origin, no-mail records and zone settings. The worker and its routes are
# deployed by wrangler (deploy.yml).
module "cloudflare_help" {
  source = "../../modules/cloudflare-help"
  count  = local.help ? 1 : 0

  zone_id              = local.help_zone_id
  help_domain          = var.help_domain
  manage_zone_settings = var.manage_zone_settings
}

# ── Compute ───────────────────────────────────────────────────────────────

resource "aws_ecs_cluster" "this" {
  name = local.cluster

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecs_cluster_capacity_providers" "this" {
  cluster_name       = aws_ecs_cluster.this.name
  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
  }
}

module "nats" {
  source = "../../modules/nats"

  name             = var.name
  mode             = var.nats_mode
  node_count       = var.nats_nodes
  external_url     = var.external_nats_url
  image            = var.nats_image
  cluster_arn      = aws_ecs_cluster.this.arn
  cluster_name     = aws_ecs_cluster.this.name
  vpc_id           = module.network.vpc_id
  subnet_ids       = module.network.public_subnet_ids
  namespace_id     = aws_service_discovery_private_dns_namespace.this.id
  namespace_name   = local.namespace
  token_secret_arn = module.secrets.arns["NATS_TOKEN"]
  kms_key_arn      = aws_kms_key.this.arn
  cpu_architecture = var.cpu_architecture
  client_security_group_ids = {
    api     = aws_security_group.api.id
    worker  = aws_security_group.worker.id
    gateway = aws_security_group.gateway.id
  }
  enable_execute_command = var.enable_execute_command
  log_retention_days     = var.log_retention_days
}

locals {
  opendesk_image = "${aws_ecr_repository.this["opendesk"].repository_url}:${var.image_tag}"
  realtime_image = "${aws_ecr_repository.this["realtime"].repository_url}:${var.image_tag}"

  # Plain settings shared by the api and worker roles.
  app_env = merge({
    OPENDESK_ENV        = "production"
    HTTP_ADDR           = ":8080"
    DATA_DIR            = "/tmp/opendesk"
    API_URL             = "https://${local.host.api}"
    APP_URL             = "https://${local.host.app}"
    SITE_URL            = "https://${local.host.site}"
    GATEWAY_URL         = "wss://${local.host.gateway}/socket"
    CORS_ORIGINS        = "https://${local.host.app}"
    ADMIN_URL           = "https://${local.host.admin}" # the admin panel: the only origin /v1/admin/* accepts
    TRUST_PROXY         = "true"
    AUTO_MIGRATE        = "false" # deploy.yml runs opendesk-migrate first
    UPDATE_CHECK        = "false" # deploy.yml ships each release here; nothing to announce
    NATS_URL            = module.nats.url
    STORAGE_DRIVER      = "s3"
    S3_BUCKET           = module.s3.bucket
    S3_REGION           = var.aws_region
    MAIL_DRIVER         = "ses"
    MAIL_FROM           = "opendesk <noreply@${var.domain}>"
    SES_REGION          = var.aws_region
    INBOUND_DOMAIN      = local.host.inbound
    INBOUND_AUTHSERV_ID = "mx.cloudflare.net" # Email Routing's sender checks (deploy/cloudflare/email-worker)
    GOOGLE_CLIENT_ID    = var.google_client_id
    MICROSOFT_CLIENT_ID = var.microsoft_client_id
    # The Gmail mailbox client, when it is its own Google project; empty uses the sign-in client.
    GOOGLE_MAILBOX_CLIENT_ID = var.google_mailbox_client_id
    # The Microsoft mailbox app, when it is not the sign-in app; empty uses the sign-in app.
    MICROSOFT_MAILBOX_CLIENT_ID = var.microsoft_mailbox_client_id
    MICROSOFT_TENANT            = var.microsoft_tenant
    COLLAB_GOOGLE_CLIENT_ID     = var.collab_google_client_id # Collab's own clients; empty uses the mailbox clients
    COLLAB_MICROSOFT_CLIENT_ID  = var.collab_microsoft_client_id
    GITHUB_CLIENT_ID            = var.github_client_id
    TURNSTILE_SITE_KEY          = var.turnstile_site_key
    POSTHOG_HOST                = var.posthog_host
    APP_ATTEST_APP_IDS          = var.app_attest_app_ids

    # Integrations that only accept apps registered in advance (variables.tf).
    MCP_SLACK_CLIENT_ID  = var.mcp_slack_client_id
    MCP_BOX_CLIENT_ID    = var.mcp_box_client_id
    MCP_GITHUB_CLIENT_ID = var.mcp_github_client_id

    # Mailbox sign-in per provider (variables.tf).
    GOOGLE_MAILBOX_OAUTH    = tostring(var.google_mailbox_oauth)
    MICROSOFT_MAILBOX_OAUTH = tostring(var.microsoft_mailbox_oauth)
    # Receiving over IMAP (variables.tf); off, mailboxes receive by forwarding.
    MAILBOX_IMAP = tostring(var.mailbox_imap)
    # Sending through the platform mailer (variables.tf); off, mailboxes send through their own server.
    MAILBOX_SYSTEM_SEND = tostring(var.mailbox_system_send)

    # The company in the footer of every platform email (variables.tf).
    EMAIL_LEGAL_ENTITY                = var.email_legal.entity
    EMAIL_LEGAL_COMPANY_NUMBER        = var.email_legal.company_number
    EMAIL_LEGAL_PLACE_OF_REGISTRATION = var.email_legal.place_of_registration
    EMAIL_LEGAL_ADDRESS               = var.email_legal.address
    }, local.help ? {
    # Help centers at https://<slug>.<help_domain>, behind the help worker
    # (HELP_PROXY_SECRET); custom domains CNAME to custom.<help_domain>.
    HELP_DOMAIN       = var.help_domain
    HELP_CNAME_TARGET = "custom.${var.help_domain}"
    } : {}, local.help && contains(var.optional_secrets, "CLOUDFLARE_SAAS_TOKEN") ? {
    # Custom domains become Cloudflare for SaaS custom hostnames; the zone id
    # needs the token beside it.
    CLOUDFLARE_SAAS_ZONE_ID = local.help_zone_id
    } : {}, contains(var.optional_secrets, "OPENROUTER_API_KEY") ? {
    # One key for chat, Jev decisions and (without Voyage) embeddings.
    AI_PROVIDER   = "openrouter"
    AI_MODEL      = var.ai_model
    AI_FAST_MODEL = var.ai_fast_model
    } : contains(var.optional_secrets, "ANTHROPIC_API_KEY") ? {
    AI_PROVIDER   = "anthropic"
    AI_MODEL      = var.ai_model
    AI_FAST_MODEL = var.ai_fast_model
    } : {}, contains(var.optional_secrets, "VOYAGE_API_KEY") ? {
    EMBEDDINGS_DRIVER = "voyage"
    EMBEDDINGS_MODEL  = var.embeddings_model
    } : {}, contains(var.optional_secrets, "APNS_KEY") ? {
    APNS_KEY_ID      = var.apns_key_id
    APNS_TEAM_ID     = var.apns_team_id
    APNS_TOPIC       = var.apns_topic
    PUSH_RELAY_SERVE = tostring(var.push_relay_serve)
  } : {})

  # CLOUDFLARE_SAAS_TOKEN goes only with its zone (CLOUDFLARE_SAAS_ZONE_ID above).
  optional_secrets = {
    for k in var.optional_secrets : k => module.secrets.arns[k] if k != "CLOUDFLARE_SAAS_TOKEN" || local.help
  }

  app_secrets = merge(
    {
      for k in ["AUTH_SECRET", "CREDENTIALS_KEY", "HELP_PROXY_SECRET", "INBOUND_SECRET", "NATS_TOKEN", "DATABASE_URL", "DATABASE_URL_DIRECT"] :
      k => module.secrets.arns[k]
    },
    local.optional_secrets,
  )

  opendesk_health = {
    command      = ["CMD", "/opendesk", "healthcheck"]
    start_period = 30
  }
}

data "aws_iam_policy_document" "app" {
  statement {
    sid       = "Attachments"
    actions   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
    resources = ["${module.s3.bucket_arn}/*"]
  }

  statement {
    sid       = "AttachmentsList"
    actions   = ["s3:ListBucket"]
    resources = [module.s3.bucket_arn]
  }

  statement {
    sid       = "AttachmentsKey"
    actions   = ["kms:GenerateDataKey", "kms:Decrypt"]
    resources = [aws_kms_key.this.arn]
  }

  statement {
    sid       = "PlatformMail"
    actions   = ["ses:SendEmail", "ses:SendRawEmail"]
    resources = [module.ses.identity_arn, module.ses.configuration_set_arn]
  }
}

module "api" {
  source = "../../modules/ecs-service"

  name                = "${var.name}-api"
  container_name      = "api"
  image               = local.opendesk_image
  cluster_arn         = aws_ecs_cluster.this.arn
  cluster_name        = aws_ecs_cluster.this.name
  cpu                 = var.api.cpu
  memory              = var.api.memory
  cpu_architecture    = var.cpu_architecture
  environment         = merge(local.app_env, { OPENDESK_ROLE = "api" })
  secrets             = local.app_secrets
  port_mappings       = [{ container_port = 8080, name = "http" }]
  health_check        = local.opendesk_health
  writable_paths      = ["/tmp"]
  task_role_policies  = { app = data.aws_iam_policy_document.app.json }
  ecr_repository_arns = [aws_ecr_repository.this["opendesk"].arn]
  kms_key_arn         = aws_kms_key.this.arn
  log_retention_days  = var.log_retention_days
  subnet_ids          = module.network.public_subnet_ids
  security_group_ids  = [aws_security_group.api.id]
  target_group_arn    = module.alb.api_target_group_arn
  desired_count       = var.api.min
  autoscaling         = { min = var.api.min, max = var.api.max }

  enable_execute_command = var.enable_execute_command
}

module "worker" {
  source = "../../modules/ecs-service"

  name             = "${var.name}-worker"
  container_name   = "worker"
  image            = local.opendesk_image
  cluster_arn      = aws_ecs_cluster.this.arn
  cluster_name     = aws_ecs_cluster.this.name
  cpu              = var.worker.cpu
  memory           = var.worker.memory
  cpu_architecture = var.cpu_architecture
  environment      = merge(local.app_env, { OPENDESK_ROLE = "worker" })
  secrets          = local.app_secrets
  # The worker role answers /healthz and /readyz only.
  health_check        = local.opendesk_health
  writable_paths      = ["/tmp"]
  task_role_policies  = { app = data.aws_iam_policy_document.app.json }
  ecr_repository_arns = [aws_ecr_repository.this["opendesk"].arn]
  kms_key_arn         = aws_kms_key.this.arn
  log_retention_days  = var.log_retention_days
  subnet_ids          = module.network.public_subnet_ids
  security_group_ids  = [aws_security_group.worker.id]
  desired_count       = var.worker.min
  autoscaling         = { min = var.worker.min, max = var.worker.max }
  spot                = var.worker.spot
  stop_timeout        = 120 # let River finish running jobs

  enable_execute_command = var.enable_execute_command
}

# One-off migration task: deploy.yml runs it with the direct URL before
# updating the services. No service, no load balancer.
module "migrate" {
  source = "../../modules/ecs-service"

  name             = "${var.name}-migrate"
  container_name   = "migrate"
  create_service   = false
  image            = local.opendesk_image
  command          = ["migrate"]
  cpu              = 256
  memory           = 512
  cpu_architecture = var.cpu_architecture
  environment = {
    OPENDESK_ENV = "production"
    DATA_DIR     = "/tmp/opendesk"
  }
  secrets = {
    # The migrate role (DDL), direct: migrations take session-level locks.
    # The services connect as the app role, which cannot change the schema.
    DATABASE_URL        = module.secrets.arns["DATABASE_URL_MIGRATE"]
    DATABASE_URL_DIRECT = module.secrets.arns["DATABASE_URL_MIGRATE"]
    # config.Load refuses to boot in production without these.
    AUTH_SECRET     = module.secrets.arns["AUTH_SECRET"]
    CREDENTIALS_KEY = module.secrets.arns["CREDENTIALS_KEY"]
  }
  writable_paths      = ["/tmp"]
  ecr_repository_arns = [aws_ecr_repository.this["opendesk"].arn]
  kms_key_arn         = aws_kms_key.this.arn
  log_retention_days  = var.log_retention_days
}

module "gateway" {
  source = "../../modules/ecs-service"

  name             = "${var.name}-gateway"
  container_name   = "gateway"
  image            = local.realtime_image
  cluster_arn      = aws_ecs_cluster.this.arn
  cluster_name     = aws_ecs_cluster.this.name
  cpu              = var.gateway.cpu
  memory           = var.gateway.memory
  cpu_architecture = var.cpu_architecture
  environment = {
    PORT                 = "4000"
    PHX_HOST             = local.host.gateway
    PHX_SERVER           = "true"
    CHECK_ORIGINS        = "https://${local.host.app}"
    NATS_URL             = module.nats.url
    CLUSTER_DNS          = "gateway.${local.namespace}"
    RELEASE_DISTRIBUTION = "name"
    RELEASE_TMP          = "/tmp"
    TRUSTED_PROXY_HOPS   = "2" # Cloudflare, then the ALB
    POSTHOG_HOST         = var.posthog_host
  }
  secrets = merge(
    {
      AUTH_SECRET     = module.secrets.arns["AUTH_SECRET"]
      NATS_TOKEN      = module.secrets.arns["NATS_TOKEN"]
      SECRET_KEY_BASE = module.secrets.arns["SECRET_KEY_BASE"]
    },
    contains(var.optional_secrets, "POSTHOG_KEY") ? { POSTHOG_KEY = module.secrets.arns["POSTHOG_KEY"] } : {},
  )
  port_mappings = [{ container_port = 4000, name = "http" }]
  health_check = {
    command      = ["CMD-SHELL", "/app/bin/healthcheck"]
    start_period = 30
  }
  writable_paths         = ["/tmp"]
  ecr_repository_arns    = [aws_ecr_repository.this["realtime"].arn]
  kms_key_arn            = aws_kms_key.this.arn
  log_retention_days     = var.log_retention_days
  subnet_ids             = module.network.public_subnet_ids
  security_group_ids     = [aws_security_group.gateway.id]
  target_group_arn       = module.alb.gateway_target_group_arn
  discovery_namespace_id = aws_service_discovery_private_dns_namespace.this.id
  discovery_name         = "gateway"
  desired_count          = var.gateway.min
  autoscaling            = { min = var.gateway.min, max = var.gateway.max }
  stop_timeout           = 60

  enable_execute_command = var.enable_execute_command
}

# ── CI/CD ─────────────────────────────────────────────────────────────────

module "github_oidc" {
  source = "../../modules/github-oidc"

  github_repo                = var.github_repo
  github_repo_immutable      = var.github_repo_immutable
  create_oidc_provider       = var.create_github_oidc_provider
  existing_oidc_provider_arn = var.existing_github_oidc_provider_arn
  cluster_name               = aws_ecs_cluster.this.name
  service_names              = [module.api.service_name, module.worker.service_name, module.gateway.service_name]
  migrate_family             = module.migrate.family
  migrate_log_group_arn      = module.migrate.log_group_arn
  ecr_repository_arns        = [for r in aws_ecr_repository.this : r.arn]
  pass_role_arns = flatten([
    for m in [module.api, module.worker, module.gateway, module.migrate] : [m.task_role_arn, m.execution_role_arn]
  ])

  # The Terraform roles deploy.yml and ci.yml assume (deploy/terraform/README.md, "Applying changes").
  terraform_state_bucket = var.terraform_state_bucket != "" ? var.terraform_state_bucket : "${var.name}-terraform-state-${local.account}"
  secret_prefix          = "${var.name}/${local.env}"
  kms_key_arn            = aws_kms_key.this.arn
}
