#!/usr/bin/env bash
# test-deploy-target.sh - offline test of scripts/deploy-target.sh.
set -Eeuo pipefail
top=$(git rev-parse --show-toplevel)
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
git -C "$W" init -q
cp "$top/scripts/deploy-target.sh" "$W/deploy-target.sh"
unset DEPLOY_TARGET
resolve() { (cd "$W" && bash ./deploy-target.sh 2>&1); }

[[ $(resolve) == box ]] || fail "absent must be box"
pass "absent = box"

echo 'hmctl:example-site' >"$W/.deploy-target"
[[ $(resolve) == 'hmctl example-site' ]] || fail "file not read: $(resolve)"
pass "file read"

[[ $(DEPLOY_TARGET=hmctl:other-site resolve) == 'hmctl other-site' ]] || fail "env must win over file"
pass "env wins over file"

[[ $(DEPLOY_TARGET='' resolve) == box ]] || fail "empty env must be box"
[[ $(DEPLOY_TARGET=box resolve) == box ]] || fail "env box must be box"
pass "empty/box env = box"

rm -f "$W/.deploy-target"
for bad in hmctl: hmctl:Example hmctl:a hmctl:1abc hmctl:abc- hmctl:a--b hmctl:ab_c cell:example-site hmctl:abcdefghijklmnopqrstuvwxyzabcd; do
  if DEPLOY_TARGET=$bad resolve >/dev/null; then fail "bad target accepted: $bad"; fi
done
out=$(DEPLOY_TARGET=hmctl:Bad resolve) || true
grep -q 'bad DEPLOY_TARGET' <<<"$out" || fail "no message on bad target: $out"
echo 'hmctl:bad_slug' >"$W/.deploy-target"
if resolve >/dev/null; then fail "bad file slug accepted"; fi
pass "bad slugs rejected"
echo "ALL OK"
