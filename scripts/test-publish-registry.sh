#!/usr/bin/env bash
# test-publish-registry.sh - tests scripts/publish-registry.sh and
# `make registry-pull-archive` with ssh/rsync stubs on PATH that act on a local
# directory standing in for the host. No real host. Run by `make check`.
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
ROOT=$PWD
SCRIPT="$ROOT/scripts/publish-registry.sh"
bash -n "$SCRIPT"
command -v rsync >/dev/null || { echo "FAIL: rsync is required"; exit 1; }

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }

# Stubs: ssh runs the remote command locally; rsync drops the "host:" prefix and
# delegates to the real rsync. Every call is logged to $LOG.
mkdir -p "$W/stubs"
LOG=$W/calls.log; export LOG
cat >"$W/stubs/ssh" <<'STUB'
#!/usr/bin/env bash
echo "ssh ${*:2}" >>"$LOG"
exec bash -c "${*: -1}"
STUB
cat >"$W/stubs/rsync" <<'STUB'
#!/usr/bin/env bash
echo "rsync $*" >>"$LOG"
args=("${@#fakehost:}"); last=$((${#args[@]} - 1))
/usr/bin/rsync "${args[@]}" || exit $?
if [ "${FAKE_RSYNC_CORRUPT:-}" = 1 ]; then
  find "${args[last]}" -type f -name '*.hmp' -exec sh -c 'echo x >>"$1"' _ {} \;
fi
STUB
chmod +x "$W/stubs/ssh" "$W/stubs/rsync"
export PATH="$W/stubs:$PATH"
export DEPLOY_TARGET=box   # a developer's real .deploy-target must not flip the box cases

# A local registry checkout (a throwaway git repo) and a fake host.
setup() { # setup <name>: sets $repo $box, empties the log
  repo=$W/$1/repo; box=$W/$1/box; : >"$LOG"
  mkdir -p "$repo/registry/packages" "$repo/registry/plugins" "$repo/registry/archive/hello" "$box"
  git -C "$repo" init -q
  echo pkg >"$repo/registry/packages/hello.hmp"; echo page >"$repo/registry/plugins/index.html"
  echo '{}' >"$repo/registry/registry.json"
  echo new >"$repo/registry/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp"
}
publish() { # publish [args]: runs in $repo against $box
  (cd "$repo" && DEPLOY_HOST=fakehost REMOTE_REGISTRY="$box" "$SCRIPT" "$@")
}
rsync_dests() { sed -n 's/^rsync .* fakehost:\(.*\)$/\1/p' "$LOG" | sed "s|^$box/||"; }

# 1. A file on the host with different content refuses before any upload.
setup differs
mkdir -p "$box/archive/hello"; echo OTHER >"$box/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp"
echo newer >"$repo/registry/archive/hello/hello-2.0.0-bbbbbbbbbbbb-k.hmp"
if out=$(publish 2>&1); then fail "differing remote archive accepted"; fi
grep -q "differs from the file on the host" <<<"$out" || fail "differs: wrong error: $out"
! grep -q '^rsync' "$LOG" || fail "differs: uploaded something: $(cat "$LOG")"
! grep -q mkdir "$LOG" || fail "differs: touched the host: $(cat "$LOG")"
[ ! -e "$box/archive/hello/hello-2.0.0-bbbbbbbbbbbb-k.hmp" ] && [ ! -e "$box/registry.json" ] || fail "differs: files appeared on the host"
pass "remote-differs refuses before any upload"
grep -q "make registry-pull-archive first" <<<"$out" || fail "differs: no pull hint: $out"

# 1b. A host archive file missing locally prints the pull hint but does not block the publish.
setup missing
mkdir -p "$box/archive/hello"; echo old >"$box/archive/hello/hello-0.9.0-cccccccccccc-k.hmp"
out=$(publish 2>&1) || fail "missing-local publish failed: $out"
grep -q "run make registry-pull-archive first" <<<"$out" || fail "missing-local: no hint: $out"
grep -q "archive/hello/hello-0.9.0-cccccccccccc-k.hmp is not in" <<<"$out" || fail "missing-local: file not named: $out"
[ -f "$box/archive/hello/hello-0.9.0-cccccccccccc-k.hmp" ] || fail "missing-local: remote file touched"
setup nomissing
out=$(publish 2>&1) || fail "clean publish failed: $out"
! grep -q "registry-pull-archive" <<<"$out" || fail "hint printed for a complete checkout: $out"
pass "pull hint when the host archive has files this checkout lacks"

# 2. --dry-run uploads nothing.
setup dry
out=$(publish --dry-run 2>&1) || fail "dry-run failed: $out"
grep -q '^DRY:' <<<"$out" || fail "dry-run printed no plan: $out"
! grep -q '^rsync' "$LOG" || fail "dry-run ran rsync"
! grep -q mkdir "$LOG" || fail "dry-run ran mkdir on the host"
[ -z "$(find "$box" -type f)" ] || fail "dry-run uploaded: $(find "$box")"
pass "dry-run uploads nothing"

# 3. Upload order archive -> packages -> plugins (when built) -> registry.json, then hashes match.
setup order
publish >"$W/out" 2>&1 || fail "publish failed: $(cat "$W/out")"
[ "$(rsync_dests | tr '\n' ' ')" = "archive/ packages/ plugins/ registry.json " ] || fail "upload order: $(rsync_dests | tr '\n' ' ')"
cmp "$repo/registry/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp" "$box/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp" || fail "archive file not on the host"
[ -s "$box/registry.json" ] && [ -s "$box/packages/hello.hmp" ] && [ -s "$box/plugins/index.html" ] || fail "registry files missing on the host"
# A second run is a no-op for the archive.
publish >"$W/out" 2>&1 || fail "second publish failed: $(cat "$W/out")"
grep -q "archive: nothing new" "$W/out" || fail "second run: $(cat "$W/out")"
pass "upload order and idempotence"

# 3b. Without registry/plugins (a site that shows the directory as a CMS page)
# the publish succeeds and uploads no plugins/ directory.
setup noplugins
rm -rf "$repo/registry/plugins"
publish >"$W/out" 2>&1 || fail "publish without registry/plugins failed: $(cat "$W/out")"
[ "$(rsync_dests | tr '\n' ' ')" = "archive/ packages/ registry.json " ] || fail "no-plugins upload order: $(rsync_dests | tr '\n' ' ')"
[ ! -e "$box/plugins" ] && [ -s "$box/registry.json" ] && [ -s "$box/packages/hello.hmp" ] || fail "no-plugins: wrong files on the host"
pass "publish without registry/plugins"

# 3c. Screenshots (theme directory) go up after packages and before plugins/registry.json.
setup shots
mkdir -p "$repo/registry/screenshots"; echo png >"$repo/registry/screenshots/kumo.png"
publish >"$W/out" 2>&1 || fail "publish with screenshots failed: $(cat "$W/out")"
[ "$(rsync_dests | tr '\n' ' ')" = "archive/ packages/ screenshots/ plugins/ registry.json " ] || fail "screenshots upload order: $(rsync_dests | tr '\n' ' ')"
cmp "$repo/registry/screenshots/kumo.png" "$box/screenshots/kumo.png" || fail "screenshot not on the host"
# A symlink under screenshots/ is refused before anything is contacted.
setup shotlink
mkdir -p "$repo/registry/screenshots"; ln -s /etc/passwd "$repo/registry/screenshots/x.png"
if out=$(publish 2>&1); then fail "symlink in screenshots accepted"; fi
grep -q "symlinks are not allowed" <<<"$out" || fail "shotlink: wrong error: $out"
[ ! -s "$LOG" ] || fail "shotlink: contacted the host: $(cat "$LOG")"
pass "screenshots uploaded in order, symlinks refused (absent case is 3b)"

# 4. A hash mismatch after the archive upload stops before the index goes out.
setup corrupt
if out=$(FAKE_RSYNC_CORRUPT=1 publish 2>&1); then fail "corrupt upload accepted"; fi
grep -q "hash mismatch after upload" <<<"$out" || fail "corrupt: wrong error: $out"
[ "$(rsync_dests | tr '\n' ' ')" = "archive/ " ] || fail "corrupt: kept uploading: $(rsync_dests | tr '\n' ' ')"
[ ! -e "$box/registry.json" ] || fail "corrupt: index uploaded"
pass "post-upload hash check"

# 5. An archive file name with shell metacharacters never reaches the host.
setup unsafe
echo x >"$repo/registry/archive/hello/it's;touch x.hmp"
if out=$(publish 2>&1); then fail "unsafe name accepted"; fi
grep -q "unsafe file name" <<<"$out" || fail "unsafe: wrong error: $out"
[ ! -s "$LOG" ] || fail "unsafe: contacted the host: $(cat "$LOG")"
pass "unsafe archive file names are refused"

# 6. registry-pull-archive: first use (no archive on the host yet) succeeds; later it fetches.
: >"$LOG"; mkdir -p "$W/pull/box"
make -s registry-pull-archive DEPLOY_HOST=fakehost REMOTE_REGISTRY="$W/pull/box" ARCHIVE_DIR="$W/pull/local" >"$W/out" 2>&1 || fail "first pull failed: $(cat "$W/out")"
[ -d "$W/pull/local" ] && [ -z "$(find "$W/pull/local" -type f)" ] || fail "first pull: expected an empty local archive"
mkdir -p "$W/pull/box/archive/hello"; echo a >"$W/pull/box/archive/hello/f.hmp"
make -s registry-pull-archive DEPLOY_HOST=fakehost REMOTE_REGISTRY="$W/pull/box" ARCHIVE_DIR="$W/pull/local" >"$W/out" 2>&1 || fail "pull failed: $(cat "$W/out")"
[ -f "$W/pull/local/hello/f.hmp" ] || fail "pull did not fetch the archive"
# An unreachable host (ssh exit 255) is an error, not an empty archive.
cat >"$W/stubs/ssh" <<'STUB'
#!/usr/bin/env bash
exit 255
STUB
if make -s registry-pull-archive DEPLOY_HOST=fakehost REMOTE_REGISTRY="$W/pull/box" ARCHIVE_DIR="$W/pull/l2" >"$W/out" 2>&1; then fail "unreachable host treated as empty"; fi
pass "registry-pull-archive: first use, fetch and unreachable host"

# 7. Both entry points validate the host the same way.
for h in 'a b' '-oProxyCommand=x' 'a;b'; do
  if DEPLOY_HOST=$h scripts/deploy-host.sh >/dev/null 2>&1; then fail "deploy-host accepted '$h'"; fi
  if make -s registry-pull-archive DEPLOY_HOST="$h" ARCHIVE_DIR="$W/x" >/dev/null 2>&1; then fail "pull accepted '$h'"; fi
done
pass "deploy host validation"

# --- cell mode: stage under DEPLOY_STAGING/registry, publish with hmctl site static --path registry
cat >"$W/stubs/ssh" <<'STUB'
#!/usr/bin/env bash
echo "ssh ${*: -1}" >>"$LOG"
exec bash -c "${*: -1}"
STUB
# hmctl is the shared fake with the rsync stub hidden (the fake publishes with the real one)
cp "$ROOT/scripts/fake-hmctl.sh" "$W/fake-hmctl.sh"
printf '#!/usr/bin/env bash\nPATH=${PATH#"%s:"} exec bash "%s" "$@"\n' "$W/stubs" "$W/fake-hmctl.sh" >"$W/stubs/hmctl"
chmod +x "$W/stubs/hmctl"
export STUB_LOG=$LOG FAKE_STATIC=$W/cellstatic
cell_setup() { # cell_setup <name>: like setup, plus a published static root; sets $stage $static
  setup "$1"; stage=$W/$1/stage; static=$W/$1/static; mkdir -p "$static"
  export FAKE_STATIC=$static
}
cell_publish() { # cell_publish [args]
  (cd "$repo" && DEPLOY_TARGET=hmctl:example-site DEPLOY_HOST=fakehost DEPLOY_STAGING="${STAGING_OVERRIDE:-$stage}" \
    DEPLOY_STATIC_ROOT="$static" "$SCRIPT" "$@")
}
cell_dests() { sed -n 's/^rsync .* fakehost:\(.*\)$/\1/p' "$LOG" | sed -E "s|^$stage/run\.[A-Za-z0-9]+/||"; }

# 8. Cell publish: same upload order into staging, one site static, nothing deleted.
cell_setup cell
mkdir -p "$static/registry/archive/hello" "$static/other"; echo keep >"$static/other/f"
echo old >"$static/registry/archive/hello/hello-0.9.0-cccccccccccc-k.hmp"
cell_publish >"$W/out" 2>&1 || fail "cell publish failed: $(cat "$W/out")"
[ "$(cell_dests | tr '\n' ' ')" = "archive/ packages/ plugins/ registry.json " ] || fail "cell upload order: $(cell_dests | tr '\n' ' ')"
[ "$(grep -c '^hmctl site static' "$LOG")" = 1 ] || fail "cell: expected one site static: $(cat "$LOG")"
grep -qE "^hmctl site static example-site --from $stage/run\.[A-Za-z0-9]+ --path registry\$" "$LOG" || fail "cell: wrong site static call: $(cat "$LOG")"
! grep -q -- '--delete' "$LOG" || fail "cell: --delete used"
cmp "$repo/registry/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp" "$static/registry/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp" || fail "cell: archive not published"
[ -s "$static/registry/registry.json" ] && [ -s "$static/registry/packages/hello.hmp" ] || fail "cell: registry not published"
[ -f "$static/other/f" ] && [ -f "$static/registry/archive/hello/hello-0.9.0-cccccccccccc-k.hmp" ] || fail "cell: unrelated published files touched"
grep -q "run make registry-pull-archive first" "$W/out" || fail "cell: no pull hint for a published-only file"
pass "cell publish: staged in order, one site static --path registry"

# 9. Cell: a published archive file that differs refuses before mkdir/rsync/hmctl.
cell_setup celldiff
mkdir -p "$static/registry/archive/hello"; echo OTHER >"$static/registry/archive/hello/hello-1.0.0-aaaaaaaaaaaa-k.hmp"
if out=$(cell_publish 2>&1); then fail "cell: differing published archive accepted"; fi
grep -q "differs from the file on the host" <<<"$out" || fail "celldiff: wrong error: $out"
! grep -qE '^rsync|mkdir|^hmctl' "$LOG" || fail "celldiff: touched the host: $(cat "$LOG")"
[ ! -e "$stage" ] || fail "celldiff: staging created"
pass "cell: differing published archive refuses before any upload"

# 9b. Cell fails closed when the static root is unreadable.
cell_setup cellnoroot
rmdir "$static"
if out=$(cell_publish 2>&1); then fail "cell: missing static root accepted"; fi
grep -q "cannot read the published archive" <<<"$out" || fail "cellnoroot: wrong error: $out"
! grep -qE '^rsync|mkdir|^hmctl' "$LOG" || fail "cellnoroot: touched the host: $(cat "$LOG")"
pass "cell: unreadable static root fails closed"

# 10. Cell dry-run: shows the plan, runs no rsync, no mkdir, no hmctl.
cell_setup celldry
out=$(cell_publish --dry-run 2>&1) || fail "cell dry-run failed: $out"
grep -q -- '--path registry' <<<"$out" || fail "cell dry-run: no site static line: $out"
! grep -qE '^rsync|^hmctl|mkdir' "$LOG" || fail "cell dry-run touched the host: $(cat "$LOG")"
[ ! -e "$stage" ] || fail "cell dry-run created staging"
pass "cell dry-run uploads and publishes nothing"

# 11. Cell: a corrupt upload dies before hmctl publishes anything.
cell_setup cellcorrupt
if out=$(FAKE_RSYNC_CORRUPT=1 cell_publish 2>&1); then fail "cell: corrupt upload accepted"; fi
grep -q "hash mismatch after upload" <<<"$out" || fail "cellcorrupt: wrong error: $out"
! grep -q '^hmctl' "$LOG" || fail "cellcorrupt: hmctl ran: $(cat "$LOG")"
[ ! -e "$static/registry/registry.json" ] || fail "cellcorrupt: index published"
pass "cell: post-upload hash check dies before hmctl"

# 11b. Cell: staging must be private and outside the static root; the run dir is removed afterwards.
cell_setup cellstage
for bad in "$stage/../x" "$static" "$static/sub" "$W" /srv/sites/example-site/static; do
  : >"$LOG"
  if out=$(STAGING_OVERRIDE=$bad cell_publish 2>&1); then fail "cell: DEPLOY_STAGING=$bad accepted"; fi
  ! grep -qE '^rsync|^hmctl|rm -rf|mkdir' "$LOG" || fail "cell: host touched with DEPLOY_STAGING=$bad: $(cat "$LOG")"
done
mkdir -p "$stage"; chmod 755 "$stage"; : >"$LOG"
if out=$(cell_publish 2>&1); then fail "cell: shared staging base accepted"; fi
grep -q "not a private directory" <<<"$out" || fail "cellstage: wrong error: $out"
! grep -qE '^rsync|^hmctl' "$LOG" || fail "cellstage: staged into a shared base"
rm -rf "$stage"; cell_publish >"$W/out" 2>&1 || fail "cellstage: publish failed: $(cat "$W/out")"
[ -z "$(ls -A "$stage")" ] || fail "cellstage: run dir left behind"
pass "cell: private staging outside the static root, cleaned up"

# --- Makefile in cell mode
# 12. deploy and its steps refuse with a hint and never touch ssh/scp.
for t in deploy upload-api install-unit check-unit restart-api; do
  : >"$LOG"
  if out=$(DEPLOY_TARGET=hmctl:example-site make -s "$t" DEPLOY_HOST=fakehost 2>&1); then fail "make $t succeeded in cell mode"; fi
  grep -q "scripts/ship.sh" <<<"$out" || fail "make $t: no hint: $out"
  [ ! -s "$LOG" ] || fail "make $t called the host: $(cat "$LOG")"
done
if out=$(DEPLOY_TARGET=bogus make -s deploy DEPLOY_HOST=fakehost 2>&1); then fail "make deploy accepted a malformed target"; fi
[ ! -s "$LOG" ] || fail "malformed target reached the host"
pass "cell mode: make deploy refuses with a hint"

# 13. registry-pull-archive in cell mode reads the published archive under the static root.
cat >"$W/stubs/ssh" <<'STUB'
#!/usr/bin/env bash
echo "ssh ${*:2}" >>"$LOG"
exec bash -c "${*: -1}"
STUB
: >"$LOG"; mkdir -p "$W/cellpull/static/registry/archive/hello"; echo a >"$W/cellpull/static/registry/archive/hello/f.hmp"
DEPLOY_TARGET=hmctl:example-site DEPLOY_STATIC_ROOT="$W/cellpull/static" make -s registry-pull-archive DEPLOY_HOST=fakehost ARCHIVE_DIR="$W/cellpull/local" >"$W/out" 2>&1 || fail "cell pull failed: $(cat "$W/out")"
[ -f "$W/cellpull/local/hello/f.hmp" ] || fail "cell pull did not fetch the published archive"
grep -q "^rsync .* fakehost:$W/cellpull/static/registry/archive/ " "$LOG" || fail "cell pull: rsync not from the published path: $(cat "$LOG")"
# No published archive yet: starts empty (ssh test -d exits 1).
: >"$LOG"; mkdir -p "$W/cellpull2/static"
DEPLOY_TARGET=hmctl:example-site DEPLOY_STATIC_ROOT="$W/cellpull2/static" make -s registry-pull-archive DEPLOY_HOST=fakehost ARCHIVE_DIR="$W/cellpull2/local" >"$W/out" 2>&1 || fail "cell pull (empty) failed: $(cat "$W/out")"
grep -q "starting empty" "$W/out" || fail "cell pull (empty): $(cat "$W/out")"
pass "cell mode: registry-pull-archive reads the published archive"
echo "ALL PASS"
