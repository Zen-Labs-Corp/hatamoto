---
title: Release process
nav_title: Release process
description: How a Hatamoto release is tagged, built reproducibly in CI, attested, signed offline, published and rolled out through the stable, edge and canary channels.
---

This page is for maintainers and for anyone who wants to verify how the binary they run was made. It describes the shape of the process and the checks in it. Releases are built by CI from a tag, signed **offline** with keys that never touch CI, and rolled out gradually through channels. Operators do not need any of this to run or update Hatamoto. See [How updates work](../updates/overview.md) for the user side, and [Make your first contribution](contributing.md) for the checks a change passes before it reaches a release.

## Version numbers

`scripts/version.sh` derives the version stamped into every build:

| State of the checkout | Version |
|---|---|
| On tag `vX.Y.Z` | `vX.Y.Z` |
| N commits after `vX.Y.Z` | `vX.Y.(Z+1)-dev.N` |
| N commits after `vX.Y.Z-pre` | `vX.Y.Z-pre.dev.N` |
| No release tag | `v0.0.0-dev.<commit count>` |
| Not a git checkout | `v0.0.0-dev.0` |

Only tags shaped like `vN.N.N` with an optional prerelease count. Other tags are ignored. `make build` stamps the version and the commit into the binary through linker flags, and `hatamoto version` prints them (`--json` for the machine-readable form that release tooling reads).

## Reproducible builds

`make release-build` builds the four release artifacts: `hatamoto-linux-amd64`, `hatamoto-linux-arm64`, `hatamoto-darwin-amd64` and `hatamoto-darwin-arm64`. It has no defaults for the version and commit:

```bash
RELEASE_VERSION=v1.2.3 RELEASE_COMMIT=$(git rev-parse HEAD) make release-build
```

A forgotten value stops the build instead of stamping `git describe` output. Every setting that changes the bytes is pinned in the Makefile and not read from the caller's shell: `GOAMD64`, `GOARM64`, `GOFLAGS`, `GOEXPERIMENT`, the Go toolchain (the `toolchain` line of `backend/go.mod`) and flags such as `-trimpath` and `-buildvcs=false`. The result lands in `dist/` (override with `DIST`). Anyone can rebuild a release and compare sha256 sums, which is what makes the signed record meaningful.

Next to each bare binary the same target writes a **bundle**, `hatamoto-<os>-<arch>.tar.gz`, holding the binary, `LICENSE`, `NOTICE` and `THIRD_PARTY_LICENSES/` (the licence texts of every Go module in the build from `tools/licenses`, the vendored JavaScript and the font licence). The bundles are reproducible too (sorted names, fixed times and owner, `gzip -n`; GNU tar is required). The bare binaries stay: the signed record and the updater reference them and never the bundles.

`cd backend && go run ./tools/licenses -check-notice ../NOTICE` runs in `make check`. It fails when a module compiled into the binary ships a `NOTICE` file that the root `NOTICE` does not name and reproduce, or when Lucide is not attributed.

## Tags

`scripts/tag-release.sh vX.Y.Z` creates the two signed tags of a release on one commit (default `HEAD`, which must be on `origin/main`), and `--push` sends both in one atomic push:

| Tag | Meaning |
|---|---|
| `vX.Y.Z` | The product release. Pushing it starts the `release` workflow. |
| `backend/vX.Y.Z` | The Go module `hatamoto.dev/core`, which lives in `backend/`: `go get hatamoto.dev/core@vX.Y.Z` resolves this tag. It does not match `v*` and starts nothing. |

The SDK is not tagged here. It is exported to the public repository `Zen-Labs-Corp/hatamoto-sdk` by `scripts/publish-sdk.sh`, which tags `vA.B.C` there. See [SDK and registry](sdk-and-registry.md).

The script refuses to run without an SSH signing key (`git config gpg.format ssh` and `user.signingkey`, ideally a hardware-backed key), an existing tag or a commit that main does not have, and it deletes a tag it made that fails verification. `scripts/test-tag-release.sh` tests all of that with a scratch repository and a throwaway key.

