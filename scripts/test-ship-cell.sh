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
exec sh -c "$*"
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
  # OLD_MARK=1: the site reports $OLD as deployed-sha; OLD_FILE=1: its binary is still staged
  : >"$LOG"; rm -f "$W/mark" "$W/bin-state" "$LOG.goose" "$LOG.stdin"; rm -rf "$W/stage"
  if [[ ${OLD_MARK:-} == 1 ]]; then echo "$OLD" >"$W/mark"; fi
  if [[ ${OLD_FILE:-} == 1 ]]; then mkdir -p "$W/stage"; echo old-binary >"$W/stage/hatamoto-$OLD"; fi
  set +e
  OUT=$(cd "$W/main" && env PATH="$S:$PATH" STUB_LOG="$LOG" FAKE_MARK="$W/mark" FAKE_BIN_STATE="$W/bin-state" \
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
for pat in "hmctl site status example-site" "hmctl site backup example-site" "hmctl site binary example-site --from $W/stage/hatamoto-$DEPLOY" \
           "hmctl site run example-site -- migrate" "hmctl site mark example-site deployed-sha $DEPLOY"; do
  n=$(line "$pat"); [[ -n "$n" ]] || fail "call missing: $pat"
  (( n > last )) || fail "call out of order: $pat"
  last=$n
done
if grep -qE 'site static|sudo|pg_dump' "$LOG"; then fail "box or static commands in a cell ship"; fi
grep -q 'backup id: 20260101T000000Z-manual' <<<"$OUT" || fail "backup id not captured"
[[ "$(cat "$W/mark")" == "$DEPLOY" ]] || fail "deployed-sha mark not set to the deploy sha"
cmp -s "$W/bin-state" "$W/stage/hatamoto-$DEPLOY" || fail "binary handed to hmctl differs from the staged one"
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
# (a) goose unchanged, old binary still staged: it is re-applied; exit 2, no DB message.
OLD_MARK=1 OLD_FILE=1 CURL_FAIL_NEW=1 ship
[[ $RC == 2 ]] || fail "rollback (a) exited $RC, want 2"
grep -q "hmctl site binary example-site --from $W/stage/hatamoto-$OLD" "$LOG" || fail "(a) old binary not re-applied"
[[ "$(cat "$W/bin-state")" == old-binary ]] || fail "(a) the site is not on the old binary"
if grep -q 'DB changed by migration' <<<"$OUT"; then fail "(a) DB message although goose is unchanged"; fi
if grep -qE 'mark example-site deployed-sha [0-9a-f]{40}' "$LOG"; then fail "(a) mark written for a failed ship"; fi
pass "rollback re-applies the previous binary, exit 2"

# (b) old binary file gone: the override is cleared.
OLD_MARK=1 CURL_FAIL_NEW=1 ship
[[ $RC == 2 ]] || fail "rollback (b) exited $RC, want 2"
grep -q 'hmctl site binary example-site --clear' "$LOG" || fail "(b) --clear not used"
[[ ! -e "$W/bin-state" ]] || fail "(b) override still installed"
pass "rollback without the old file clears the override"

# (c) the migration bumped goose: binary rolled back, DB left, manual-restore message with the backup id.
OLD_MARK=1 OLD_FILE=1 CURL_FAIL_NEW=1 ship FAKE_MIGRATE_BUMPS=1
[[ $RC == 2 ]] || fail "rollback (c) exited $RC, want 2"
grep -q 'DB NOT restored' <<<"$OUT" || fail "(c) no DB message"
grep -q '20260101T000000Z-manual' <<<"$OUT" || fail "(c) message lacks the backup id"
grep -q "hmctl site binary example-site --from $W/stage/hatamoto-$OLD" "$LOG" || fail "(c) binary not rolled back"
if grep -qE 'restore|pg_|restart|suspend' "$LOG"; then fail "(c) a restore/restart command was run"; fi
pass "changed goose version: binary rolled back, manual DB restore message, exit 2"
echo "ALL OK"
