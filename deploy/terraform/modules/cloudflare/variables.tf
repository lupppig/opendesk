variable "account_id" {
  type = string
}

variable "zone_id" {
  type = string
}

variable "alb_dns_name" {
  type = string
}

variable "origin_hostnames" {
  description = "Hostnames proxied to the ALB, e.g. api.opendesk.sh and gateway.opendesk.sh."
  type        = list(string)
}

variable "pages_projects" {
  description = "key => { name = Pages project name, domain = custom domain }."
  type = map(object({
    name   = string
    domain = string
  }))
}

variable "email_routing_enabled" {
  type    = bool
  default = true
}

variable "inbound_hostname" {
  description = "Hostname that receives forwarding mail, e.g. in.opendesk.sh."
  type        = string
}

variable "email_worker_name" {
  type    = string
  default = "opendesk-inbound-email"
}

variable "manage_zone_settings" {
  description = "Manage SSL/TLS and security settings for the whole zone."
  type        = bool
  default     = true
}

variable "edge_cipher_suites" {
  description = "Limit TLS 1.2 at the edge to forward-secret AEAD suites. Cloudflare allows this only with Advanced Certificate Manager on the zone."
  type        = bool
  default     = false
}