The `release` workflow checks the signature before it builds anything: `scripts/verify-tag-signature.sh` requires an annotated tag signed by a key in `.github/allowed_signers`, and reads that file from `main`, not from the tagged commit, so a tag cannot bring its own signer list. While the file lists no key it only warns and builds, which keeps tags made before the maintainers' signing key exists working. Adding the first key line turns the check on for every later tag. In the public repository the check always requires a key, so a forgotten signer line fails the build instead of switching the check off. This signature check is a consistency check, not the security boundary: a tag brings its own copy of this workflow. The boundary is the tag ruleset on `v*` and `backend/v*` (only the release owner may create them, a blocker for the public cut-over) and, for the image, the separate `image.yml` described below.

## From tag to draft release

Pushing a tag that looks like `vMAJOR.MINOR.PATCH` (with an optional prerelease suffix) starts the `release` workflow. Its jobs hold only the permissions they use:

1. `build` (read-only) validates the tag name before anything runs, because a tag can carry quotes and semicolons, then verifies the tag signature (above), builds the four artifacts and their bundles with `make release-build`, records the Go version and flags, reads the build facts from `hatamoto version --json`, and builds an **unsigned** release record with `go run ./tools/release record`. The kind follows the version: `x.0.0` is a major, `x.y.0` a minor, anything else a patch. The `rollback_floor` and `min_from` fields are placeholders that `record --check` flags for review. It then writes an SBOM per binary with syft (SPDX and CycloneDX), a `SHA256SUMS` file for every `hatamoto-*` file, and hands `dist/` to the next jobs.
2. `attest` (identity token and attestation write) attests build provenance for each binary, bundle and SBOM and for `SHA256SUMS`, and attests both SBOMs against their binary. It runs in public repositories only. It is `continue-on-error`: the offline signature is the trust root, so a failed attestation (an Actions allowlist, an outage) leaves an error annotation and a summary line but never fails the run, which keeps `release.sh` from hanging on its run watch. Re-run the failed job to add the missing attestation.
3. `draft` (the only job that can write) creates a **draft** release with the artifacts, bundles, SBOMs, `SHA256SUMS` and the record. Prereleases are marked as such. It needs only `build`, so it alone decides whether the run is green.

The container image is not a job of this workflow. A tag push runs the workflow file of the tagged commit, so anything in it can be edited by whoever pushes a tag. `image.yml` instead runs only by `workflow_dispatch`, so its file always comes from the branch it is dispatched on, and its first step refuses any ref but `refs/heads/main`. After the release is signed and published (below), a maintainer starts it:

```bash
gh workflow run image.yml --ref main -f tag=vX.Y.Z
```

The job sits behind the `release-image` environment, which holds required reviewers. It reads `scripts/verify-tag-signature.sh` and `.github/allowed_signers` from `main`, requires a valid tag signature (always checked in the public repository), the tag's commit on `main` and a published, non-draft GitHub release for the tag, then builds `ghcr.io/zen-labs-corp/hatamoto` for `linux/amd64` and `linux/arm64` at the tag's commit, signs it keyless with cosign, attaches an SPDX SBOM attestation and verifies both. The `latest` tag moves only for a release without a prerelease suffix. The signature's identity is `image.yml@refs/heads/main`.

Owner set-up (once, at the public cut-over; needs the `release-image` environment to exist, which `gh api` creates on first use):

```bash
gh api -X PUT repos/Zen-Labs-Corp/hatamoto/environments/release-image \
  -F 'reviewers[][type]=User' -F "reviewers[][id]=$(gh api users/OWNER -q .id)" \
  -F 'deployment_branch_policy[protected_branches]=false' -F 'deployment_branch_policy[custom_branch_policies]=true'
gh api -X POST repos/Zen-Labs-Corp/hatamoto/environments/release-image/deployment-branch-policies -f name=main
```

