---
title: Signing keys and fingerprints
nav_title: Signing keys
description: The public keys Hatamoto trusts, their fingerprints and where to check them, and what happens when a key is rotated or leaks.
---

This page lists every public key that Hatamoto trusts to sign something you run: a release record, a channel manifest, a first-party plugin or theme package, and, once it exists, a release tag. Compare a fingerprint here with the one your own copy of the source produces before you rely on a download. How the keys are used in an update is described in [Trust and signing](trust-and-signing.md).

## The keys

A key id is the first 16 hex characters of the SHA-256 of the raw 32-byte ed25519 public key. The fingerprint is the full SHA-256 of the same bytes, in hex.

| Key | Signs | Key id | SHA-256 fingerprint |
|---|---|---|---|
| Release key, primary | Release records (`release-primary.pub`) | `d7effd9870698586` | `d7effd9870698586836179975dd8a6b71b94401d781d5738fd1a17e7936eed7d` |
| Release key, backup | Release records (`release-backup.pub`) | `a90b01139fbdc440` | `a90b01139fbdc4405e84c51f490d31f964a0e5d81344ce383a7b19fdc58576f0` |
| Manifest key | Channel manifests (`manifest.pub`) | `c15ece9d6b1fc201` | `c15ece9d6b1fc2012673d5ddf87a9736ac39f6d0716fa9ec9031524a4a4b89aa` |
| First-party registry key | First-party plugins and themes in the default registry | `adea8e00f5ac74ca` | `adea8e00f5ac74caf9109a7d1ad3f72fa18d0cb2e328497fe4ead672b976be88` |
| Release tag key (SSH, `release@hatamoto.dev`) | Git tags of releases (`vX.Y.Z`, `backend/vX.Y.Z`) | none (SSH key) | `SHA256:Mu3tL5oxF9Y4/Jqjri/c+aNMpHsp99b+KACNanVV79c` (as `ssh-keygen -l` prints it) |

The release and manifest keys are the files in `backend/internal/release/keys/`. The first-party registry key is the constant `FirstPartyKey` in `backend/internal/config/packages.go`. The tag key is the `release@hatamoto.dev` line in `.github/allowed_signers`; `git tag -v` with that file as `gpg.ssh.allowedSignersFile` checks a release tag (`scripts/verify-tag-signature.sh` does exactly that). It is a software ed25519 key held offline by the release owner (file mode 600, never in a synced folder); a rotation adds the new line first and updates this row.

## Check a fingerprint yourself

The public keys are base64 text. These commands print the fingerprints above from a checkout of the source:

```bash
cd hatamoto
for f in backend/internal/release/keys/*.pub; do
  pub=$(jq -r .public "$f")
  echo "$f $(jq -r .key_id "$f") $(printf '%s' "$pub" | base64 -d | sha256sum | cut -d' ' -f1)"
done

pub=$(grep -o 'FirstPartyKey = "[^"]*' backend/internal/config/packages.go | cut -d'"' -f2)
echo "first-party $(printf '%s' "$pub" | base64 -d | sha256sum | cut -d' ' -f1)"
```

The first 16 characters of each fingerprint are the key id. The program refuses to start with an embedded release or manifest key whose id does not match its public key. The first-party key is compiled into every binary, and the install and update prompts in the admin show the signer id of the package.

To check the tag signature once the tag key is published, use `git tag -v vX.Y.Z` with the published public key in your `allowed_signers` file.

## Where each private key lives

| Key | Private half |
|---|---|
| Release keys | Offline, used only when a release is cut. |
| Manifest key | A secret of the automated job that re-signs the channels. |
| First-party registry key | Held by the release manager outside the repository. It is read from `HATAMOTO_REGISTRY_KEY_FILE` when the registry is built. |
| Release tag key | A software key held offline by the release owner. |

No private key is stored in the repository: a test fails the build when one is tracked.

## Rotation

- **Release key.** Binaries embed two release public keys and accept a record signed by either. A new key is added in a release signed by an existing key. Release keys are matched by the key id in front of each signature, so older binaries keep working while both are listed. The old key is removed in a later release.
- **Manifest key.** Several manifest keys can be embedded at once, so a new one can be embedded in a release before the CI secret is switched.
- **First-party registry key.** A release that rotates the key ships the new public key in `config.FirstPartyKey` and re-signs the hosted registry. The key was rotated once, on 2026-10-16, because the earlier private key was no longer available. The retired key id is `a592a1276b3947b1`. A package that shows as blocked with that signer was signed by the retired key: download it again from the registry, which is signed with the current key.
- **Tag key.** A new key is published on this page and in the release notes of the first tag it signs, and the old one is listed here as retired.

## If a key leaks

- **Manifest key.** An attacker could halt or yank a release or try to hold back an update. They could not ship code, because every binary hash comes from the record, which the release key signs. The key is replaced in the next release and the CI secret switched.
- **Release key.** This is the serious case. Anything signed with the leaked key must be treated as untrusted. The response is a release signed with the other release key that removes the leaked one, announced in a security advisory. Installs that have not updated still trust the leaked key until they do, so the advisory states how to check and how to update by hand. An attacker would also need to put the forged record into a manifest or serve it to you, since the manifest key signs what is offered.
- **First-party registry key.** Every install that trusts it would accept a package signed with it until upgraded. Set `HATAMOTO_TRUST_FIRST_PARTY=false` to stop trusting it at once and trust only `HATAMOTO_TRUSTED_KEYS`.
- **Tag key.** Tags signed with it are untrusted from the date in the advisory. Binaries are not affected: they verify the release and manifest keys, never the tag.

Report a suspected leak or a mismatch between this page and the source to the security address in `SECURITY.md` at the root of the repository, never in a public issue.
