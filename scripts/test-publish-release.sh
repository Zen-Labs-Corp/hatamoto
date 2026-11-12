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
export DEPLOY_TARGET=box   # a developer's real .deploy-target must not flip the box cases

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
rm -rf "${BOXDIR:?}/$VER"; mkdir -p "$BOXDIR/$VER"
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

# --- cell mode: stage on the host, publish with hmctl site static --path
# hmctl runs the shared fake, with the rsync stub hidden (the fake publishes with the real one)
cp "$ROOT/scripts/fake-hmctl.sh" "$W/bin/fake-hmctl.sh"
printf '#!/usr/bin/env bash\nPATH=${PATH#"%s:"} exec bash "%s" "$@"\n' "$W/stubs" "$W/bin/fake-hmctl.sh" > "$W/stubs/hmctl"
chmod +x "$W/stubs/hmctl"
cat > "$W/stubs/ssh" <<'SSH'
#!/usr/bin/env bash
while [[ ${1:-} == -* ]]; do case $1 in -o) shift 2 ;; *) shift ;; esac; done
shift   # host
echo "ssh $*" >> "$STUB_LOG"
# SSH_FAIL_READ=1: reading the published copy fails; PLANT_PUBLISHED=<file>: a racing publish
# lands <file> (different content) while staging is prepared
[[ ${SSH_FAIL_READ:-} == 1 && "$*" == *sha256sum* ]] && exit 255
if [[ -n ${PLANT_PUBLISHED:-} && "$*" == *"mkdir -p"* ]]; then
  mkdir -p "$DEPLOY_STATIC_ROOT/releases/$PLANT_VER"; echo raced > "$DEPLOY_STATIC_ROOT/releases/$PLANT_VER/$PLANT_PUBLISHED"
fi
sh -c "$*"
SSH
cat > "$W/stubs/rsync" <<'RSYNC'
#!/usr/bin/env bash
echo "rsync $*" >> "$STUB_LOG"
args=("$@"); dest=${args[-1]#*:}; unset 'args[-1]'
for a in "${args[@]}"; do
  [[ "$a" == -* ]] && continue
  [[ -e "$dest/$a" ]] && continue
  cp "$a" "$dest/$a"
done
RSYNC
export FAKE_STATIC="$W/cellstatic" FAKE_BIN_STATE="$W/cellbin" FAKE_MARK="$W/cellmark"
CELLROOT="$W/cellstatic"   # DEPLOY_STATIC_ROOT: the published tree (the fake publishes here)
cellpub() { # cellpub <args...>
  : > "$STUB_LOG"; rm -f "$STUB_LOG.static-n"
  [[ -n "${NO_STATIC_ROOT:-}" ]] || mkdir -p "$CELLROOT"   # the static root exists on a real host
  set +e
  OUT=$(cd "$ROOT" && PATH="$W/stubs:$PATH" RELEASE_TOOL="$TOOL" DEPLOY_TARGET=hmctl:example-site \
    DEPLOY_STAGING="${STAGING_OVERRIDE:-$W/stage}" DEPLOY_STATIC_ROOT="$CELLROOT" "$SCRIPT" "$@" 2>&1)
  RC=$?
  set -e
}

# 10. Cell publish: three uploads into staging, one site static, no --delete.
cellpub $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "cell publish exited $RC"; }
[[ $(grep -c '^rsync' "$STUB_LOG") == 3 ]] || fail "cell: expected 3 rsync calls"
grep '^rsync' "$STUB_LOG" | grep -qE "stubhost:$W/stage/run\.[A-Za-z0-9]+/\$" || fail "cell: rsync does not target a per-run staging dir"
[[ $(grep '^rsync' "$STUB_LOG" | tail -1) == *" release.json stubhost:"* ]] || fail "cell: release.json not last"
[[ $(grep -c '^hmctl site static' "$STUB_LOG") == 2 ]] || fail "cell: expected two site static calls (artifacts and signature, then the record)"
grep -qE "hmctl site static example-site --from $W/stage/run\.[A-Za-z0-9]+ --path releases/$VER\$" "$STUB_LOG" || fail "cell: wrong site static call"
if grep -q -- '--delete' "$STUB_LOG"; then fail "cell: --delete used"; fi
for f in release.json release.json.sig hatamoto-linux-amd64; do
  cmp -s "$W/rel/good/$f" "$CELLROOT/releases/$VER/$f" || fail "cell: $f not published"
done
pass "cell publish stages then site static --path releases/$VER"

# 11. Cell rerun: nothing staged, hmctl not called.
cellpub $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "cell rerun exited $RC"; }
if grep -q '^rsync' "$STUB_LOG"; then fail "cell rerun called rsync"; fi
if grep -q 'site static' "$STUB_LOG"; then fail "cell rerun called site static"; fi
grep -q 'already published' <<<"$OUT" || fail "cell rerun did not say so"
pass "cell rerun uploads and publishes nothing"

# 12. Cell: a differing published file is refused, nothing staged or published.
rm -rf "$CELLROOT" "$W/stage"; mkdir -p "$CELLROOT/releases/$VER"
echo different > "$CELLROOT/releases/$VER/hatamoto-linux-arm64"
cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: differing published file accepted"
if grep -qE '^rsync|site static|mkdir' "$STUB_LOG"; then fail "cell: touched the host despite a differing file"; fi
[[ ! -e "$W/stage" ]] || fail "cell: staging created despite refusal"
[[ $(cat "$CELLROOT/releases/$VER/hatamoto-linux-arm64") == different ]] || fail "cell: published file overwritten"
pass "cell: changed published file refused before any upload"

