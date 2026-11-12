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
)
DEPLOY=$(git -C "$W/main" rev-parse HEAD)
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
  : >"$LOG"; rm -f "$W/mark" "$W/bin-state" "$LOG.goose" "$LOG.stdin"; rm -rf "$W/stage"
  set +e
  OUT=$(cd "$W/main" && env PATH="$S:$PATH" STUB_LOG="$LOG" FAKE_MARK="$W/mark" FAKE_BIN_STATE="$W/bin-state" \
    SHIP_EXPECTED_ORIGIN="$ORIGIN_SLUG" DEPLOY_HOST=stubhost DEPLOY_TARGET=hmctl:example-site \
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
echo "ALL OK"