No key or secret is used in this workflow. The provenance and signatures are tied to the workflow identity at the tag, which [Verify a download](../updates/verify-a-download.md) shows how to check.

## Signing and publishing

A maintainer reviews the record and sets the fields that need judgement, then signs it on a machine that holds the release key:

```bash
cd backend
go run ./tools/release record --check release.json --dir ../dist
go run ./tools/release record --amend release.json --rollback-floor v1.2.0 --min-from v1.0.0
go run ./tools/release sign-record release.json --key /secure/path/release.key
go run ./tools/release verify --chan edge --record release.json \
    --sig release.json.sig --dir ../dist --version v1.2.3
```

`rollback_floor` is the oldest release a rollback may return to, which comes from the n-1 check described in [Writing migrations](writing-migrations.md). `min_from` is the oldest version that may update straight to this release. Signing can use a key file or an ssh-agent. Verification checks the signature, the version and the size and hash of all four artifacts, and nothing in `tools/release` touches the network.

The signed files, `release.json`, `release.json.sig` and the four binaries, are then uploaded to the server that hosts `/releases`. `scripts/publish-release.sh vX.Y.Z DIR` does that. It refuses a directory with anything else in it (a stray key, for example), verifies everything again offline, compares each file with what the server already has, uploads the artifacts first, then the signature, and the record last, so a present record means a complete release. Files are never overwritten: releases are immutable, and a differing file aborts before any upload. It never reads a key. The target host comes from an environment variable and is never stored in the repository.

### Publish the GitHub release

Last, the draft becomes the public release. Do this only after the server upload and after the draft's attestations check out, because a published release should not change:

```bash
# the amended record and its signature replace the unsigned record in the draft
gh release upload vX.Y.Z release.json release.json.sig --clobber
# the draft's files are the ones the workflow attested
gh attestation verify dist/hatamoto-linux-amd64.tar.gz --repo Zen-Labs-Corp/hatamoto \
    --signer-workflow Zen-Labs-Corp/hatamoto/.github/workflows/release.yml
gh release edit vX.Y.Z --draft=false --verify-tag
```

The published release then carries the four binaries and bundles, `SHA256SUMS`, one SPDX and one CycloneDX SBOM per binary, `release.json` and `release.json.sig`, and the build attestations that GitHub stores for them. Anyone can check them with [Verify a download](../updates/verify-a-download.md). Turn on the repository setting for immutable releases so a published release cannot be edited.

### Publish the installer script

The VPS installer, `install.sh`, is assembled from the parts under `deploy/install/` by `deploy/install/build-install.sh`, so its embedded public keys always match `backend/internal/release/keys/`. The release workflow attaches it to the draft and attests it. After the release is published, put the same bytes on the website:

```bash
scripts/publish-install.sh vX.Y.Z
```

The script refuses to run unless the checked-out commit is the tag; the commit may also carry the `backend/vX.Y.Z` tag. The GitHub repository comes from the `origin` remote; set `GH_REPO=<owner>/<repo>` to override it. It rebuilds the installer, downloads the release's `install.sh`, compares the two checksums, copies the file to the server and replaces the live one in a single step after checking the checksum there. Use `--dry-run` to see the steps first: it prints the expected checksum and the target path (`<host>:/opt/hatamoto/public/install.sh`) and calls nothing remote. When the Caddy handle for `/install.sh` changed, apply the Caddy file with `deploy/apply-caddy.sh` as well (`--dry-run` validates and shows the diff only). Republish the installer after any change to the signing keys, because it carries them.

## Channels

Rollout is controlled by three channel manifests, `canary`, `edge` and `stable`. Each lists releases with a **ramp**: percentages of installs that are offered the release from given times, for example 5 percent now, 25 percent after two days and 100 percent after four. A channel can also halt a release or yank one with a reason and a replacement. The commands edit the unsigned channel sources (`channel.json` files in a directory you pass with `--dir`):

