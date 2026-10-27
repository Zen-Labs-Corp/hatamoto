---
title: Trust and signing
nav_title: Trust and signing
description: The two signing keys behind every update, each check a new release must pass before it runs, and what a leaked key could and could not do.
---

A self-updating program is only as trustworthy as its verification. Hatamoto separates the authority to say *what a release is* from the authority to say *which release is on offer*, and checks both against public keys compiled into every binary. Nothing is trusted because of where it was downloaded from, so a mirror or a compromised download host cannot ship code to you. This page lists the keys, the signed files and every check. The guides that rely on it start at [See how updates work](overview.md).

## The two keys

| Key | Signs | Held by | Embedded in the binary as |
|---|---|---|---|
| Release key | Each release **record**: the version, kind, rollback floor, minimum starting version, SDK range, schema head and the SHA-256 and size of every binary | Offline, used only when a release is cut | `release-primary.pub`, `release-backup.pub` |
| Manifest key | The short **channel manifest** that says which releases a channel offers, at what rollout, and which are halted or yanked | An automated CI job | `manifest.pub` |

Their fingerprints are on [Signing keys and fingerprints](keys.md). The public keys live in `backend/internal/release/keys/` and are embedded with `go:embed`. The loader refuses any embedded key that carries private material or whose id does not match its public key. Release keys are matched by the `<key_id>:<base64 signature>` prefix in each channel entry, so a second release key can be added for rotation without breaking older binaries.

## What the objects look like

A channel manifest is signed JSON with these fields: `channel` (`stable`, `edge` or `canary`), `issued_at`, `expires_at`, `releases` and `yanked`. Each entry in `releases` carries a `version`, the `record_sig` of its record, a `ramp` (a list of `from` timestamps and `pct` values) and a `halted` flag with an optional `halt_reason`. A yank carries a version, a reason and optionally a replacement (`to`).

A release record is a small JSON document:

```json
{
  "version": "v1.4.2",
  "kind": "patch",
  "security": false,
  "sdk": "0.7.0",
  "schema_head": 202610231010,
  "rollback_floor": "v1.4.0",
  "min_from": "v1.0.0",
  "notes_url": "https://example.com/notes",
  "artifacts": {
    "linux-amd64": { "sha256": "<64 hex characters>", "size": 31457280 }
  }
}
```

The values above are an illustration of the shape; real records list all four platforms (`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`).

## What every update verifies

For an install from the channel, the checks run in this order and the first failure stops the update:

1. The manifest envelope signature, against the manifest keys.
2. The manifest channel field equals the channel you asked for, so a mirror cannot hand you the edge manifest in place of stable.
3. Freshness: `issued_at` and `expires_at` must be present and the manifest must not have expired. The lifetime may not exceed 15 days.
4. The version appears in the manifest and is in canonical `vMAJOR.MINOR.PATCH[-prerelease]` form (no build metadata, no missing `v`).
5. The record signature, against the release keys, over the exact bytes of the record.
6. The record's version equals the channel entry's version, and its `rollback_floor` and `min_from` are valid versions.
7. The binary for your platform exists in the record, is within the size cap (256 MiB) and matches both the recorded size and SHA-256.

A binary that does not match its signed record is rejected before it is staged. The supervisor verifies the stored record again before it runs a staged release, so a file tampered with on disk after staging is caught too.

For an offline install, `hatamoto update apply --file F --record R --sig S` performs steps 5 to 7 (a manifest is not needed) against the same embedded keys, then also refuses a version on the bad list, one already running, one that needs a newer `min_from` first, or one below the running release's rollback floor.

## Freshness and re-signing

A manifest expires after 14 days and a scheduled CI job re-signs the channels daily, so a stale copy cannot keep an old release on offer or hide a fix. Because the check runs against the manifest's own timestamps, a box with a wrong clock may reject a good manifest; keep NTP running.

## What a leaked key could do

- **Manifest key leaked:** an attacker could halt or yank a release, or try to withhold an update. They could not ship code, because the hash of every binary comes from the record, which only the release key signs.
- **A mirror or CDN compromised:** it can serve nothing that verifies, so the worst case is a failed update.
- **Release key leaked:** this is the serious case, which is why the key is kept offline and used only when a release is cut. The binary embeds two release public keys (a primary and a backup), and either may sign a record.

## Privacy

Update checks are plain anonymous GETs with no query string, cookie or identifier. The rollout bucket is computed locally from a random id that never leaves your machine. Set `HATAMOTO_UPDATES=off` and no request is made at all.

## For maintainers

Cutting records, signing them, and publishing channel manifests is done with `backend/tools/release` (commands `keygen`, `record`, `sign-record`, `channel add|ramp|halt|unhalt|yank|resign` and `verify`). It is a development and CI tool that is never linked into the `hatamoto` binary and makes no network calls. The maintainer workflow is covered in [Release process](../migrate-contribute/release-process.md).
