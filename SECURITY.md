# Security policy

## Reporting a vulnerability

Please report security issues privately, never in a public issue:

- open a private report through [GitHub security advisories](https://github.com/useopendesk/opendesk/security/advisories/new), or
- email [team@opendesk.sh](mailto:team@opendesk.sh).

Include what you found and its impact, steps to reproduce or a proof of concept, and the
version or commit, and whether it affects opendesk Cloud, self-hosted installs or both.

We acknowledge reports promptly, keep you updated, fix confirmed issues and publish an
advisory once a fixed release is out. With your permission, we credit you.

The full policy, including testing guidelines and scope, is at
[docs.opendesk.sh/security/disclosure](https://docs.opendesk.sh/security/disclosure/).

## Supported versions

Security fixes go into the newest release, and opendesk Cloud runs `main`. Self-hosted
installs get them by upgrading to the newest release (`opendesk-ctl upgrade`, or the
nightly automatic updates the installer turns on).

## Security controls

The controls opendesk is built to, aligned with OWASP ASVS 4.0.3 Level 1 and the App
Defense Alliance CASA Tier 1, are listed at
[docs.opendesk.sh/security](https://docs.opendesk.sh/security/).
