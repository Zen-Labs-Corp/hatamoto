#!/usr/bin/env bash
# test-check-dco.sh - offline test of scripts/check-dco.sh. Run by `make check`.
set -euo pipefail
S="$(git rev-parse --show-toplevel)/scripts/check-dco.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
g() { git -C "$T" -c commit.gpgsign=false -c core.hooksPath=/dev/null "$@"; }
c() { g -c user.name="$1" -c user.email="$2" commit -q --allow-empty "${@:3}"; }
g init -q -b main
c Base base@example.com -m base; base=$(g rev-parse HEAD)
c Ann ann@example.com -s -m "signed"
(cd "$T" && bash "$S" "$base" HEAD >/dev/null) || fail "a signed-off commit was refused"
c Ann Ann@Example.com -m "case" -m "Signed-off-by: Ann <ann@example.com>"
(cd "$T" && bash "$S" "$base" HEAD >/dev/null) || fail "the email match is not case-insensitive"
c Bob bob@example.com -m "unsigned"
o=$(cd "$T" && bash "$S" "$base" HEAD 2>&1) && fail "an unsigned commit passed"
grep -q 'FAIL .* unsigned' <<<"$o" || fail "the unsigned commit is not named: $o"
g reset -q --hard HEAD~1
c Bob bob@example.com -m "someone else's sign-off" -m "Signed-off-by: Ann <ann@example.com>"
(cd "$T" && bash "$S" "$base" HEAD >/dev/null 2>&1) && fail "a sign-off by another person passed"
g reset -q --hard HEAD~1
g checkout -q -b side; c Cy cy@example.com -s -m side; g checkout -q main
g -c user.name=M -c user.email=m@example.com merge -q --no-ff --no-edit side
(cd "$T" && bash "$S" "$base" HEAD >/dev/null) || fail "a merge commit without sign-off was judged"
echo "check-dco tests ok"
