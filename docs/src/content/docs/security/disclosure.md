---
title: Responsible disclosure
description: How to report a security vulnerability in opendesk.
---

We take security reports seriously and appreciate the work of researchers who help keep opendesk and its users safe.

## How to report

Email **[team@opendesk.sh](mailto:team@opendesk.sh)**, or open a private report through [GitHub security advisories](https://github.com/useopendesk/opendesk/security/advisories/new).

Please include:

- what you found and its impact
- steps to reproduce, or a proof of concept
- the version or commit, and whether it affects the hosted service, self-hosted installs or both

Please do not open a public issue for a vulnerability.

## What to expect

- We acknowledge your report promptly and keep you updated as we investigate.
- We fix confirmed issues and publish an advisory once a fixed release is available.
- With your permission, we credit you in the advisory.

## Guidelines

When testing, please:

- test against your own self-hosted instance where you can, or only against accounts and workspaces you own on the hosted service
- never access, change or delete other people's data
- avoid degrading the service (no denial of service, spam or volumetric scanning)
- give us reasonable time to fix an issue before disclosing it

Good-faith research that follows these guidelines is welcome, and we will not pursue it.

## Scope

In scope: the opendesk code in [github.com/useopendesk/opendesk](https://github.com/useopendesk/opendesk), the desktop and iPhone apps, and the hosted service at `opendesk.sh`, `app.opendesk.sh`, `api.opendesk.sh`, `gateway.opendesk.sh`, `admin.opendesk.sh`, `docs.opendesk.sh` and the help centers at `*.opendeskhelp.com`. Help centers on a customer's own domain are in scope for flaws in opendesk, not in the customer's domain or DNS.

Out of scope: findings that need physical access or a compromised device, social engineering, reports from automated scanners without a demonstrated impact, and missing best-practice headers on pages that serve no sensitive data.