```bash
cd backend
go run ./tools/release channel add stable v1.2.3 --record release.json --sig release.json.sig --ramp now:5,+2d:25,+4d:100 --dir DIR
go run ./tools/release channel ramp stable v1.2.3 --ramp now:25,+1d:100 --dir DIR
go run ./tools/release channel halt stable v1.2.3 --reason "regression in the editor" --dir DIR
go run ./tools/release channel yank stable v1.2.3 --reason "bad migration" --to v1.2.2 --dir DIR
```

Ramp steps are `TIME:PERCENT`, where the time is `now`, `+Nd` or `+Nh`. Percentages never fall and times strictly rise.

The two keys are deliberately separate. The release key signs what a release is and lives offline. The manifest key signs which release is currently offered and lives on the maintainer's signing machine, never in CI. Compromising the manifest key can therefore halt or delay a rollout, but it cannot make a client accept a binary that the release key did not sign. Clients check both signatures. See [Trust and signing](../updates/trust-and-signing.md).

## Channel signing

The unsigned channel sources are public: `releases/channels/{canary,edge,stable}.json` in this repository. They change only through pull requests made with `go run ./tools/release channel ...`, never by hand, and `TestCommittedChannelSourcesParse` checks them on every change.

Signing and upload happen off GitHub, on the maintainer's signing machine. After a channel change is merged, and once a day, that machine signs the three channel files at the head of `main` with the manifest key, verifies each one against the embedded trust, and uploads them with a deploy key that the release server pins to the channels directory (`rrsync -wo -munge`, `--no-links`). A run that signed an older `main` than the current head publishes nothing, so a stale run cannot overwrite newer state. Neither key, nor any workflow that could use one, is in this repository: `scripts/test-channels-public.sh` (part of `make check`) fails on a channel signing workflow, a channel secret name or a tracked key file. An outside contributor's pull request can therefore never reach the manifest key.

A signed manifest is valid for 14 days, so boxes keep updating for two weeks even if the signing machine is down.

### Freshness alarm

Re-signing can stop without anyone noticing: a failed daily run, a machine that is off or lost. Boxes then stop updating when the manifest expires. Two alarms run `scripts/check-channel-freshness.sh`, which downloads the three **live** manifests from `https://hatamoto.dev/releases/channels/` and fails, with an error annotation per channel, when any `expires_at` is too close. A healthy channel is never closer than 13 days. It reads the files that boxes read, so it also catches a broken upload.

- The `channels-freshness` workflow in this repository runs daily. It needs no secret and does not depend on the signing machine. Under 11 days left (about three missed re-signs) it opens the `channels-freshness` issue, or comments on the open one; under 5 days the run fails too; once the manifests are fresh again it closes the issue.
- A user timer on the signing machine (`deploy/channels/`) runs the same check daily with a 7 day limit.

Run it by hand any time:

```bash
scripts/check-channel-freshness.sh                 # all three channels, 7 days
scripts/check-channel-freshness.sh --min-days 12   # stricter, e.g. right after a re-sign
```

## Checklist for a maintainer

1. Merge to `main` with `make check` green, including the n-1 job.
2. Tag with `scripts/tag-release.sh --push vX.Y.Z`.
3. Wait for the draft release, then rebuild locally and compare sha256 sums if you want to confirm reproducibility.
4. Review the record, set `rollback_floor` and `min_from`, and sign it offline.
5. Publish the signed files with `scripts/publish-release.sh`.
6. Verify the attestations, add the signature to the draft and publish the GitHub release (above), then publish the installer with `scripts/publish-install.sh`.
7. Add the release to `canary`, `edge` and `stable` with a ramp (`releases/channels/` in this repository), merge the channel change, then re-sign and upload the channels on the signing machine.
8. Watch the update status of a test install. Halt the ramp if anything regresses.
9. If the first-party plugin registry is near expiry, rebuild it. See [SDK and registry](sdk-and-registry.md).
