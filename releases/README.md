# releases/

`channels/{canary,edge,lts,stable}.json` are the unsigned channel sources: which release
each channel offers, at what rollout, and which are halted or yanked. They change only
through pull requests made with the release tool (`go run ./tools/release channel ...`
from `backend/`), never by hand.

They are signed with the manifest key and uploaded off GitHub, on the maintainer's
signing machine, after every merged change and once a day (a signed manifest is valid
for 14 days). No key and no signing workflow is in this repository. Boxes read the signed
manifests from `https://hatamoto.dev/releases/channels/<channel>.json`.

`lts.json` lists only releases of long-term support lines (see the release process
docs). It started as an empty source; the maintainer's signing run must include it
before the live `lts.json` exists, and until then the freshness check reports it missing.

The release tooling is `backend/tools/release`, the build is `make release-build`, the
release workflow is `.github/workflows/release.yml`, and the process is in
[`docs/site/migrate-contribute/release-process.md`](../docs/site/migrate-contribute/release-process.md).
To check that a download is what we released, read
[Verify a download](../docs/site/updates/verify-a-download.md).

When the repository opts in to another edition, a tag's draft exists only when every
opted-in edition's build arrived from a successful dispatched run, matched its manifest
(tag, commit, per-dispatch nonce) and its checksums, and is in the unsigned record;
otherwise the release run fails and nothing is drafted. See
[`release-process.md`](../docs/site/migrate-contribute/release-process.md).
