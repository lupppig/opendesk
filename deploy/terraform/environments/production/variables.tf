variable "name" {
  description = "Name prefix for every resource."
  type        = string
  default     = "opendesk"
}

variable "aws_region" {
  description = "AWS region. The PlanetScale database must be in the same one (PrivateLink is per region)."
  type        = string
  default     = "us-east-1"
}

variable "planetscale" {
  description = "The PlanetScale Postgres database, created in the dashboard in aws_region (modules/planetscale)."
  type = object({
    organization = string
    database     = string
    branch       = optional(string, "main")
  })
}

# ── Domains ───────────────────────────────────────────────────────────────

variable "domain" {
  description = "Apex domain: the marketing site, and the platform mail sender."
  type        = string
  default     = "opendesk.sh"
}

variable "subdomains" {
  description = "Hostname labels under the apex."
  type = object({
    app     = optional(string, "app")
    admin   = optional(string, "admin")
    docs    = optional(string, "docs")
    api     = optional(string, "api")
    gateway = optional(string, "gateway")
    inbound = optional(string, "in")
    mail    = optional(string, "mail")
  })
  default = {}
}

variable "cloudflare_account_id" {
  type = string
}

variable "cloudflare_zone_name" {
  description = "Zone that holds every hostname."
  type        = string
  default     = "opendesk.sh"
}

variable "cloudflare_zone_id" {
  description = "Set to skip the zone lookup by name."
  type        = string
  default     = null
}

variable "help_domain" {
  description = "The help center zone (HELP_DOMAIN): help centers are at <slug>.<help_domain>, and custom domains CNAME to custom.<help_domain>. Empty leaves help centers at API_URL/help/<slug> and manages no help zone."
  type        = string
  default     = "opendeskhelp.com"
}

variable "help_zone_id" {
  description = "The Cloudflare zone of help_domain. Set to skip the zone lookup by name; it is also CLOUDFLARE_SAAS_ZONE_ID."
  type        = string
  default     = null
}

variable "manage_zone_settings" {
  type    = bool
  default = true
}

variable "restrict_alb_to_cloudflare" {
  type    = bool
  default = true
}

variable "dmarc_rua" {
  type    = string
  default = ""
}

# ── Deploys ───────────────────────────────────────────────────────────────

variable "github_repo" {
  type    = string
  default = "useopendesk/opendesk"
}

variable "github_repo_immutable" {
  description = "The repository in GitHub's immutable OIDC subject, owner@owner_id/name@repo_id (gh api repos/OWNER/REPO/actions/oidc/customization/sub shows the prefix). Empty trusts github_repo only."
  type        = string
  default     = ""
}

variable "create_github_oidc_provider" {
  type    = bool
  default = true
}

variable "existing_github_oidc_provider_arn" {
  type    = string
  default = null
}

variable "terraform_state_bucket" {
  description = "The state bucket (backend.tf), which also holds the settings. Empty means opendesk-terraform-state-<account id>."
  type        = string
  default     = ""
}

variable "image_tag" {
  description = "Tag for the initial task definitions. deploy.yml registers later revisions."
  type        = string
  default     = "bootstrap"
}

variable "cpu_architecture" {
  description = "ARM64 or X86_64; deploy.yml must build images for it."
  type        = string
  default     = "ARM64"
}

variable "enable_execute_command" {
  type    = bool
  default = false
}

variable "log_retention_days" {
  type    = number
  default = 30
}

# ── Sizes ─────────────────────────────────────────────────────────────────
#
# The defaults are the launch size: one task of each, scaling out on CPU. For
# no single point of failure (deploy/README.md, "Sizing"), set
#   api     = { min = 2, max = 6 }
#   gateway = { min = 2, max = 4 }
#   worker  = { spot = false }
#   nats_nodes = 3
# and switch the PlanetScale cluster to high availability in its dashboard.

variable "api" {
  type = object({
    cpu    = optional(number, 512)
    memory = optional(number, 1024)
    min    = optional(number, 1)
    max    = optional(number, 3)
  })
  default = {}
}

variable "worker" {
  description = "spot runs the worker on Fargate Spot: about 70% cheaper, and River retries the jobs of an interrupted task."
  type = object({
    cpu    = optional(number, 512)
    memory = optional(number, 1024)
    min    = optional(number, 1)
    max    = optional(number, 2)
    spot   = optional(bool, true)
  })
  default = {}
}

variable "gateway" {
  type = object({
    cpu    = optional(number, 512)
    memory = optional(number, 1024)
    min    = optional(number, 1)
    max    = optional(number, 2)
  })
  default = {}
}

variable "alb_idle_timeout" {
  type    = number
  default = 600
}

variable "alb_deletion_protection" {
  type    = bool
  default = true
}

# ── NATS ──────────────────────────────────────────────────────────────────

