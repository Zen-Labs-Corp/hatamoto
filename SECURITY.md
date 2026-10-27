# Security policy

## Reporting a vulnerability

Please do **not** open a public issue, discussion or pull request for a
security problem.

- Preferred: GitHub private vulnerability reporting. Open the repository's
  **Security** tab and choose **Report a vulnerability**.
- Or email **security@hatamoto.dev**. It reaches more than one person.
  Encrypt if you can; ask for a key in a first message without details.

Include the affected version (`hatamoto version`), how to reproduce it, the
impact you expect, and whether the problem is already public. Plugin
vulnerabilities belong to the plugin's author; report sandbox escapes and
capability bypasses (a plugin doing something its manifest does not allow)
here.

## What to expect

| Step | Target |
| --- | --- |
| Acknowledgement | within 3 business days |
| First assessment (accepted or not, severity) | within 10 business days |
| Fix for critical and high severity | within 30 days of the assessment |
| Fix for medium and low severity | in the next regular release |

We keep you informed while we work on it, agree a disclosure date with you,
publish a GitHub security advisory with the fix, and credit you unless you
prefer otherwise. Please give us a reasonable time to ship a fix before
disclosing.

## How a fix is handled

1. **Private advisory.** The report arrives as a private GitHub security
   advisory (or we open one for an emailed report). The discussion, the
   patch and any test happen in the advisory's private fork. Nothing is
   public yet.
2. **Patch on every channel.** The fix is released on all update channels
   (`canary`, `edge`, `stable`). Security releases are marked as such in the
   signed release record, and inside the current major they skip the update
   defer, window and pin settings that delay a normal release.
3. **Advisory and CVE.** When the patched release is available, we publish
   the GitHub security advisory (GHSA) with the affected and fixed versions,
   and request a CVE through GitHub for it.
4. **Go vulnerability database.** A vulnerability in the plugin SDK
   (`hatamoto.dev/sdk`) is also submitted to the Go vulnerability database,
   so `govulncheck` reports it to plugin authors.

If the problem is already being exploited, or a fix cannot wait for the steps
above, we say so in the advisory and ship the fix first.

## Related documents

- [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md): what Hatamoto protects,
  and which test enforces each guarantee.
- [Signing keys and fingerprints](docs/site/updates/keys.md): the keys that
  sign releases and first-party packages, and what a leak would mean.

## Supported versions

| Version | Supported |
| --- | --- |
| The latest stable release (`stable` update channel) | Yes |
| The current `edge` release | Yes |
| `canary` builds | Fixes land here first; no separate support |
| The previous major (after 1.0) | Security fixes for six months after the next major is released |
| Older releases | No: update to the latest stable (self-hosted installs update themselves unless updates are turned off) |

Security fixes ship as new releases on the update channels; there are no
backports to older minor versions of the current major.

## Scope

In scope: this repository (the `hatamoto` binary, the admin, the API, the MCP
server, the updater and supervisor, the plugin host and SDK, the example
plugins, the release and registry tooling). Out of scope: third-party plugins
and themes, deployments that run with settings the docs mark as unsafe (for
example `SECURITY_HEADERS_DISABLED=true` or the demo passwords of
`docker-compose.yml` on a public host), and denial of service by volume alone.
