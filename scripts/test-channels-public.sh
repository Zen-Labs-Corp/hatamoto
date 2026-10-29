#!/usr/bin/env bash
# test-channels-public.sh - the channel sources are public, the channel keys never
# are. Run by `make check`.
#
# releases/channels/{canary,edge,stable}.json (the unsigned channel sources) are
# tracked here and ship in `git archive`. They are signed with the manifest key and
# uploaded on the maintainer's signing machine; no key and no signing or upload
# step is ever on GitHub. So this repository must not carry:
#   - a channel signing workflow (.github/workflows/channels.yml),
#   - a workflow or file that names the manifest key or upload secrets,
#   - a key file.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

CHANNELS=(canary edge stable)
SECRET_NAMES='HATAMOTO_MANIFEST_KEY|RELEASES_SSH_|release-channels'
# Files that may name the secrets: this test and the workflow tests (names only, as strings to look for).
NAME_ALLOWED=(scripts/test-channels-public.sh backend/internal/release/workflows_test.go)

for c in "${CHANNELS[@]}"; do
  f=releases/channels/$c.json
  git ls-files --error-unmatch "$f" >/dev/null 2>&1 || fail "$f is not tracked"
  [ "$(git check-attr export-ignore -- "$f" | awk '{print $NF}')" != set ] || fail "$f is export-ignore"
done
extra=$(git ls-files releases/channels | grep -vxE 'releases/channels/(canary|edge|stable)\.json' || true)
[ -z "$extra" ] || fail "unexpected files in releases/channels: $extra"
pass "channel sources tracked and shipped (canary, edge, stable)"

[ -z "$(git ls-files .github/workflows/channels.yml)" ] || fail ".github/workflows/channels.yml is tracked: channels are never signed on GitHub"
excl=(); for f in "${NAME_ALLOWED[@]}"; do excl+=(":!$f"); done
if git grep -nIE "$SECRET_NAMES" -- . "${excl[@]}" ':!.pi'; then fail "a tracked file names a channel signing or upload secret (above)"; fi
pass "no channel signing workflow, no channel secret named"

keys=$(git ls-files | grep -E '(^|/)[^/]*\.key$|(^|/)releases-deploy$' || true)
[ -z "$keys" ] || fail "key files are tracked: $keys"
pass "no key file tracked"
