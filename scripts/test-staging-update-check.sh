#!/usr/bin/env bash
# test-staging-update-check.sh - offline checks of scripts/staging-update-check.sh:
# syntax, argument validation (exit 64) and the preflight (exit 2) with no
# tools on PATH. It never starts a box or touches the network. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
S=scripts/staging-update-check.sh
bash -n "$S"
fail() { echo "FAIL: $*"; exit 1; }

bash "$S" --help | grep -q 'staging-update-check' || fail "--help prints nothing useful"
set +e
out=$(bash "$S" --expect not-a-version 2>&1); rc=$?
set -e
[ "$rc" = 64 ] || fail "--expect not-a-version exited $rc, want 64: $out"
echo "PASS: bad --expect exits 64"
set +e; out=$(bash "$S" 2>&1); rc=$?; set -e
[ "$rc" = 64 ] || fail "missing --expect exited $rc, want 64"
set +e; out=$(bash "$S" --expect v0.25.0 --box c 2>&1); rc=$?; set -e
[ "$rc" = 64 ] || fail "bad --box exited $rc, want 64"
echo "PASS: missing --expect and bad --box exit 64"
# --channel: default edge, canary/edge/stable accepted, anything else refused.
set +e; out=$(bash "$S" --expect v0.25.0 --channel nightly 2>&1); rc=$?; set -e
[ "$rc" = 64 ] || fail "bogus --channel exited $rc, want 64"
set +e; out=$(bash "$S" --expect v0.25.0 --channel 2>&1); rc=$?; set -e
[ "$rc" = 64 ] || fail "--channel without a value exited $rc, want 64"
grep -Eq '^EXPECT="" .* CHAN=edge ' "$S" || fail "--channel does not default to edge"
for c in canary edge stable; do
  set +e; out=$(PATH=/nonexistent "$(command -v bash)" "$S" --expect v0.25.0 --channel "$c" 2>&1); rc=$?; set -e
  [ "$rc" = 2 ] || fail "--channel $c exited $rc, want 2 (accepted, then preflight): $out"
done
echo "PASS: --channel defaults to edge, accepts canary|edge|stable and refuses others"

# The bootstrap must be older than --expect (a box newer than the release never updates).
for bad in v0.25.0 v0.25.1-dev.3 v0.26.0-dev.1 v1.0.0-dev.1; do
  set +e; out=$(bash "$S" --expect v0.25.0 --bootstrap-version "$bad" 2>&1); rc=$?; set -e
  [ "$rc" = 64 ] && grep -q 'not older than' <<<"$out" || fail "--bootstrap-version $bad: exit $rc, want 64: $out"
done
set +e; out=$(bash "$S" --expect v0.25.0 --bootstrap-version nonsense 2>&1); rc=$?; set -e
[ "$rc" = 64 ] || fail "bad --bootstrap-version exited $rc, want 64"
echo "PASS: a bootstrap version not older than --expect exits 64"
# The script must not stamp from scripts/version.sh (newer than the tag after release).
if grep -n 'scripts/version.sh' <(grep -v '^#' "$S"); then fail "the staging script stamps from version.sh"; fi

empty=$(mktemp -d); trap 'rm -rf "$empty"' EXIT
set +e; out=$(PATH="$empty" "$(command -v bash)" "$S" --expect v0.25.0 2>&1); rc=$?; set -e
[ "$rc" = 2 ] && grep -q '^STAGING PREFLIGHT:' <<<"$out" || fail "no tools on PATH: exit $rc, output: $out"
echo "PASS: missing tools exit 2 with a STAGING PREFLIGHT line"

