# Security Policy

## Supported versions

`gobpm` is pre-1.0 and under active development. Security fixes are made
against the latest release and `master`; there is no back-porting to older
pre-release tags. Pin a version you have reviewed and upgrade forward to pick
up fixes.

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.** Public
issues disclose the problem before a fix is available.

Instead, report it privately through GitHub:

1. Go to the repository's **Security** tab →
   [**Report a vulnerability**](https://github.com/dr-dobermann/gobpm/security/advisories/new).
2. Describe the issue, the affected package/version, and a reproduction if you
   have one.

This opens a private security advisory visible only to you and the
maintainers.

## What to expect

- **Acknowledgement:** best-effort within a few days.
- **Assessment & fix:** we triage, confirm, and prepare a fix in the private
  advisory; we may ask for clarification or a reproduction.
- **Disclosure:** once a fix is released, the advisory is published with
  credit to the reporter (unless you prefer to remain anonymous).

Because this is a library embedded into other applications, please include how
the vulnerability is reachable through the public API where you can — it helps
us assess impact for downstream consumers.
