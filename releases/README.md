# releases/

`channels/{canary,edge,stable}.json` are the unsigned channel sources: which release
each channel offers, at what rollout, and which are halted or yanked. They change only
through pull requests made with the release tool (`go run ./tools/release channel ...`
from `backend/`), never by hand.

They are signed with the manifest key and uploaded off GitHub, on the maintainer's
signing machine, after every merged change and once a day (a signed manifest is valid
for 14 days). No key and no signing workflow is in this repository. Boxes read the signed
manifests from `https://hatamoto.dev/releases/channels/<channel>.json`.

The release tooling is `backend/tools/release`, the build is `make release-build`, the
release workflow is `.github/workflows/release.yml`, and the process is in
[`docs/site/migrate-contribute/release-process.md`](../docs/site/migrate-contribute/release-process.md).
To check that a download is what we released, read
[Verify a download](../docs/site/updates/verify-a-download.md).
