variable "name" {
  description = "Name prefix (ALB and target group names are limited to 32 characters)."
  type        = string
}

variable "vpc_id" {
  type = string
}

variable "vpc_cidr" {
  description = "Targets live in this CIDR; the ALB may only connect into it."
  type        = string
}

variable "subnet_ids" {
  type = list(string)
}

variable "api_hostname" {
  type = string
}

variable "gateway_hostname" {
  type = string
}

variable "cloudflare_zone_id" {
  description = "Zone that holds the ACM validation records."
  type        = string
}

variable "idle_timeout" {
  description = "ALB idle timeout in seconds. WebSockets need >= 300."
  type        = number
  default     = 600

  validation {
    condition     = var.idle_timeout >= 300
    error_message = "The gateway's WebSockets need an idle timeout of at least 300 seconds."
  }
}

variable "restrict_to_cloudflare" {
  description = "Only accept connections from Cloudflare's IP ranges (both hostnames are proxied)."
  type        = bool
  default     = true
}

variable "deletion_protection" {
  type    = bool
  default = true
}

variable "access_logs_bucket" {
  description = "Optional S3 bucket for ALB access logs."
  type        = string
  default     = null
}

variable "connection_logs_bucket" {
  description = "Optional S3 bucket for ALB connection logs (client, TLS and outcome; no URLs)."
  type        = string
  default     = null
}

variable "connection_logs_prefix" {
  description = "Key prefix for ALB connection logs in connection_logs_bucket. Defaults to name."
  type        = string
  default     = null
}

variable "access_logs_prefix" {
  description = "Key prefix for ALB access logs in access_logs_bucket. Defaults to name."
  type        = string
  default     = null
}