variable "nats_mode" {
  description = "cluster = NATS nodes on ECS (nats_nodes); external = external_nats_url (e.g. Synadia Cloud)."
  type        = string
  default     = "cluster"
}

variable "nats_nodes" {
  description = "1 runs a single standalone server; 3 a cluster that survives losing a node."
  type        = number
  default     = 1

  validation {
    condition     = contains([1, 3, 5], var.nats_nodes)
    error_message = "nats_nodes is 1, 3 or 5."
  }
}

variable "external_nats_url" {
  type    = string
  default = ""
}

variable "nats_image" {
  type    = string
  default = "nats:2.15-alpine"
}

# ── Application settings (non-secret) ─────────────────────────────────────

variable "posthog_host" {
  type    = string
  default = "https://us.i.posthog.com"
}

variable "google_client_id" {
  type    = string
  default = ""
}

variable "google_mailbox_client_id" {
  description = "GOOGLE_MAILBOX_CLIENT_ID: the Google client for Gmail mailbox connections, when it is not the sign-in client. Its secret is GOOGLE_MAILBOX_CLIENT_SECRET."
  type        = string
  default     = ""
}

variable "microsoft_client_id" {
  type    = string
  default = ""
}

variable "microsoft_mailbox_client_id" {
  description = "MICROSOFT_MAILBOX_CLIENT_ID: the Microsoft app for mailbox connections, when it is not the sign-in app. Its secret is MICROSOFT_MAILBOX_CLIENT_SECRET."
  type        = string
  default     = ""
}

variable "collab_google_client_id" {
  description = "COLLAB_GOOGLE_CLIENT_ID: the Google client Collab mail accounts connect with (gmail.modify, a restricted scope), when it is not the mailbox client. Its secret is COLLAB_GOOGLE_CLIENT_SECRET (optional_secrets)."
  type        = string
  default     = ""
}

variable "collab_microsoft_client_id" {
  description = "COLLAB_MICROSOFT_CLIENT_ID: the Microsoft app Collab mail accounts connect with (Mail.ReadWrite, Mail.Send), when it is not the mailbox app. Its secret is COLLAB_MICROSOFT_CLIENT_SECRET (optional_secrets)."
  type        = string
  default     = ""
}

variable "google_mailbox_oauth" {
  description = "Offer Google sign-in for new mailbox connections. Keep false until the Google OAuth client is verified for the Gmail scopes; mailboxes then connect with an app password or forwarding. User sign-in with Google is not affected."
  type        = bool
  default     = false
}

variable "microsoft_mailbox_oauth" {
  description = "Offer Microsoft sign-in for new mailbox connections. User sign-in with Microsoft is not affected."
  type        = bool
  default     = true
}

variable "mailbox_imap" {
  description = "MAILBOX_IMAP: let mailboxes receive over IMAP (generic IMAP, and Google and Microsoft mailboxes that read the inbox). Off, mailboxes receive by forwarding and Google and Microsoft connect send-only (gmail.send, Mail.Send); no IMAP connection is kept open. Match the Pages variable MAILBOX_IMAP so the app and the API agree."
  type        = bool
  default     = false
}

variable "mailbox_system_send" {
  description = "MAILBOX_SYSTEM_SEND: let mailboxes send replies through the platform mailer (SES, from MAIL_FROM). Off, every mailbox sends through its own Google, Microsoft or SMTP server, so customer mail is never sent from this account."
  type        = bool
  default     = false
}

variable "microsoft_tenant" {
  type    = string
  default = "common"
}

variable "github_client_id" {
  type    = string
  default = ""
}

# Integrations whose sign-in only accepts apps registered in advance
# (contracts/integrations.md): MCP_<KEY>_CLIENT_ID, with the secret
# MCP_<KEY>_CLIENT_SECRET in optional_secrets. Redirect URL:
# https://<api host>/v1/integrations/oauth/callback.
variable "mcp_slack_client_id" {
  description = "MCP_SLACK_CLIENT_ID: the Slack app integrations sign in with."
  type        = string
  default     = ""
}

variable "mcp_box_client_id" {
  description = "MCP_BOX_CLIENT_ID: the Box app integrations sign in with."
  type        = string
  default     = ""
}

variable "mcp_github_client_id" {
  description = "MCP_GITHUB_CLIENT_ID: the GitHub App integrations sign in with; empty asks for a token."
  type        = string
  default     = ""
}

variable "turnstile_site_key" {
  type    = string
  default = ""
}

