# Cloudflare: DNS for the AWS origins, the three Pages projects and their
# domains, Email Routing for forwarding addresses, and zone security settings.
#
# Pages projects are direct-upload (no git source): deploy.yml builds each one
# and runs `wrangler pages deploy`, so builds see the same pinned toolchain as
# CI and Cloudflare never needs access to the repository.
#
# Inbound mail: in.<domain> is enabled for Email Routing (Cloudflare adds and
# locks its MX and SPF records), and the catch-all hands every message to the
# Email Worker, which POSTs the raw message to the API. The catch-all is
# zone-wide in the API, but only hostnames with Email Routing MX records
# receive mail at all, and Terraform enables in.<domain> only; the apex keeps
# whatever mail setup it has. The worker script is deployed by
# wrangler (deploy.yml); create it before the first apply, or apply again
# after the first worker deploy.

locals {
  zone_settings = {
    ssl                      = "strict"
    always_use_https         = "on"
    min_tls_version          = "1.2"
    tls_1_3                  = "on"
    automatic_https_rewrites = "on"
    security_level           = "medium"
    browser_check            = "on"
    websockets               = "on"
    opportunistic_encryption = "on"
  }
}

# ── AWS origins, proxied ───────────────────────────────────────────────────

resource "cloudflare_dns_record" "origin" {
  for_each = toset(var.origin_hostnames)

  zone_id = var.zone_id
  name    = each.value
  type    = "CNAME"
  content = var.alb_dns_name
  proxied = true
  ttl     = 1 # automatic; required when proxied
  comment = "AWS ALB (ECS)"
}

# ── Pages ──────────────────────────────────────────────────────────────────

resource "cloudflare_pages_project" "this" {
  for_each = var.pages_projects

  account_id        = var.account_id
  name              = each.value.name
  production_branch = "main"
}

resource "cloudflare_pages_domain" "this" {
  for_each = var.pages_projects

  account_id   = var.account_id
  project_name = cloudflare_pages_project.this[each.key].name
  name         = each.value.domain
}

resource "cloudflare_dns_record" "pages" {
  for_each = var.pages_projects

  zone_id = var.zone_id
  name    = each.value.domain
  type    = "CNAME" # flattened at the apex
  content = cloudflare_pages_project.this[each.key].subdomain
  proxied = true
  ttl     = 1
  comment = "Cloudflare Pages: ${each.value.name}"
}

# ── Email Routing for forwarding addresses ─────────────────────────────────

resource "cloudflare_email_routing_dns" "inbound" {
  count = var.email_routing_enabled ? 1 : 0

  zone_id = var.zone_id
  name    = var.inbound_hostname
}

resource "cloudflare_email_routing_catch_all" "inbound" {
  count = var.email_routing_enabled ? 1 : 0

  zone_id = var.zone_id
  name    = "opendesk inbound: every forwarding address to the Email Worker"
  enabled = true

  matchers = [{ type = "all" }]
  actions = [{
    type  = "worker"
    value = [var.email_worker_name]
  }]

  depends_on = [cloudflare_email_routing_dns.inbound]
}

# ── Zone security ──────────────────────────────────────────────────────────

resource "cloudflare_zone_setting" "this" {
  for_each = var.manage_zone_settings ? local.zone_settings : {}

  zone_id    = var.zone_id
  setting_id = each.key
  value      = each.value
}

# TLS 1.2 at the edge with forward-secret AEAD suites only (no CBC, no static RSA);
# TLS 1.3 suites are all strong and not configurable. Cloudflare accepts a cipher
# list only on a zone with Advanced Certificate Manager, so it is opt-in.
resource "cloudflare_zone_setting" "ciphers" {
  count = var.manage_zone_settings && var.edge_cipher_suites ? 1 : 0

  zone_id    = var.zone_id
  setting_id = "ciphers"
  value = [
    "ECDHE-ECDSA-AES128-GCM-SHA256",
    "ECDHE-ECDSA-CHACHA20-POLY1305",
    "ECDHE-RSA-AES128-GCM-SHA256",
    "ECDHE-RSA-CHACHA20-POLY1305",
    "ECDHE-ECDSA-AES256-GCM-SHA384",
    "ECDHE-RSA-AES256-GCM-SHA384",
  ]
}

# HSTS at the edge. The origins send it too; this covers the Pages sites.
resource "cloudflare_zone_setting" "hsts" {
  count = var.manage_zone_settings ? 1 : 0

  zone_id    = var.zone_id
  setting_id = "security_header"
  value = {
    strict_transport_security = {
      enabled            = true
      max_age            = 31536000
      include_subdomains = true
      preload            = false
      nosniff            = true
    }
  }
}
