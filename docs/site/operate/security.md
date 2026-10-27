---
title: Security policy and reporting
nav_title: Security policy
description: How to report a vulnerability in Hatamoto privately, what happens after you report it, and which versions get security fixes.
---

This page tells you how to report a security problem in Hatamoto, what happens after you report it and which versions receive fixes. It is the page behind `https://hatamoto.dev/security`. To protect your own instance, follow [Harden an instance](hardening.md).

## Overview

| Question | Answer |
|---|---|
| Where to report | GitHub private vulnerability reporting on the [Hatamoto repository](https://github.com/Zen-Labs-Corp/hatamoto/security/advisories/new), or email `security@hatamoto.dev` |
| Where not to report | Public issues, discussions or pull requests |
| First answer | Within 3 business days |
| Full policy | [Security policy on GitHub](https://github.com/Zen-Labs-Corp/hatamoto/security/policy) |
| Machine-readable contact | `https://hatamoto.dev/.well-known/security.txt` (RFC 9116) |

## How to report

Never open a public issue, discussion or pull request for a security problem.

1. Preferred: open the repository's **Security** tab and choose **Report a vulnerability**. The direct address is `https://github.com/Zen-Labs-Corp/hatamoto/security/advisories/new`. The report stays private between you and the maintainers.
2. Or email `security@hatamoto.dev`. To encrypt, ask for a key in a first message that holds no details.

Include:

- the affected version (the output of `hatamoto version`),
- how to reproduce the problem,
- the impact you expect,
- whether the problem is already public.

A vulnerability in a plugin belongs to the plugin's author. Report sandbox escapes and capability bypasses here: a plugin that does something its manifest does not allow is a Hatamoto problem.

## What happens next

| Step | Target |
|---|---|
| Acknowledgement | Within 3 business days |
| First assessment (accepted or not, severity) | Within 10 business days |
| Fix for critical and high severity | Within 30 days of the assessment |
| Fix for medium and low severity | In the next regular release |

The maintainers keep you informed while they work on it, agree a disclosure date with you, publish a GitHub security advisory with the fix and credit you unless you prefer otherwise. Please give them a reasonable time to ship a fix before you disclose.

## How a fix ships

1. **Private advisory.** The report becomes a private GitHub security advisory (one is opened for an emailed report). The discussion, the patch and any test happen in the advisory's private fork.
2. **Release on every channel.** The fix is released on the `canary`, `edge` and `stable` update channels. The signed release record marks it as a security release, and inside the current major it skips the defer, window and pin settings that delay a normal release. See [How updates work](../updates/overview.md).
3. **Advisory and CVE.** When the fixed release is available, the advisory is published with the affected and fixed versions, and a CVE is requested through GitHub.
4. **Go vulnerability database.** A vulnerability in the plugin SDK (`hatamoto.dev/sdk`) is also submitted to the Go vulnerability database, so `govulncheck` reports it to plugin authors.

If the problem is already exploited, or a fix cannot wait for these steps, the advisory says so and the fix ships first.

## Supported versions

| Version | Security fixes |
|---|---|
| The latest stable release (`stable` channel) | Yes |
| The current `edge` release | Yes |
| `canary` builds | Fixes land here first; no separate support |
| The previous major (after 1.0) | For six months after the next major is released |
| Older releases | No: update to the latest stable |

Security fixes ship as new releases on the update channels. There are no backports to older minor versions of the current major. Self-hosted instances update themselves unless updates are turned off; see [Pin or turn off updates](../updates/pin-or-turn-off-updates.md).

## Scope

In scope: the `hatamoto` binary, the admin, the API, the MCP server, the updater and supervisor, the plugin host and SDK, the example plugins, and the release and registry tooling.

Out of scope:

- third-party plugins and themes,
- deployments that run with settings these docs mark as unsafe, for example `SECURITY_HEADERS_DISABLED=true` or the demo passwords of `docker-compose.yml` on a public host,
- denial of service by volume alone.

## Related

- [Harden an instance](hardening.md): the production checklist.
- [Signing keys](../updates/keys.md): the keys that sign releases and first-party packages.
- [Trust and signing](../updates/trust-and-signing.md): how an instance checks an update before it installs it.
