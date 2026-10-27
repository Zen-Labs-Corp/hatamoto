#!/usr/bin/env bash
# test-publish-release.sh - tests scripts/publish-release.sh with throwaway
# keys, a verifier built with the test-key seam, and ssh/rsync stubs that
# operate on a local directory standing in for the server. No real host, no
# real key. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
ROOT=$PWD
SCRIPT="$ROOT/scripts/publish-release.sh"
bash -n "$SCRIPT"
command -v rsync >/dev/null || { echo "FAIL: rsync is required"; exit 1; }

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
mkdir -p "$W"/{bin,stubs,keys,trust,rel,box}
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

# --- tooling: test-key verifier, throwaway keys, a fake release
( cd backend && go build -tags hatamoto_testkeys -o "$W/bin/release" ./tools/release )
TOOL="$W/bin/release"
( unset HATAMOTO_TEST_TRUST_DIR
  "$TOOL" keygen --kind release --out "$W/keys" >/dev/null
  "$TOOL" keygen --kind manifest --out "$W/keys" >/dev/null
  mkdir -p "$W/keys2"; "$TOOL" keygen --kind release --out "$W/keys2" >/dev/null )
cp "$W/keys/release.pub" "$W/trust/release-test.pub"
cp "$W/keys/manifest.pub" "$W/trust/manifest.pub"
export HATAMOTO_TEST_TRUST_DIR="$W/trust"

VER=v0.25.0
make_release() { # make_release <dir> <signing key dir>
  local d=$1 k=$2 i=0 p
  mkdir -p "$d"
  for p in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    i=$((i + 1)); head -c $((200 + i)) /dev/urandom > "$d/hatamoto-$p"
  done
  echo '{"version":"'$VER'","commit":"abc","sdk":"0.10.0","schema_head":1}' > "$W/facts.json"
  "$TOOL" record --version $VER --kind minor --facts "$W/facts.json" --rollback-floor v0.0.0 \
    --min-from v0.0.0 --notes-url https://example.test/notes --dir "$d" --out "$d/release.json" >/dev/null
  "$TOOL" sign-record "$d/release.json" --key "$k/release.key" --out "$d/release.json.sig" >/dev/null
}
make_release "$W/rel/good" "$W/keys"

# --- stubs: ssh runs the command locally; rsync copies and logs
cat > "$W/stubs/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$STUB_LOG"
sh -c "$2"
SSH
cat > "$W/stubs/rsync" <<'RSYNC'
#!/usr/bin/env bash
echo "rsync $*" >> "$STUB_LOG"
args=("$@"); dest=${args[-1]#*:}; unset 'args[-1]'
for a in "${args[@]}"; do
  [[ "$a" == -* ]] && continue
  [[ -e "$dest/$a" ]] && continue   # --ignore-existing
  cp "$a" "$dest/$a"
done
RSYNC
chmod +x "$W/stubs/ssh" "$W/stubs/rsync"
export STUB_LOG="$W/log"

BOXDIR="$W/box/releases"
pub() { # pub <args...>: run the script against the stub box; sets OUT and RC
  : > "$STUB_LOG"
  set +e
  OUT=$(cd "$ROOT" && PATH="$W/stubs:$PATH" RELEASE_TOOL="$TOOL" REMOTE_RELEASES="$BOXDIR" "$SCRIPT" "$@" 2>&1)
  RC=$?
  set -e
}
export DEPLOY_HOST=stubhost

# 1. Good publish: files appear, release.json is uploaded last.
pub $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "good publish exited $RC"; }
for f in release.json release.json.sig hatamoto-linux-amd64 hatamoto-darwin-arm64; do
  cmp -s "$W/rel/good/$f" "$BOXDIR/$VER/$f" || fail "$f missing or different on the box"
done
grep -q "published $VER" <<<"$OUT" || fail "no success line"
if grep '^rsync' "$STUB_LOG" | grep -qv -- ' --no-links '; then fail "an rsync call lacks --no-links"; fi
last=$(grep '^rsync' "$STUB_LOG" | tail -1)
[[ "$last" == *" release.json stubhost:"* ]] || fail "release.json was not uploaded last: $last"
[[ $(grep '^rsync' "$STUB_LOG" | sed -n 2p) == *" release.json.sig stubhost:"* ]] || fail "signature was not the second upload"
[[ $(grep -c '^rsync' "$STUB_LOG") == 3 ]] || fail "expected 3 rsync calls (artifacts, sig, record)"
pass "good publish; artifacts, then signature, then release.json last"

