#!/usr/bin/env bash
# test-ship-cell.sh - real (non-dry) `scripts/ship.sh` runs in cell mode against a
# scratch repo, a fake ssh and scripts/fake-hmctl.sh: call order, backup id,
# deployed-sha mark, preflight abort. Box mode is covered by test-ship-dryrun.sh.
set -Eeuo pipefail
top=$(git rev-parse --show-toplevel)
cd "$top"
bash -n scripts/ship.sh
fail() { echo "FAIL: $*" >&2; [[ -z "${LOG:-}" || ! -f "$LOG" ]] || sed 's/^/  log: /' "$LOG" >&2; [[ -z "${OUT:-}" ]] || sed 's/^/  out: /' <<<"$OUT" >&2; exit 1; }
pass() { echo "ok: $*"; }
slug_of() { local u=${1%/}; u=${u%.git}; u=${u//:/\/}; awk -F/ 'NF >= 2 {print $(NF-1) "/" $NF}' <<< "$u"; }

W=$(mktemp -d); trap 'rm -rf "$W"' EXIT
git init -q --bare "$W/origin.git"
git init -q -b main "$W/main"
(
  cd "$W/main"
  git config user.email t@example.com; git config user.name t
  mkdir scripts
  cp "$top/scripts/ship.sh" scripts/ship.sh
  printf '#!/bin/sh\necho v0\n' > scripts/version.sh
  printf '#!/bin/sh\n' > scripts/ship-db-restore.sh
  printf 'build-api:\n\t@mkdir -p backend/bin && echo new-binary > backend/bin/hatamoto\n' > Makefile
  git add -A && git commit -qm base
  git remote add origin "$W/origin.git"
  git push -q origin main
  # a second commit that touches backend/, so a known previous DEPLOYED_SHA classifies as a deploy
  mkdir backend && echo change > backend/x
  git add -A && git commit -qm change
  git push -q origin main
)
DEPLOY=$(git -C "$W/main" rev-parse HEAD)
OLD=$(git -C "$W/main" rev-parse HEAD~1)
ORIGIN_SLUG=$(slug_of "$W/origin.git")

# --- stubs: gh (PR already merged), ssh (drops flags and host, runs the string), scp, curl, systemctl, journalctl, sleep
S=$W/stubs; mkdir -p "$S"
cat > "$S/gh" <<GH
#!/usr/bin/env bash
field=""; prev=""
for a in "\$@"; do [[ "\$prev" == --json ]] && field=\$a; prev=\$a; done
case "\$field" in
  state) echo MERGED ;; mergeable) echo MERGEABLE ;; mergeStateStatus) echo CLEAN ;;
  headRefName) echo feat/x ;; mergeCommit) echo $DEPLOY ;; nameWithOwner) echo "$ORIGIN_SLUG" ;; *) echo main ;;
esac
GH
# fake ssh: skip `-o X` pairs and -T, drop the host, `sh -c` the remaining single string
cat > "$S/ssh" <<'SSH'
#!/usr/bin/env bash
while [[ ${1:-} == -* ]]; do case $1 in -o) shift 2 ;; *) shift ;; esac; done
shift   # host
echo "ssh $*" >>"$STUB_LOG"
cmd="$*"
exec sh -c "${cmd//\/srv\/sites\//$SRV_ROOT/sites/}"
SSH
cat > "$S/scp" <<'SCP'
#!/usr/bin/env bash
while [[ ${1:-} == -* ]]; do case $1 in -o) shift 2 ;; *) shift ;; esac; done
echo "scp $*" >>"$STUB_LOG"
cp "$1" "${2#*:}"
SCP
cat > "$S/curl" <<'CURL'
#!/usr/bin/env bash
# CURL_FAIL_NEW=1: the site is unhealthy while the new binary is installed
if [[ ${CURL_FAIL_NEW:-} == 1 ]] && grep -q new-binary "$FAKE_BIN_STATE" 2>/dev/null; then exit 22; fi
for a in "$@"; do
  [[ $a == -w ]] && { echo 200; exit 0; }
  [[ $a == */health ]] && { echo ok; exit 0; }
