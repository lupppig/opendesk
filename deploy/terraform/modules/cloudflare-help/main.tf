# Cloudflare: the help center zone (contracts/teams.md "Help center addresses").
#
# Every help center is at <slug>.<help_domain>, and a customer's own domain
# (help.acme.com) CNAMEs to custom.<help_domain> and joins the zone as a
# Cloudflare for SaaS custom hostname, which the API adds and removes itself
# (CLOUDFLARE_SAAS_TOKEN). Every request that enters the zone runs the help
# worker (deploy/cloudflare/help-worker), whose routes, "*/*" included, are
# deployed with it by wrangler. The worker is the origin: the hostnames here
# point at 100:: (the IPv6 discard prefix), an originless record that only
# lets Cloudflare proxy them.
#
#   <help_domain>, www   -> the worker, which redirects to the product site
#   *                    -> the worker, one help center per label
#   custom               -> CNAME to origin: what customer domains point at
#   origin               -> the custom hostname fallback origin
#
# The zone sends no mail, so it says so: a null MX, an SPF that permits no
# sender, and a DMARC policy that rejects whatever claims to be from it.
#
# Cloudflare for SaaS must be enabled on the zone (SSL/TLS > Custom Hostnames)
# before the fallback origin can be set; deploy/README.md has the steps.

locals {
  zone_settings = {
    ssl                      = "strict"
    always_use_https         = "on"
    min_tls_version          = "1.2"
    tls_1_3                  = "on"
    automatic_https_rewrites = "on"
    opportunistic_encryption = "on"
  }

  origin_host = "${var.fallback_origin_label}.${var.help_domain}"
  target_host = "${var.cname_target_label}.${var.help_domain}"

  # Originless records for the worker.
  worker_hosts = {
    apex     = var.help_domain
    www      = "www.${var.help_domain}"
    wildcard = "*.${var.help_domain}"
    origin   = local.origin_host
  }
}

# ── Hostnames, proxied to the worker ───────────────────────────────────────

resource "cloudflare_dns_record" "worker" {
  for_each = local.worker_hosts

  zone_id = var.zone_id
  name    = each.value
  type    = "AAAA"
  content = "100::"
  proxied = true
  ttl     = 1 # automatic; required when proxied
  comment = each.key == "origin" ? "Custom hostname fallback origin (help worker)" : "Help worker"
}

resource "cloudflare_dns_record" "cname_target" {
  zone_id = var.zone_id
  name    = local.target_host
  type    = "CNAME"
  content = local.origin_host
  proxied = true
  ttl     = 1
  comment = "CNAME target for help center custom domains (HELP_CNAME_TARGET)"
}

resource "cloudflare_custom_hostname_fallback_origin" "this" {
  zone_id = var.zone_id
  origin  = local.origin_host

  depends_on = [cloudflare_dns_record.worker]
}

# ── No mail from this zone ─────────────────────────────────────────────────

resource "cloudflare_dns_record" "null_mx" {
  zone_id  = var.zone_id
  name     = var.help_domain
  type     = "MX"
  content  = "."
  priority = 0
  ttl      = 3600
  comment  = "Null MX (RFC 7505): the zone receives no mail"
}

resource "cloudflare_dns_record" "spf" {
  zone_id = var.zone_id
  name    = var.help_domain
  type    = "TXT"
  content = "\"v=spf1 -all\""
  ttl     = 3600
  comment = "SPF: the zone sends no mail"
}

resource "cloudflare_dns_record" "dmarc" {
  zone_id = var.zone_id
  name    = "_dmarc.${var.help_domain}"
  type    = "TXT"
  content = "\"v=DMARC1; p=reject;\""
  ttl     = 3600
  comment = "DMARC: reject mail that claims to be from the zone"
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
