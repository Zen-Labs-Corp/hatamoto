# releases/

This repository does not hold release channel state. Which release each channel
(`stable`, `edge`, `canary`) offers, and the manifest key that signs it, live in a
private operations repository, so that a leaked CI secret or a channel change never
travels with the source. Channel manifests are served, signed, from
`https://hatamoto.dev/releases/channels/<channel>.json`.

What is here: the release tooling (`backend/tools/release`), the build
(`make release-build`), the release workflow (`.github/workflows/release.yml`) and
the process, in [`docs/site/migrate-contribute/release-process.md`](../docs/site/migrate-contribute/release-process.md).
To check that a download is what we released, read
[Verify a download](../docs/site/updates/verify-a-download.md).
