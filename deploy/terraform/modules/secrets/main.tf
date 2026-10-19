# Secrets Manager entries for every secret the services read, named
# <name>/<env>/<KEY>, all encrypted with the one customer-managed key.
#
# Generated secrets are created once from the random provider and never
# rotate on their own: a new CREDENTIALS_KEY would make every stored mailbox
# credential unreadable, and a new AUTH_SECRET signs everyone out.
#
# Operator-supplied secrets (OAuth client secrets, Turnstile, PostHog, Stripe
# keys and price ids, the OpenRouter, Anthropic and Voyage API keys, the APNs key, the
# Cloudflare for SaaS token, integration app secrets) are
# created empty. Set them with
#   scripts/put-secret.sh <KEY>
# (it reads this list) and then list the key in `optional_secrets` in the environment so the task
# definitions start referencing it. A task that references a secret with no
# value would fail to start, which is why they are opt-in.

locals {
  prefix = "${var.name}/${var.environment}"

  generated = {
    AUTH_SECRET       = random_password.auth_secret.result
    CREDENTIALS_KEY   = random_id.credentials_key.hex
    HELP_PROXY_SECRET = random_password.help_proxy_secret.result
    INBOUND_SECRET    = random_password.inbound_secret.result
    NATS_TOKEN        = random_password.nats_token.result
    SECRET_KEY_BASE   = random_password.secret_key_base.result
  }

  generated_keys = ["AUTH_SECRET", "CREDENTIALS_KEY", "HELP_PROXY_SECRET", "INBOUND_SECRET", "NATS_TOKEN", "SECRET_KEY_BASE"]

  # Listed apart from the values so for_each never sees sensitive data.
  database_keys = ["DATABASE_URL", "DATABASE_URL_DIRECT", "DATABASE_URL_MIGRATE"]

  database = {
    DATABASE_URL         = var.database_url
    DATABASE_URL_DIRECT  = var.database_url_direct
    DATABASE_URL_MIGRATE = var.database_url_migrate
  }

  manual = toset([
    "GOOGLE_CLIENT_SECRET",
    "GOOGLE_MAILBOX_CLIENT_SECRET",
    "MICROSOFT_MAILBOX_CLIENT_SECRET",
    "MICROSOFT_CLIENT_SECRET",
    "GITHUB_CLIENT_SECRET",
    "TURNSTILE_SECRET",
    "POSTHOG_KEY",
    "STRIPE_SECRET_KEY",
    "STRIPE_WEBHOOK_SECRET",
    "STRIPE_PRICE_PRO",
    "STRIPE_PRICE_PRO_YEARLY",
    "STRIPE_PRICE_CREDITS",
    # The hidden collab plan (contracts/billing.md "Collab").
    "STRIPE_PRICE_COLLAB",
    "STRIPE_PRICE_COLLAB_YEARLY",
    "OPENROUTER_API_KEY",
    "ANTHROPIC_API_KEY",
    "VOYAGE_API_KEY",
    "APNS_KEY",
    # Adds help center custom domains to the Cloudflare for SaaS zone: SSL and
    # Certificates edit on that zone only.
    "CLOUDFLARE_SAAS_TOKEN",
    # Integrations that only accept apps registered in advance.
    "MCP_SLACK_CLIENT_SECRET",
    "MCP_BOX_CLIENT_SECRET",
    "MCP_GITHUB_CLIENT_SECRET",
    # Collab mail accounts in their own Google project and Entra app.
    "COLLAB_GOOGLE_CLIENT_SECRET",
    "COLLAB_MICROSOFT_CLIENT_SECRET",
  ])

  all_keys = setunion(
    toset(local.generated_keys),
    toset(local.database_keys),
    local.manual,
  )
}

resource "random_password" "auth_secret" {
  length  = 64
  special = false
}

# 32 bytes for AES-256-GCM, hex encoded (64 characters).
resource "random_id" "credentials_key" {
  byte_length = 32
}

# Shared with the help worker (deploy/cloudflare/help-worker), which sets it
# there with `wrangler secret put HELP_PROXY_SECRET`.
resource "random_password" "help_proxy_secret" {
  length  = 48
  special = false
}

resource "random_password" "inbound_secret" {
  length  = 48
  special = false
}

resource "random_password" "nats_token" {
  length  = 48
  special = false
}

resource "random_password" "secret_key_base" {
  length  = 96
  special = false
}

resource "aws_secretsmanager_secret" "this" {
  for_each = local.all_keys

  name                    = "${local.prefix}/${each.key}"
  kms_key_id              = var.kms_key_arn
  recovery_window_in_days = var.recovery_window_in_days
}

resource "aws_secretsmanager_secret_version" "generated" {
  for_each = toset(local.generated_keys)

  secret_id     = aws_secretsmanager_secret.this[each.key].id
  secret_string = local.generated[each.key]
}

resource "aws_secretsmanager_secret_version" "database" {
  for_each = toset(local.database_keys)

  secret_id     = aws_secretsmanager_secret.this[each.key].id
  secret_string = local.database[each.key]
}
