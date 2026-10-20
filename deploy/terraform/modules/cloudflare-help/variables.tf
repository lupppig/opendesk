variable "zone_id" {
  description = "The help center zone, e.g. the zone of opendeskhelp.com."
  type        = string
}

variable "help_domain" {
  description = "The zone's domain, the API's HELP_DOMAIN (help centers are at <slug>.<help_domain>)."
  type        = string
}

variable "cname_target_label" {
  description = "Label of the CNAME target for custom domains: <label>.<help_domain> is the API's HELP_CNAME_TARGET."
  type        = string
  default     = "custom"
}

variable "fallback_origin_label" {
  description = "Label of the custom hostname fallback origin."
  type        = string
  default     = "origin"
}

variable "manage_zone_settings" {
  description = "Manage SSL/TLS settings for the whole zone."
  type        = bool
  default     = true
}

variable "edge_cipher_suites" {
  description = "Limit TLS 1.2 at the edge to forward-secret AEAD suites. Cloudflare allows this only with Advanced Certificate Manager on the zone."
  type        = bool
  default     = false
}