variable "optional_secrets" {
  description = <<-EOT
    Operator-supplied secrets the tasks should reference, once they have a value
    (GOOGLE_CLIENT_SECRET, GOOGLE_MAILBOX_CLIENT_SECRET, MICROSOFT_CLIENT_SECRET, MICROSOFT_MAILBOX_CLIENT_SECRET, GITHUB_CLIENT_SECRET,
    TURNSTILE_SECRET, POSTHOG_KEY, STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET,
    STRIPE_PRICE_PRO, STRIPE_PRICE_PRO_YEARLY, STRIPE_PRICE_CREDITS, STRIPE_PRICE_COLLAB, STRIPE_PRICE_COLLAB_YEARLY,
    OPENROUTER_API_KEY, ANTHROPIC_API_KEY, VOYAGE_API_KEY, APNS_KEY, CLOUDFLARE_SAAS_TOKEN,
    MCP_SLACK_CLIENT_SECRET, MCP_BOX_CLIENT_SECRET, MCP_GITHUB_CLIENT_SECRET,
    COLLAB_GOOGLE_CLIENT_SECRET, COLLAB_MICROSOFT_CLIENT_SECRET). See modules/secrets.
  EOT
  type        = set(string)
  default     = []

  validation {
    condition = alltrue([for s in var.optional_secrets : contains([
      "GOOGLE_CLIENT_SECRET", "GOOGLE_MAILBOX_CLIENT_SECRET", "MICROSOFT_CLIENT_SECRET", "MICROSOFT_MAILBOX_CLIENT_SECRET", "GITHUB_CLIENT_SECRET", "TURNSTILE_SECRET", "POSTHOG_KEY",
      "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "STRIPE_PRICE_PRO", "STRIPE_PRICE_PRO_YEARLY", "STRIPE_PRICE_CREDITS",
      "STRIPE_PRICE_COLLAB", "STRIPE_PRICE_COLLAB_YEARLY",
      "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "VOYAGE_API_KEY", "APNS_KEY", "CLOUDFLARE_SAAS_TOKEN",
      "MCP_SLACK_CLIENT_SECRET", "MCP_BOX_CLIENT_SECRET", "MCP_GITHUB_CLIENT_SECRET",
      "COLLAB_GOOGLE_CLIENT_SECRET", "COLLAB_MICROSOFT_CLIENT_SECRET",
    ], s)])
    error_message = "optional_secrets may only list the operator-supplied secrets."
  }
}

# AI (contracts/ai.md). The provider is switched on only once its key is in
# optional_secrets, so a task never starts with a provider and no key:
# OPENROUTER_API_KEY sets openrouter, else ANTHROPIC_API_KEY sets anthropic.
# Model ids are the provider's own (openrouter "anthropic/claude-sonnet-5.5",
# anthropic "claude-sonnet-5-5"); empty uses the app's default for the provider.
variable "ai_model" {
  description = "AI_MODEL for the AI provider; empty uses the app's default."
  type        = string
  default     = ""
}

variable "ai_fast_model" {
  description = "AI_FAST_MODEL for the AI provider; empty uses the app's default."
  type        = string
  default     = ""
}

variable "embeddings_model" {
  description = "EMBEDDINGS_MODEL for the voyage driver; empty uses the app's default."
  type        = string
  default     = ""
}

# Native apps (contracts/native.md). iPhone push is on once APNS_KEY is in
# optional_secrets: the instance sends its own pushes (push = direct) and, with
# push_relay_serve, relays them for self-hosted instances that opt in.
variable "apns_key_id" {
  description = "APNS_KEY_ID: the key id of the APNs auth key (.p8) stored as APNS_KEY."
  type        = string
  default     = ""

  validation {
    condition     = !contains(var.optional_secrets, "APNS_KEY") || length(var.apns_key_id) > 0
    error_message = "apns_key_id is required once APNS_KEY is in optional_secrets."
  }
}

variable "apns_team_id" {
  description = "APNS_TEAM_ID: the Apple developer team that owns the APNs key and the app."
  type        = string
  default     = ""

  validation {
    condition     = !contains(var.optional_secrets, "APNS_KEY") || length(var.apns_team_id) > 0
    error_message = "apns_team_id is required once APNS_KEY is in optional_secrets."
  }
}

variable "apns_topic" {
  description = "APNS_TOPIC: the iPhone app's bundle id."
  type        = string
  default     = "sh.opendesk.app"
}

variable "push_relay_serve" {
  description = "PUSH_RELAY_SERVE: serve /v1/push/relay/* for self-hosted instances. Needs APNS_KEY."
  type        = bool
  default     = true
}

variable "app_attest_app_ids" {
  description = "APP_ATTEST_APP_IDS: comma-separated TEAMID.bundle ids whose App Attest proofs stand in for Turnstile. Empty turns App Attest off."
  type        = string
  default     = ""
}

variable "email_legal" {
  description = "The company named in the footer of every platform email (EMAIL_LEGAL_*), where the law asks for it."
  type = object({
    entity                = string
    company_number        = string
    place_of_registration = string
    address               = string
  })
  default = {
    entity                = "Mindroot Ltd"
    company_number        = "16543299"
    place_of_registration = "England and Wales"
    address               = "71-75 Shelton Street, London, England, WC2H 9JQ"
  }
}
