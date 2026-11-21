#!/usr/bin/env bash
# test-edition-artifacts.sh - tests scripts/edition-artifacts.sh offline: a stub
# `gh` stands in for the edition build repository, a fake token must never be
# printed. No network, no real token. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
SCRIPT="$PWD/scripts/edition-artifacts.sh"
bash -n "$SCRIPT"

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
mkdir -p "$W"/{stubs,out}
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

ED=$(sed -n 's/^var Editions = \[\]string{"\([a-z0-9]*\)".*/\1/p' backend/internal/release/release.go)
[[ -n $ED ]] || fail "cannot read the edition name from backend/internal/release/release.go"
PLATS=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
TOKEN=tok-NEVER-PRINT-123
TAG=v0.25.0
SHA=0123456789abcdef0123456789abcdef01234567

cat > "$W/stubs/gh" <<'GH'
#!/usr/bin/env bash
echo "gh $*" >> "$STUB_LOG"
case "$*" in
  *"-X POST"*)
    [[ ${STUB_MODE:-} == dispatch-fail ]] && exit 1
    for a in "$@"; do [[ $a == client_payload\[nonce\]=* ]] && echo "${a#*=}" > "$STUB_LOG.nonce"; done
    exit 0 ;;
  "api repos/"*artifacts*)
    case ${STUB_MODE:-} in
      http403) echo "gh: Resource not accessible (HTTP 403)" >&2; exit 1 ;;
      http502) echo "gh: Bad Gateway (HTTP 502)" >&2; exit 1 ;;
      stale) echo '{"artifacts":[{"expired":false,"created_at":"2000-01-01T00:00:00Z","workflow_run":{"id":7}}]}' ;;
      none) echo '{"artifacts":[]}' ;;
      *) echo '{"artifacts":[{"expired":false,"created_at":"2999-01-01T00:00:00Z","workflow_run":{"id":42}}]}' ;;
    esac ;;
  "api repos/"*/actions/runs/*)
    ev=repository_dispatch st=completed co=success
    case ${STUB_MODE:-} in
      run-failed) co=failure ;; run-cancelled) co=cancelled ;; run-manual) ev=workflow_dispatch ;;
      run-pending) st=in_progress co=null ;;
    esac
    echo '{"event":"'$ev'","status":"'$st'","conclusion":'$( [[ $co == null ]] && echo null || echo "\"$co\"" )',"html_url":"https://example.test/runs/42"}' ;;
  "run download"*)
    while [[ $# -gt 0 ]]; do [[ $1 == -D ]] && d=$2; shift; done
    mkdir -p "$d"; cp "$STUB_SRC"/* "$d"/
    sed -i "s/@NONCE@/$(cat "$STUB_LOG.nonce")/" "$d/edition-build.json" ;;
esac
GH
chmod +x "$W/stubs/gh"
export PATH="$W/stubs:$PATH" STUB_LOG="$W/gh.log"
: > "$STUB_LOG"

make_src() { # make_src <dir>: the four edition files plus a matching SHA256SUMS
  local d=$1 p i=0
  rm -rf "$d"; mkdir -p "$d"
  for p in "${PLATS[@]}"; do i=$((i + 1)); head -c $((300 + i)) /dev/urandom > "$d/hatamoto-$ED-$p"; done
  (cd "$d" && sha256sum hatamoto-* > SHA256SUMS)
  # the manifest the edition build writes; @NONCE@ is replaced by the stub with the dispatched nonce
  printf '{"tag":"%s","sha":"%s","nonce":"@NONCE@"}\n' "$TAG" "$SHA" > "$d/edition-build.json"
}
snap() { (cd "$1" && find . -type f | LC_ALL=C sort | xargs -r sha256sum); }
OUT_N=0
run() { # run <env...> -- args: runs the script, output to $W/out/N, status in $RC
  OUT_N=$((OUT_N + 1)); RC=0
  env "$@" > "$W/out/$OUT_N" 2>&1 || RC=$?
}
reset_dist() { rm -rf "$W/dist"; mkdir -p "$W/dist"; echo keep > "$W/dist/hatamoto-linux-amd64"; }
FETCH=(bash "$SCRIPT" fetch --tag $TAG --sha $SHA --dist "$W/dist")
COMMON=(RELEASE_EDITIONS="$ED" EDITION_REPO=owner/edition-build GH_TOKEN="$TOKEN"
  EDITION_WAIT_SECONDS=2 EDITION_POLL_SECONDS=1 STUB_SRC="$W/src")
make_src "$W/src"
reset_dist; BEFORE=$(snap "$W/dist")

# --- ISC-1: no opt-in => skipped, no gh call
: > "$STUB_LOG"
run RELEASE_EDITIONS= GH_TOKEN="$TOKEN" "${FETCH[@]}"
[[ $RC -eq 0 ]] && grep -q skipped "$W/out/$OUT_N" && [[ ! -s $STUB_LOG ]] || fail "empty opt-in must skip without gh"
pass "empty opt-in skips and never calls gh"

# --- ISC-3: missing or malformed token / repo => fail before any gh call
: > "$STUB_LOG"
run RELEASE_EDITIONS="$ED" EDITION_REPO=owner/edition-build GH_TOKEN= "${FETCH[@]}"
[[ $RC -ne 0 ]] || fail "empty token must fail"
run "${COMMON[@]}" EDITION_REPO= "${FETCH[@]}"
[[ $RC -ne 0 ]] || fail "empty repo must fail"
run "${COMMON[@]}" EDITION_REPO='bad repo;x' "${FETCH[@]}"
[[ $RC -ne 0 ]] || fail "malformed repo must fail"
run "${COMMON[@]}" EDITION_REPO=justaname "${FETCH[@]}"
[[ $RC -ne 0 ]] || fail "repo without owner must fail"
[[ ! -s $STUB_LOG ]] || fail "gh was called before the inputs were checked"
pass "missing or malformed token and repo fail before any gh call"

# --- dispatch failure
run "${COMMON[@]}" STUB_MODE=dispatch-fail "${FETCH[@]}"
[[ $RC -ne 0 ]] && grep -q 'dispatch failed' "$W/out/$OUT_N" || fail "a failed dispatch must fail"
pass "dispatch failure fails"

# --- the dispatch carries tag, sha and a nonce
run "${COMMON[@]}" STUB_MODE=none "${FETCH[@]}"
grep -q "client_payload\[tag\]=$TAG" "$STUB_LOG" && grep -q "client_payload\[sha\]=$SHA" "$STUB_LOG" \
  && grep -qE 'client_payload\[nonce\]=[^ ]{12,}' "$STUB_LOG" || fail "dispatch lacks tag, sha or nonce"
pass "the dispatch sends tag, sha and a nonce"

# --- the producing run must be a finished successful repository_dispatch run; api errors surface
make_src "$W/src"
for mode in run-failed run-cancelled run-manual http403; do
  reset_dist; BEFORE=$(snap "$W/dist"); start=$SECONDS
  run "${COMMON[@]}" EDITION_WAIT_SECONDS=20 STUB_MODE=$mode "${FETCH[@]}"
  [[ $RC -ne 0 ]] && ! grep -q 'timed out' "$W/out/$OUT_N" || fail "$mode: must fail at once, not time out"
  (( SECONDS - start < 10 )) || fail "$mode: did not fail at once"
  [[ $(snap "$W/dist") == "$BEFORE" ]] || fail "$mode: dist changed"
done
grep -q 'HTTP 403' "$W/out/$OUT_N" || fail "a 403 must be reported"
run "${COMMON[@]}" STUB_MODE=run-pending "${FETCH[@]}"
[[ $RC -ne 0 ]] && grep -q 'timed out' "$W/out/$OUT_N" || fail "a run that never finishes must time out"
run "${COMMON[@]}" STUB_MODE=http502 "${FETCH[@]}"
[[ $RC -ne 0 ]] && grep -q 'timed out' "$W/out/$OUT_N" || fail "a 5xx must be retried until the deadline"
pass "failed, cancelled, manual and unfinished runs and api errors fail closed"

# --- ISC-2 / ISC-6: nothing fresh => timeout, dist unchanged
for mode in none stale; do
  start=$SECONDS
  run "${COMMON[@]}" STUB_MODE=$mode "${FETCH[@]}"
  [[ $RC -ne 0 ]] && grep -q 'timed out' "$W/out/$OUT_N" || fail "$mode: must time out"
  (( SECONDS - start < 15 )) || fail "$mode: timeout took too long"
  [[ $(snap "$W/dist") == "$BEFORE" ]] || fail "$mode: dist changed"
done
pass "no artifact and a stale artifact time out, dist unchanged"

# --- ISC-4 / ISC-5: bad sources never reach dist
bad() { # bad <label> <mutator...>
  local label=$1; shift
  make_src "$W/src"; (cd "$W/src" && "$@")
  reset_dist; BEFORE=$(snap "$W/dist")
  run "${COMMON[@]}" STUB_MODE=ready "${FETCH[@]}"
  [[ $RC -ne 0 ]] || fail "$label: must fail"
  [[ $(snap "$W/dist") == "$BEFORE" ]] || fail "$label: dist changed"
  pass "$label fails, dist unchanged"
}
bad "sha256 mismatch" bash -c "echo x >> hatamoto-$ED-linux-arm64"
bad "missing platform" rm "hatamoto-$ED-darwin-arm64"
bad "extra file" touch extra.bin
bad "missing SHA256SUMS" rm SHA256SUMS
bad "empty file" bash -c ": > hatamoto-$ED-linux-amd64; sha256sum hatamoto-* > SHA256SUMS"
bad "manifest with another tag" bash -c "sed -i 's/\"tag\":\"[^\"]*\"/\"tag\":\"v9.9.9\"/' edition-build.json"
bad "manifest with another sha" bash -c "sed -i 's/\"sha\":\"[^\"]*\"/\"sha\":\"deadbeefdeadbeef\"/' edition-build.json"
bad "manifest with another nonce" bash -c "sed -i 's/@NONCE@/some-other-dispatch/' edition-build.json"
bad "missing manifest" rm edition-build.json
bad "manifest with an extra key" bash -c "sed -i 's/}\$/,\"x\":\"y\"}/' edition-build.json"
bad "unlisted file in SHA256SUMS" bash -c "sed -i '1d' SHA256SUMS"

make_src "$W/src"
reset_dist; cp "$W/src/hatamoto-$ED-linux-arm64" "$W/dist/"; BEFORE=$(snap "$W/dist")
run "${COMMON[@]}" STUB_MODE=ready "${FETCH[@]}"
[[ $RC -ne 0 ]] && [[ $(snap "$W/dist") == "$BEFORE" ]] || fail "existing target must fail and leave dist unchanged"
pass "an existing target in dist fails, dist unchanged"

# a dangling symlink in dist counts as an existing target
make_src "$W/src"; reset_dist; ln -s /nonexistent/x "$W/dist/hatamoto-$ED-linux-arm64"
run "${COMMON[@]}" STUB_MODE=ready "${FETCH[@]}"
[[ $RC -ne 0 ]] && [[ -L $W/dist/hatamoto-$ED-linux-arm64 && ! -e $W/dist/hatamoto-$ED-linux-arm64 ]] || fail "dangling symlink must fail"
pass "a dangling symlink in dist fails"

# opt-in of only separators fails closed (the workflow if: runs the step for any non-empty value)
for v in " " "," " , "; do
  run RELEASE_EDITIONS="$v" EDITION_REPO=owner/edition-build GH_TOKEN="$TOKEN" "${FETCH[@]}"
  [[ $RC -ne 0 ]] && ! grep -q skipped "$W/out/$OUT_N" || fail "RELEASE_EDITIONS='$v' must fail"
  run RELEASE_EDITIONS="$v" bash "$SCRIPT" require --record "$W/none.json"
  [[ $RC -ne 0 ]] || fail "require with RELEASE_EDITIONS='$v' must fail"
done
pass "an opt-in without any edition name fails closed"

# --- the good case
make_src "$W/src"; reset_dist
run "${COMMON[@]}" STUB_MODE=ready "${FETCH[@]}"
[[ $RC -eq 0 ]] || fail "good fetch failed: $(cat "$W/out/$OUT_N")"
for p in "${PLATS[@]}"; do
  cmp -s "$W/src/hatamoto-$ED-$p" "$W/dist/hatamoto-$ED-$p" || fail "hatamoto-$ED-$p not copied byte-equal"
done
[[ ! -e $W/dist/edition-build.json ]] || fail "the manifest must not be copied into dist"
[[ $(cat "$W/dist/hatamoto-linux-amd64") == keep ]] || fail "community file touched"
pass "a fresh verified artifact is copied byte-equal"

# --- the step summary names the edition run and lists the hashes
make_src "$W/src"; reset_dist
run "${COMMON[@]}" GITHUB_STEP_SUMMARY="$W/summary" STUB_MODE=ready "${FETCH[@]}"
grep -q 'https://example.test/runs/42' "$W/summary" || fail "summary lacks the run URL"
for p in "${PLATS[@]}"; do grep -q "$(sha256sum < "$W/src/hatamoto-$ED-$p" | cut -d' ' -f1)" "$W/summary" || fail "summary lacks the hash of $p"; done
pass "the summary has the run URL and every file hash"

# --- ISC-A-1: the token never appears anywhere
if grep -rq -- "$TOKEN" "$W/out" "$STUB_LOG"; then fail "the token was printed"; fi
if grep -qE '^[[:space:]]*set .*-[a-zA-Z]*x' "$SCRIPT"; then fail "script must not use set -x"; fi
pass "the token is never printed"

# --- ISC-7 / ISC-8: a record built from a local edition dir carries the edition
( cd backend && go build -o "$W/release" ./tools/release )
TOOL="$W/release"
make_dist() { # make_dist <dir>: four community files and the build facts
  local d=$1 p i=0
  rm -rf "$d"; mkdir -p "$d"
  for p in "${PLATS[@]}"; do i=$((i + 1)); head -c $((200 + i)) /dev/urandom > "$d/hatamoto-$p"; done
  echo '{"version":"'$TAG'","commit":"abc","sdk":"0.10.0","schema_head":1}' > "$W/facts.json"
}
record() { "$TOOL" record --version $TAG --kind minor --facts "$W/facts.json" --rollback-floor v0.0.0 \
  --min-from v0.0.0 --notes-url https://example.test/notes --dir "$1" --out "$1/release.json" >/dev/null; }
make_src "$W/src"; make_dist "$W/d1"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" local --tag $TAG --from "$W/src" --dist "$W/d1"
[[ $RC -eq 0 ]] || fail "local copy failed: $(cat "$W/out/$OUT_N")"
record "$W/d1"
[[ $(jq --arg e "$ED" '.editions[$e] | length' "$W/d1/release.json") -eq 4 ]] || fail "record lacks four $ED platforms"
for p in "${PLATS[@]}"; do
  want=$(sha256sum "$W/src/hatamoto-$ED-$p" | cut -d' ' -f1)
  got=$(jq -r --arg e "$ED" --arg p "$p" '.editions[$e][$p].sha256' "$W/d1/release.json")
  [[ $want == "$got" ]] || fail "record sha256 for $p differs from the source file"
done
pass "record --dir on a locally filled dist writes editions.$ED with the source hashes"

run RELEASE_EDITIONS="$ED" bash "$SCRIPT" require --record "$W/d1/release.json"
[[ $RC -eq 0 ]] || fail "require must pass on a complete record"
make_dist "$W/d2"; record "$W/d2"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" require --record "$W/d2/release.json"
[[ $RC -ne 0 ]] || fail "require must fail when the edition is missing"
jq --arg e "$ED" 'del(.editions[$e]["linux-arm64"])' "$W/d1/release.json" > "$W/partial.json"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" require --record "$W/partial.json"
[[ $RC -ne 0 ]] || fail "require must fail on a partial edition"
run RELEASE_EDITIONS= bash "$SCRIPT" require --record "$W/d2/release.json"
[[ $RC -eq 0 ]] || fail "require must pass with no opt-in"
pass "require checks every opted-in edition and passes with no opt-in"

make_src "$W/src"; echo x >> "$W/src/hatamoto-$ED-linux-amd64"; make_dist "$W/d3"; BEFORE=$(snap "$W/d3")
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" local --tag $TAG --from "$W/src" --dist "$W/d3"
[[ $RC -ne 0 ]] && [[ $(snap "$W/d3") == "$BEFORE" ]] || fail "local with a bad checksum must fail and leave dist unchanged"
pass "local mode with a bad SHA256SUMS fails, dist unchanged"

# local: manifest tag must equal --tag, sha must equal --sha when given; relative --from/--dist work
make_src "$W/src"; make_dist "$W/d4"; sed -i 's/@NONCE@/n/' "$W/src/edition-build.json"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" local --tag v9.9.9 --from "$W/src" --dist "$W/d4"
[[ $RC -ne 0 ]] || fail "local: manifest tag differs from --tag must fail"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" local --tag $TAG --sha fedcba9876543210 --from "$W/src" --dist "$W/d4"
[[ $RC -ne 0 ]] || fail "local: manifest sha differs from --sha must fail"
[[ ! -e $W/d4/hatamoto-$ED-linux-arm64 ]] || fail "local: dist touched on a manifest mismatch"
rm "$W/src/edition-build.json"
run RELEASE_EDITIONS="$ED" bash "$SCRIPT" local --tag $TAG --from "$W/src" --dist "$W/d4"
[[ $RC -ne 0 ]] || fail "local: missing manifest must fail"
printf '{"tag":"%s","sha":"%s","nonce":"n"}\n' "$TAG" "$SHA" > "$W/src/edition-build.json"
run RELEASE_EDITIONS="$ED" bash -c "cd '$W' && bash '$SCRIPT' local --tag $TAG --sha $SHA --from src --dist d4"
[[ $RC -eq 0 ]] || fail "local with relative paths failed: $(cat "$W/out/$OUT_N")"
cmp -s "$W/src/hatamoto-$ED-linux-amd64" "$W/d4/hatamoto-$ED-linux-amd64" || fail "relative paths: file not copied"
pass "local checks the manifest and accepts relative --from and --dist"

echo "ALL PASS"