# --halt must prove the box checked, not just that nothing was downloaded.
eval "$(sed -n '/^# BEGIN halt_proof/,/^# END halt_proof/p' "$S")"
NOW=$(date -u -d '2026-10-18T12:00:00Z' +%s)
ok_status() { printf 'version     v0.0.0-dev.1 (channel edge, mode auto)\nstatus      %s\nlast check  %s %s\n' "$1" "$2" "$3"; }
want_proof() { # want_proof <0|1> <label> <since> <status text>
  local rc=0; halt_proof "$3" "$4" >/dev/null || rc=$?
  [ "$rc" = "$1" ] || fail "halt_proof: $2 (rc=$rc, want $1)"
}
want_proof 0 "fresh ok check, up to date" "$NOW" "$(ok_status 'up to date: v0.0.0-dev.1 is already running' 2026-10-18T12:05:00Z ok)"
want_proof 0 "fresh ok check naming the halt" "$NOW" "$(ok_status 'halted: v0.25.0 is halted' 2026-10-18T12:05:00Z ok)"
want_proof 1 "never checked" "$NOW" "$(printf 'status      no check yet\nlast check  never\n')"
want_proof 1 "check before the halt window" "$NOW" "$(ok_status 'up to date' 2026-10-18T11:00:00Z ok)"
want_proof 1 "check errored" "$NOW" "$(ok_status 'up to date' 2026-10-18T12:05:00Z error)"
want_proof 1 "blocked status" "$NOW" "$(ok_status 'blocked: v0.25.0 needs v0.0.0 first' 2026-10-18T12:05:00Z ok)"
want_proof 1 "no status text" "$NOW" ""
echo "PASS: halt_proof needs a fresh successful check that is up to date or halted"
grep -q 'update check by hand exits 0' "$S" || fail "--halt does not assert the hand check exits 0"

# The normal wait stops early only when the box can no longer reach --expect.
eval "$(sed -n '/^# BEGIN stuck_reason/,/^# END stuck_reason/p' "$S")"
st() { printf 'version     v1.0.0-dev.1 (channel canary, mode auto)\nstatus      %s\nlast check  2026-10-28T12:27:23Z %s\n' "$1" "${2:-ok}"; }
want_stuck() { # want_stuck <0|1> <label> <status text> [current]
  local rc=0; stuck_reason "$3" "${4:-}" v1.0.2 >/dev/null || rc=$?
  [ "$rc" = "$1" ] || fail "stuck_reason: $2 (rc=$rc, want $1)"
}
want_stuck 0 "major needs a human" "$(st 'available major: v1.0.2 is a major release; apply it by hand')"
want_stuck 0 "switched to another release" "$(st 'busy: v0.32.0 has not been proven yet' skipped)" v0.32.0
want_stuck 0 "up to date" "$(st 'up to date: v1.0.0-dev.1 is already running')"
want_stuck 0 "blocked" "$(st 'blocked: v1.0.2 needs v1.0.1 first')"
want_stuck 0 "rolled back" "$(st 'rolled back v1.0.2: health gate failed')"
want_stuck 0 "check errored" "$(st 'no check yet' error)"
want_stuck 1 "no check yet" "$(printf 'status      no check yet\nlast check  never\n')"
want_stuck 1 "scheduled" "$(st 'scheduled: v1.0.2 at 2026-10-28T12:30:00Z')"
want_stuck 1 "applying" "$(st 'applying: v1.0.2')"
want_stuck 1 "staged" "$(st staged)"
want_stuck 1 "busy on the dev build" "$(st 'busy: switching' skipped)" v1.0.0-dev.1
want_stuck 1 "already on the target" "$(st 'busy: v1.0.2 has not been proven yet' skipped)" v1.0.2
want_stuck 1 "no status text" ""
echo "PASS: stuck_reason stops the wait only for states the box cannot leave by itself"

# The script must stay a production-only check.
if grep -nE 'echo "HATAMOTO_(TEST_|UPDATE_URL)|-e "?HATAMOTO_(TEST_|UPDATE_URL)|-tags[ =]|TAGS=' "$S"; then
  fail "the staging script sets a test seam or an update URL"
fi
echo "staging script tests ok"