# 13. Cell dry-run: no rsync, no hmctl, shows both steps.
rm -rf "$CELLROOT" "$W/stage"
cellpub --dry-run $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "cell dry-run exited $RC"; }
if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell dry-run called rsync or hmctl"; fi
grep -q 'DRY: rsync' <<<"$OUT" || fail "cell dry-run did not show the rsync"
grep -q -- "--path releases/$VER" <<<"$OUT" || fail "cell dry-run did not show site static"
if grep -q -- '--delete' <<<"$OUT"; then fail "cell dry-run shows --delete"; fi
[[ ! -e "$W/stage" ]] || fail "cell dry-run created staging"
pass "cell dry-run shows the steps, runs nothing"
# 14. Cell fails closed: an unreadable published copy is not "absent", nothing is staged or published.
rm -rf "$CELLROOT" "$W/stage"; mkdir -p "$CELLROOT"
SSH_FAIL_READ=1 cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: unreadable published copy accepted"
if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell: published despite an unreadable copy"; fi
grep -q 'cannot read the published copy' <<<"$OUT" || fail "cell: no read-error message"
pass "cell: unreadable published copy refuses the publish"

# A missing or unlistable static root must not make every file look absent.
rm -rf "$CELLROOT" "$W/stage"
NO_STATIC_ROOT=1 cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: missing static root accepted"
if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell: published into a missing static root"; fi
grep -q 'static root' <<<"$OUT" || { echo "$OUT"; fail "cell: no static-root message"; }
if [[ $(id -u) != 0 ]]; then
  mkdir -p "$CELLROOT/releases/$VER"; chmod 000 "$CELLROOT"
  cellpub $VER "$W/rel/good"
  chmod 755 "$CELLROOT"
  [[ $RC != 0 ]] || fail "cell: unreadable static root accepted"
  if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell: published into an unreadable static root"; fi
  mkdir -p "$CELLROOT/releases/$VER"; chmod 000 "$CELLROOT/releases/$VER"
  cellpub $VER "$W/rel/good"
  chmod 755 "$CELLROOT/releases/$VER"
  [[ $RC != 0 ]] || fail "cell: unreadable release dir accepted"
  if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell: published over an unreadable release dir"; fi
fi
pass "cell: missing or unreadable static root refuses the publish"

# 15. Cell re-check: a file published by someone else after the first check is not overwritten.
rm -rf "$CELLROOT" "$W/stage"; mkdir -p "$CELLROOT"
PLANT_PUBLISHED=release.json PLANT_VER=$VER DEPLOY_STATIC_ROOT="$CELLROOT" cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: raced publish accepted"
[[ $(grep -c '^hmctl site static' "$STUB_LOG") == 1 ]] || fail "cell: site static ran for the record after it appeared"
[[ $(cat "$CELLROOT/releases/$VER/release.json") == raced ]] || fail "cell: raced file overwritten"
pass "cell: re-check before site static catches a racing publish"
# 16. Staging containment: '..', a path equal to, inside or above the static root, a shared base.
for bad in "$W/stage/../x" "$CELLROOT" "$CELLROOT/sub" "$W" /srv/sites/example-site/static; do
  rm -rf "$W/stage" "$CELLROOT"; mkdir -p "$CELLROOT"
  STAGING_OVERRIDE="$bad" cellpub $VER "$W/rel/good"
  [[ $RC != 0 ]] || fail "cell: DEPLOY_STAGING=$bad accepted"
  if grep -qE '^rsync|site static|rm -rf' "$STUB_LOG"; then fail "cell: host touched with DEPLOY_STAGING=$bad"; fi
done
rm -rf "$W/stage" "$CELLROOT"; mkdir -p "$W/stage" "$CELLROOT"; chmod 755 "$W/stage"
cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: world-readable staging base accepted"
grep -q "not a private directory" <<<"$OUT" || { echo "$OUT"; fail "cell: no private-directory message"; }
if grep -qE '^rsync|site static' "$STUB_LOG"; then fail "cell: staged into a shared base"; fi
rm -rf "$W/stage" "$CELLROOT"
cellpub $VER "$W/rel/good"
[[ $RC == 0 ]] || { echo "$OUT"; fail "cell publish after the containment cases exited $RC"; }
[[ -z "$(ls -A "$W/stage")" ]] || fail "cell: run dir left behind"
pass "cell: staging must be a private dir outside the static root; run dir removed after publishing"
# 17. Cell order: release.json goes live only after the signature. A failing second publish leaves
# the artifacts and the signature published and the record absent.
rm -rf "$W/stage" "$CELLROOT"; mkdir -p "$CELLROOT"
FAKE_STATIC_FAIL_NTH=2 cellpub $VER "$W/rel/good"
[[ $RC != 0 ]] || fail "cell: failed second publish accepted"
[[ -f "$CELLROOT/releases/$VER/release.json.sig" && -f "$CELLROOT/releases/$VER/hatamoto-linux-amd64" ]] || fail "cell: signature/artifacts not published first"
[[ ! -e "$CELLROOT/releases/$VER/release.json" ]] || fail "cell: release.json live before the second publish"
pass "cell: release.json is published after the signature"
echo "publish-release tests ok"
