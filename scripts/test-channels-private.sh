#!/usr/bin/env bash
# test-channels-private.sh - the channel signing state never ships in the public
# repository. Run by `make check`, in both repositories:
#
#   private repo (releases/channels is tracked): the channel sources and the two
#     channel workflows are export-ignore and absent from `git archive HEAD`, and
#     scripts/public-snapshot.sh, run on a copy of HEAD, produces a tree without
#     them or the secrets they use;
#   public repo (nothing tracked there): no channel source, no channel workflow
#     and no reference to the channel secrets is tracked.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

PRIVATE_FILES=(releases/channels .github/workflows/channels.yml .github/workflows/channels-freshness.yml)
SECRET_NAMES='HATAMOTO_MANIFEST_KEY|RELEASES_SSH_|release-channels'
# Files that may name the secrets: this test and the workflow tests (names only, as strings to look for).
NAME_ALLOWED=(test-channels-private.sh workflows_test.go)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

if git ls-files --error-unmatch releases/channels >/dev/null 2>&1 || [ -n "$(git ls-files releases/channels)" ]; then
  # --- the private (ops) side ------------------------------------------------
  for p in "${PRIVATE_FILES[@]}"; do
    [ -n "$(git ls-files "$p")" ] || fail "$p is not tracked here, so this test would prove nothing"
  done
  git ls-files -z releases/channels .github/workflows/channels.yml .github/workflows/channels-freshness.yml \
    | git check-attr -z --stdin export-ignore \
    | awk -v RS='\0' 'NR%3==1{p=$0} NR%3==0 && $0!="set"{print p}' > "$T/not-ignored"
  [ ! -s "$T/not-ignored" ] || fail "not export-ignore: $(tr '\n' ' ' < "$T/not-ignored")"
  pass "channel sources and workflows are export-ignore"
  git archive --format=tar HEAD | tar -t > "$T/archive"
  grep -E '^(releases/channels|\.github/workflows/channels(-freshness)?\.yml)' "$T/archive" && fail "channel files are in git archive HEAD"
  grep -q '^releases/README.md$' "$T/archive" || fail "releases/README.md (the public note) is not shipped"
  pass "git archive HEAD holds no channel file; releases/README.md ships"
  # The snapshot script on a copy of HEAD (its own checks refuse a leaking tree).
  git clone -q --no-hardlinks . "$T/copy"
  ( cd "$T/copy" && bash scripts/public-snapshot.sh --author-name "Example Org" --author-email "release@example.org" --out "$T/snap" >/dev/null ) \
    || fail "public-snapshot.sh failed on a copy of HEAD"
  for p in "${PRIVATE_FILES[@]}"; do [ ! -e "$T/snap/$p" ] || fail "$p is in the public snapshot"; done
  if grep -rIlE "$SECRET_NAMES" "$T/snap" --exclude-dir=.git "${NAME_ALLOWED[@]/#/--exclude=}" | grep .; then fail "the snapshot names a channel secret (files above)"; fi
  pass "public snapshot: no channel file, no channel secret name"
else
  # --- the public side ---------------------------------------------------------
  for p in "${PRIVATE_FILES[@]}"; do [ -z "$(git ls-files "$p")" ] || fail "$p is tracked in a repository that must not carry channel state"; done
  if git grep -nIE "$SECRET_NAMES" -- . ':!scripts/test-channels-private.sh' ':!backend/internal/release/workflows_test.go' ':!.pi'; then fail "a tracked file names a channel secret (above)"; fi
  pass "no channel state tracked"
fi
