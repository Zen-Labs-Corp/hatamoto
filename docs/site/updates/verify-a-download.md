---
title: Verify a download
nav_title: Verify a download
description: Check that a Hatamoto binary or Docker image is the one we built and signed, before you run it, with the GitHub CLI, sha256sum and openssl.
next: trust-and-signing.md, manual-update-cli.md
---

In this guide you check a Hatamoto download against three independent proofs before you run it: the build attestation GitHub recorded, the checksum list, and the release record that we sign offline with a key that never touches the build. You do not need a Hatamoto binary for any of it. The updater inside Hatamoto runs the last check for you on every update; this guide is for the first download and for anyone who wants to check by hand.

## Before you start

- The files of one release from the [releases page](https://github.com/Zen-Labs-Corp/hatamoto/releases): the bundle for your platform (`hatamoto-linux-amd64.tar.gz` and so on, holding the binary, `LICENSE`, `NOTICE` and `THIRD_PARTY_LICENSES/`), or the bare binary `hatamoto-<os>-<arch>`, plus `SHA256SUMS`, `release.json` (the **release record**: version and the SHA-256 of every binary) and `release.json.sig` (its signature). A release appears there only after the record is signed.
- The [GitHub CLI](https://cli.github.com) (`gh`, version 2.49 or later), `jq`, `sha256sum` and `openssl` 3.
- A copy of the source at the release tag, or just the three public key files from `backend/internal/release/keys/` in the repository. Take them from the repository, never from the download.

## Steps

1. Check the build attestation. It shows GitHub recorded the file as built by this repository's `release` workflow for the tag, not on a person's laptop. The workflow file comes from the tagged commit, so the protection of release tags is what keeps that honest; step 3 is the proof that we released it:

   ```bash
   gh attestation verify hatamoto-linux-amd64.tar.gz \
     --repo Zen-Labs-Corp/hatamoto \
     --signer-workflow Zen-Labs-Corp/hatamoto/.github/workflows/release.yml \
     --source-ref refs/tags/vX.Y.Z
   ```

   Run it for the bundle and for the bare binary if you use that. The SBOMs (the list of every Go module inside the binary) are attested too, in SPDX and CycloneDX form. Add `--predicate-type https://spdx.dev/Document/v2.3` to verify the SPDX one.

2. Check the file against the checksum list:

   ```bash
   sha256sum --ignore-missing -c SHA256SUMS
   ```

   Every file you downloaded must print `OK`.

   `SHA256SUMS` is itself attested (`gh attestation verify SHA256SUMS --repo Zen-Labs-Corp/hatamoto --signer-workflow Zen-Labs-Corp/hatamoto/.github/workflows/release.yml`), so someone who replaced a release file cannot also quietly replace the list. Without that check it is only a transfer check: the release record in step 3 is the proof.

3. Check the signed release record. First read the fingerprint of the signing key from the signature file, and compare it with the key in the repository:

   ```bash
   cut -d: -f1 release.json.sig
   jq -r .key_id backend/internal/release/keys/release-primary.pub
   ```

   The two lines must match. The key id is the first 16 hex characters of the SHA-256 of the raw public key, so you can also compute it yourself:

   ```bash
   jq -r .public backend/internal/release/keys/release-primary.pub | base64 -d | sha256sum | cut -c1-16
   ```

   The release key fingerprints are `d7effd9870698586` (primary) and `a90b01139fbdc440` (backup, used only if the primary is lost). A signature with any other id is not ours. Then verify the signature over the exact bytes of the record. It is a raw Ed25519 signature, so wrap the 32-byte key in a standard header and ask openssl:

   ```bash
   { printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; \
     jq -r .public backend/internal/release/keys/release-primary.pub | base64 -d; } \
     | openssl pkey -pubin -inform DER -out release.pem
   cut -d: -f2- release.json.sig | base64 -d > release.sig.bin
   openssl pkeyutl -verify -pubin -inkey release.pem -rawin -in release.json -sigfile release.sig.bin
   ```

4. Compare the binary with the record. If you downloaded the bundle, unpack it first. The record holds the hash of the bare binary:

   ```bash
   tar -xzf hatamoto-linux-amd64.tar.gz
   jq -r '.artifacts["linux-amd64"].sha256' release.json
   sha256sum hatamoto-linux-amd64/hatamoto
   ```

   The two hashes must be equal. Replace `linux-amd64` with your platform.

5. For the Docker image, check the keyless signature and the SBOM attestation. The image is built by the `image` workflow, which a maintainer starts from `main` after the release is signed and published. Its identity is that workflow file **on `main`**, not a tag:

   ```bash
   cosign verify ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z \
     --certificate-identity "https://github.com/Zen-Labs-Corp/hatamoto/.github/workflows/image.yml@refs/heads/main" \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     -a version=vX.Y.Z
   ```

   The identity is the same for every release, so `-a version=vX.Y.Z` is what ties the signature to the version: we sign each image with `version` and `commit` annotations, and an older signed image that someone re-tagged as `vX.Y.Z` fails this check. Use the version you meant to install, not the tag you pulled. Use `cosign verify-attestation --type spdxjson` with the two identity flags for the SBOM (it cannot carry annotations; it is bound to the image digest that the annotated signature covers). Pin the image by the digest that command prints when you deploy it. An identity ending in a tag (`@refs/tags/...`) or in another workflow file is not ours: do not run that image. The image workflow refuses a tag that is not signed by a listed maintainer key, not on `main`, or without a published release, but the cosign identity itself only proves "built by the image workflow on main". The signed release record is still the proof that we released a version. The image's binary is rebuilt from the verified tag by the Dockerfile, so it is not the binary `release.json` hashes: the record proves the release, not the image's bytes, and the annotated signature and digest are the proof for the image.

## Check the installer script

If you install on a new server with the `install.sh` script (see [Install on a VPS](../getting-started/install-vps.md)), you can check that script the same way before you run it. It is attached to each release and attested by the same workflow:

```bash
curl -fsSLO https://hatamoto.dev/install.sh
gh attestation verify install.sh \
  --repo Zen-Labs-Corp/hatamoto \
  --signer-workflow Zen-Labs-Corp/hatamoto/.github/workflows/release.yml
```

The script is not tied to one version: it carries the three public keys (the release primary, the release backup and the manifest key, see [the two keys](trust-and-signing.md#the-two-keys)) and uses them to check the release list and the binary it installs, so it picks the newest stable release, or the installer's minimum if that is newer, unless you set `HATAMOTO_VERSION`. Because the keys are inside the script, a key rotation means we publish a new installer: an old copy keeps trusting the old keys.

## Check it worked

Step 1 ends with `Verification succeeded!`, step 2 prints `OK` for each file, step 3 prints `Signature Verified Successfully` and step 4 shows two identical hashes. Three agreeing proofs mean the file is what we built, listed and signed.

If step 3 prints `Signature Verification Failure`, the record was changed after signing or the key is not ours: stop and do not run the binary. If step 1 says no attestation was found, you may have an old release or a copy that was not built by the workflow. Report a mismatch at `security@hatamoto.dev` with the file names and the output.

What the checks do not cover: they say the binary matches what the project signed, not that the project's code is free of bugs. The [Trust and signing](trust-and-signing.md) page lists what each key protects and what a leaked key could do.