# 2. Re-run is a no-op.
pub $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "rerun exited $RC"; }
if grep -q '^rsync' "$STUB_LOG"; then fail "rerun called rsync"; fi
grep -q 'already published' <<<"$OUT" || fail "rerun did not say so"
pass "rerun uploads nothing"

# 3. A differing remote file: refused before any upload, nothing new on the box.
rm -rf "$BOXDIR/$VER"; mkdir -p "$BOXDIR/$VER"
echo different > "$BOXDIR/$VER/hatamoto-linux-arm64"
pub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "differing remote file accepted"
if grep -q '^rsync' "$STUB_LOG"; then fail "rsync ran despite a differing file"; fi
[[ $(ls "$BOXDIR/$VER" | wc -l) == 1 ]] || fail "files were added despite refusal"
[[ $(cat "$BOXDIR/$VER/hatamoto-linux-arm64") == different ]] || fail "remote file was overwritten"
pass "changed file refused before any upload"
rm -rf "$BOXDIR"

# 4. Tampered artifact and 5. bad signature: the verifier stops it, ssh never runs.
cp -r "$W/rel/good" "$W/rel/tamper"; echo x >> "$W/rel/tamper/hatamoto-darwin-amd64"
pub $VER "$W/rel/tamper"
[[ $RC != 0 ]] || fail "tampered artifact accepted"
[[ ! -s "$STUB_LOG" ]] || fail "server was contacted for a tampered release"
pass "tampered artifact refused, server untouched"
mkdir -p "$W/trust2"; cp "$W/keys2/release.pub" "$W/trust2/release-test.pub"; cp "$W/keys/manifest.pub" "$W/trust2/manifest.pub"
HATAMOTO_TEST_TRUST_DIR="$W/trust2" make_release "$W/rel/badsig" "$W/keys2"
pub $VER "$W/rel/badsig"
[[ $RC != 0 ]] || fail "release signed by an untrusted key accepted"
[[ ! -s "$STUB_LOG" ]] || fail "server was contacted for a badly signed release"
pass "bad signature refused, server untouched"

# 6. Extra file (a key) in DIR.
cp -r "$W/rel/good" "$W/rel/extra"; echo secret > "$W/rel/extra/x.key"
pub $VER "$W/rel/extra"
[[ $RC != 0 ]] || fail "stray x.key accepted"
[[ ! -s "$STUB_LOG" ]] || fail "server contacted despite a stray file"
pass "directory with a stray key refused"

# 7. Host resolution: unset -> error; env ok; .deploy-host ok.
unset DEPLOY_HOST
HOME_REPO=$(mktemp -d -p "$W"); git -C "$HOME_REPO" init -q
mkdir -p "$HOME_REPO/scripts" "$HOME_REPO/backend"; cp "$SCRIPT" "$HOME_REPO/scripts/"
runin() { : > "$STUB_LOG"; set +e; OUT=$(cd "$HOME_REPO" && PATH="$W/stubs:$PATH" RELEASE_TOOL="$TOOL" REMOTE_RELEASES="$BOXDIR" scripts/publish-release.sh "$@" 2>&1); RC=$?; set -e; }
runin $VER "$W/rel/good"
[[ $RC != 0 ]] && grep -q 'DEPLOY_HOST' <<<"$OUT" || { echo "$OUT"; fail "missing host not reported"; }
echo stubhost > "$HOME_REPO/.deploy-host"
runin $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail ".deploy-host not used"; }
rm -rf "$BOXDIR"
runin --dry-run $VER "$W/rel/good"
export DEPLOY_HOST=stubhost
pass "host from .deploy-host; missing host is an error"

# 8. Wrong version arguments.
rm -rf "$BOXDIR"
pub v0.25.1 "$W/rel/good"; [[ $RC != 0 ]] || fail "record for another version accepted"
pub 0.25.0 "$W/rel/good"; [[ $RC != 0 ]] || fail "version without v accepted"
pub "v0.25.0/../x" "$W/rel/good"; [[ $RC != 0 ]] || fail "path in version accepted"
[[ ! -e "$BOXDIR" ]] || fail "box touched by a refused version"
pass "wrong versions refused"

# 9. --dry-run uploads nothing.
pub --dry-run $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "dry-run exited $RC"; }
if grep -q '^rsync' "$STUB_LOG"; then fail "dry-run called rsync"; fi
[[ ! -e "$BOXDIR/$VER" ]] || fail "dry-run created the release directory"
grep -q 'DRY: rsync' <<<"$OUT" || fail "dry-run did not show the rsync"
pass "dry-run uploads nothing"
echo "publish-release tests ok"
