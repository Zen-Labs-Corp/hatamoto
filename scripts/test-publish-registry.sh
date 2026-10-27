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
echo "ALL PASS"