done
exit 0
CURL
printf '#!/bin/sh\nexit 0\n' > "$S/systemctl"
printf '#!/bin/sh\necho "started"\n' > "$S/journalctl"
printf '#!/bin/sh\nexit 0\n' > "$S/sleep"
cp "$top/scripts/fake-hmctl.sh" "$S/hmctl"
chmod +x "$S"/*

LOG=$W/calls.log; OUT=""
ship() { # ship [ENV=VAL...]: real run in the scratch clone; sets OUT and RC
  # OLD_MARK=1: the site reports $OLD as deployed-sha; PRE_OVERRIDE=1: it runs an override binary
  : >"$LOG"; rm -f "$W/mark" "$W/bin-state" "$LOG.goose" "$LOG.stdin" "$LOG.binfail"; rm -rf "$W/stage" "$W/srv"
  if [[ -n ${STAGE_MODE:-} ]]; then mkdir -p "$W/stage"; chmod "$STAGE_MODE" "$W/stage"; fi   # a pre-existing staging base
  if [[ ${OLD_MARK:-} == 1 ]]; then echo "$OLD" >"$W/mark"; fi
  if [[ ${PRE_OVERRIDE:-} == 1 ]]; then
    mkdir -p "$W/srv/sites/example-site/bin"; echo old-binary >"$W/srv/sites/example-site/bin/hatamoto"; cp "$W/srv/sites/example-site/bin/hatamoto" "$W/bin-state"
  fi
  set +e
  OUT=$(cd "$W/main" && env PATH="$S:$PATH" STUB_LOG="$LOG" FAKE_MARK="$W/mark" FAKE_BIN_STATE="$W/bin-state" SRV_ROOT="$W/srv" \
    CURL_FAIL_NEW="${CURL_FAIL_NEW:-}" SHIP_EXPECTED_ORIGIN="$ORIGIN_SLUG" DEPLOY_HOST=stubhost DEPLOY_TARGET=hmctl:example-site \
    DEPLOY_STAGING="$W/stage" SHIP_REEXEC=1 SHIP_MERGEABLE_WAIT=0 "$@" bash scripts/ship.sh 1 2>&1)
  RC=$?
  set -e
}
line() { grep -nF -- "$1" "$LOG" | head -1 | cut -d: -f1; }

# 1. A healthy ship: status < backup < binary < migrate < mark, the backup id is captured.
ship
[[ $RC == 0 ]] || fail "healthy cell ship exited $RC"
last=0
for pat in "hmctl site status example-site" "hmctl site backup example-site" "hmctl site binary example-site --from $W/stage/run." \
           "hmctl site run example-site -- migrate" "hmctl site mark example-site deployed-sha $DEPLOY"; do
  n=$(line "$pat"); [[ -n "$n" ]] || fail "call missing: $pat"
  (( n > last )) || fail "call out of order: $pat"
  last=$n
done
if grep -qE 'site static|sudo|pg_dump' "$LOG"; then fail "box or static commands in a cell ship"; fi
grep -q 'backup id: 20260101T000000Z-manual' <<<"$OUT" || fail "backup id not captured"
[[ "$(cat "$W/mark")" == "$DEPLOY" ]] || fail "deployed-sha mark not set to the deploy sha"
[[ "$(cat "$W/bin-state")" == new-binary ]] || fail "binary handed to hmctl is not the built one"
grep -qE "site binary example-site --from $W/stage/run\\.[A-Za-z0-9]+/hatamoto-$DEPLOY\$" "$LOG" || fail "binary not staged in a per-run dir"
[[ -z "$(ls -A "$W/stage" 2>/dev/null)" ]] || fail "run dir left behind after a healthy ship"
grep -q 'SHIP-H OK' <<<"$OUT" || fail "no success line"
grep -q 'hatamoto@example-site.service' "$LOG" || fail "health did not check the site unit"
pass "healthy cell ship: call order, backup id, mark"

# 2. A site that is not healthy stops the preflight before the backup.
ship FAKE_HEALTH_FAIL=1
[[ $RC != 0 ]] || fail "unhealthy site accepted"
grep -q 'preflight' <<<"$OUT" || fail "no preflight message"
if grep -qE 'site (backup|binary|run)' "$LOG"; then fail "mutating call after a failed preflight"; fi
pass "unhealthy status aborts before the backup"

# 3. Backup output without an id aborts before the binary is touched.
cat > "$S/hmctl-noid" <<'F'
#!/usr/bin/env bash
[[ $2 == backup ]] && { echo "ok  backup  done"; echo "hmctl $*" >>"$STUB_LOG"; exit 0; }
exec "$(dirname "$0")/hmctl-real" "$@"
F
mv "$S/hmctl" "$S/hmctl-real"; mv "$S/hmctl-noid" "$S/hmctl"; chmod +x "$S/hmctl"
ship
[[ $RC != 0 ]] || fail "backup without an id accepted"
grep -q 'no backup id' <<<"$OUT" || fail "no backup-id message"
if grep -q 'site binary' "$LOG"; then fail "binary uploaded after a failed backup"; fi
pass "backup without an id aborts before the binary"
mv "$S/hmctl-real" "$S/hmctl"

# 4. psql status lines: every query starts with \set QUIET on (the fake prints psql's \pset banners
# otherwise), so a healthy ship still reads the fingerprint; an unreadable one aborts before the backup.
ship
[[ $RC == 0 ]] || fail "ship with psql banners exited $RC (QUIET not set?)"
[[ -s "$LOG.stdin" ]] || fail "no psql query recorded"
if grep -q '^\\set QUIET on$' "$LOG.stdin"; then :; else fail "psql input lacks \\set QUIET on"; fi
ship FAKE_GOOSE=unreadable
[[ $RC == 1 ]] || fail "unreadable fingerprint exited $RC, want 1"
grep -q 'applied-migrations fingerprint' <<<"$OUT" || fail "no fingerprint message"
if grep -qE 'site (backup|binary|run)' "$LOG"; then fail "mutating call although the fingerprint is unreadable"; fi
pass "psql banners are silenced; unreadable fingerprint aborts the preflight"

# --- rollback: the new binary is unhealthy
# (a) goose unchanged, the site ran an override: a pre-ship copy of it is re-applied; exit 2, no DB message.
OLD_MARK=1 PRE_OVERRIDE=1 CURL_FAIL_NEW=1 ship
[[ $RC == 2 ]] || fail "rollback (a) exited $RC, want 2"
grep -qE "hmctl site binary example-site --from $W/stage/run\.[A-Za-z0-9]+/hatamoto-previous$" "$LOG" || fail "(a) old binary not re-applied"
if grep -q -- '--clear' "$LOG"; then fail "(a) --clear would swap in the channel release"; fi
[[ "$(cat "$W/bin-state")" == old-binary ]] || fail "(a) the site is not on the old binary"
if grep -q 'DB changed by migration' <<<"$OUT"; then fail "(a) DB message although goose is unchanged"; fi
if grep -qE 'mark example-site deployed-sha [0-9a-f]{40}' "$LOG"; then fail "(a) mark written for a failed ship"; fi
pass "rollback re-applies the previous binary, exit 2"

# (b) no override before the ship: the override is cleared.
OLD_MARK=1 CURL_FAIL_NEW=1 ship
[[ $RC == 2 ]] || fail "rollback (b) exited $RC, want 2"
grep -q 'hmctl site binary example-site --clear' "$LOG" || fail "(b) --clear not used"
[[ ! -e "$W/bin-state" ]] || fail "(b) override still installed"
pass "rollback without the old file clears the override"

# (c) the migration bumped goose: binary rolled back, DB left, manual-restore message with the backup id.
OLD_MARK=1 PRE_OVERRIDE=1 CURL_FAIL_NEW=1 ship FAKE_MIGRATE_BUMPS=1
[[ $RC == 2 ]] || fail "rollback (c) exited $RC, want 2"
grep -q 'DB NOT restored' <<<"$OUT" || fail "(c) no DB message"
grep -q '20260101T000000Z-manual' <<<"$OUT" || fail "(c) message lacks the backup id"
grep -qE "hmctl site binary example-site --from $W/stage/run\.[A-Za-z0-9]+/hatamoto-previous$" "$LOG" || fail "(c) binary not rolled back"
if grep -qE 'restore|pg_|restart|suspend' "$LOG"; then fail "(c) a restore/restart command was run"; fi
pass "changed goose version: binary rolled back, manual DB restore message, exit 2"
# (d) `site binary` itself fails after the start-up migration changed the schema: the fingerprint is
# still compared, so the manual-restore message appears.
OLD_MARK=1 PRE_OVERRIDE=1 ship FAKE_BINARY_FAIL=1 FAKE_MIGRATE_BUMPS=1
[[ $RC == 2 ]] || fail "rollback (d) exited $RC, want 2"
grep -q 'DB NOT restored' <<<"$OUT" || fail "(d) no DB message although the binary step migrated"
if grep -q 'site run example-site -- migrate' "$LOG"; then fail "(d) migrate ran after the failed binary step"; fi
pass "failed binary step after a start-up migration: DB flagged for manual restore"
# (e) the binary state cannot be read: preflight abort, nothing mutated (so --clear is never a guess).
PRE_OVERRIDE=1 ship FAKE_BINARY_SHOW=garbage
[[ $RC == 1 ]] || fail "(e) unreadable binary state exited $RC, want 1"
if grep -qE 'site (run|binary [^ ]+ --)' "$LOG"; then fail "(e) mutating call although the binary state is unknown"; fi
pass "unknown binary state aborts the preflight"
# 5. Staging containment: a base that others can write to, '..', or a path inside /srv/sites is refused
# before anything is changed.
STAGE_MODE=755 ship
[[ $RC == 1 ]] || fail "shared staging base exited $RC, want 1"
grep -q 'not a private directory' <<<"$OUT" || fail "no private-directory message"
if grep -qE 'scp|site (binary|run)' "$LOG"; then fail "upload after a refused staging base"; fi
for bad in "$W/stage/../x" /srv/sites/example-site/static /srv; do
  ship DEPLOY_STAGING="$bad"
  [[ $RC == 1 ]] || fail "DEPLOY_STAGING=$bad exited $RC, want 1"
  grep -q 'bad DEPLOY_STAGING' <<<"$OUT" || fail "DEPLOY_STAGING=$bad not refused up front"
done
pass "staging: private base required, '..' and /srv/sites refused"
echo "ALL OK"
