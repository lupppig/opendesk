# One ALB shared by the API and the realtime gateway, routed by host header.
#
# Both hostnames are proxied by Cloudflare, so by default the ALB only accepts
# connections from Cloudflare's published ranges: the origin cannot be reached
# around the WAF. Cloudflare connects with SNI set to the public hostname and
# validates the ACM certificate (zone SSL mode "strict").
#
# The idle timeout covers WebSockets: the gateway's sockets are long-lived and
# the Phoenix client heartbeats every 30s, so anything >= 300s is safe. The
# Cloudflare proxy itself keeps WebSockets open while traffic flows.

data "cloudflare_ip_ranges" "this" {}

locals {
  hostnames  = [var.api_hostname, var.gateway_hostname]
  ingress_v4 = var.restrict_to_cloudflare ? data.cloudflare_ip_ranges.this.ipv4_cidrs : ["0.0.0.0/0"]
  ingress_v6 = var.restrict_to_cloudflare ? data.cloudflare_ip_ranges.this.ipv6_cidrs : ["::/0"]
  ingress = merge(
    { for c in local.ingress_v4 : "v4-${c}" => { v4 = c, v6 = null } },
    { for c in local.ingress_v6 : "v6-${c}" => { v4 = null, v6 = c } },
  )
}

resource "aws_security_group" "alb" {
  name        = "${var.name}-alb"
  description = "ALB: HTTPS from Cloudflare"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  for_each = local.ingress

  security_group_id = aws_security_group.alb.id
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = each.value.v4
  cidr_ipv6         = each.value.v6
}

resource "aws_vpc_security_group_ingress_rule" "http" {
  for_each = local.ingress

  security_group_id = aws_security_group.alb.id
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
  cidr_ipv4         = each.value.v4
  cidr_ipv6         = each.value.v6
}

resource "aws_vpc_security_group_egress_rule" "to_vpc" {
  security_group_id = aws_security_group.alb.id
  ip_protocol       = "tcp"
  from_port         = 0
  to_port           = 65535
  cidr_ipv4         = var.vpc_cidr
}

# ── Certificate, DNS-validated through Cloudflare ─────────────────────────

resource "aws_acm_certificate" "this" {
  domain_name               = var.api_hostname
  subject_alternative_names = [var.gateway_hostname]
  validation_method         = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "cloudflare_dns_record" "validation" {
  for_each = toset(local.hostnames)

  zone_id = var.cloudflare_zone_id
  name    = trimsuffix(one([for o in aws_acm_certificate.this.domain_validation_options : o.resource_record_name if o.domain_name == each.key]), ".")
  type    = one([for o in aws_acm_certificate.this.domain_validation_options : o.resource_record_type if o.domain_name == each.key])
  content = trimsuffix(one([for o in aws_acm_certificate.this.domain_validation_options : o.resource_record_value if o.domain_name == each.key]), ".")
  ttl     = 60
  proxied = false
  comment = "ACM validation for ${each.key}"
}

resource "aws_acm_certificate_validation" "this" {
  certificate_arn         = aws_acm_certificate.this.arn
  validation_record_fqdns = [for r in cloudflare_dns_record.validation : r.name]
}

# ── Load balancer ──────────────────────────────────────────────────────────

resource "aws_lb" "this" {
  name                       = var.name
  load_balancer_type         = "application"
  internal                   = false
  security_groups            = [aws_security_group.alb.id]
  subnets                    = var.subnet_ids
  idle_timeout               = var.idle_timeout
  drop_invalid_header_fields = true
  enable_deletion_protection = var.deletion_protection
  enable_http2               = true

  # Access logs record each request's full URL, query string included, where one-time
  # values ride (OAuth codes, signed file links): keep them off unless something filters
  # them. Connection logs record the client, TLS and outcome of every connection, no URL.
  dynamic "access_logs" {
    for_each = var.access_logs_bucket == null ? [] : [1]
    content {
      bucket  = var.access_logs_bucket
      prefix  = coalesce(var.access_logs_prefix, var.name)
      enabled = true
    }
  }

  dynamic "connection_logs" {
    for_each = var.connection_logs_bucket == null ? [] : [1]
    content {
      bucket  = var.connection_logs_bucket
      prefix  = coalesce(var.connection_logs_prefix, var.name)
      enabled = true
    }
  }
}

resource "aws_lb_target_group" "api" {
  name                 = "${var.name}-api"
  port                 = 8080
  protocol             = "HTTP"
  target_type          = "ip"
  vpc_id               = var.vpc_id
  deregistration_delay = 30 # requests are short

  health_check {
    path                = "/healthz"
    matcher             = "200"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_target_group" "gateway" {
  name                 = "${var.name}-gateway"
  port                 = 4000
  protocol             = "HTTP"
  target_type          = "ip"
  vpc_id               = var.vpc_id
  deregistration_delay = 120 # let sockets drain; clients reconnect and resync by seq

  health_check {
    path                = "/health"
    matcher             = "200"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  # TLS 1.3, and TLS 1.2 with forward-secret AEAD suites only (no CBC, no static RSA).
  ssl_policy      = "ELBSecurityPolicy-TLS13-1-2-Res-2021-06"
  certificate_arn = aws_acm_certificate_validation.this.certificate_arn

  default_action {
    type = "fixed-response"
    fixed_response {
      content_type = "application/json"
      message_body = "{\"status\":404,\"code\":\"not_found\"}"
      status_code  = "404"
    }
  }
}

resource "aws_lb_listener_rule" "api" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 10

  condition {
    host_header {
      values = [var.api_hostname]
    }
  }

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

resource "aws_lb_listener_rule" "gateway" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 20

  condition {
    host_header {
      values = [var.gateway_hostname]
    }
  }

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}
