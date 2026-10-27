#!/usr/bin/env bash
# test-ship-restore.sh - syntax check + local test of scripts/ship-db-restore.sh
# against a scratch PostgreSQL (PG16). Reproduces the old failing command, then
# checks the swap restore: goose version back to 15, rows present, owner kept, database ACL +
# settings preserved, failures leave the live DB alone, API never started.
#
# Env: PG (PG16 bin dir; default: the directory of pg_ctl on PATH),
#      PGPORT_T (default 55992), SCRATCH (default /tmp/hm-pg9)
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"

for f in scripts/ship.sh scripts/ship-db-restore.sh scripts/test-ship-restore.sh; do bash -n "$f"; done
echo "bash -n ok"

PG=${PG:-$(dirname "$(command -v pg_ctl)")}
PORT=${PGPORT_T:-55992}
SCRATCH=${SCRATCH:-/tmp/hm-pg9}
DB=hm_core; DUMP=$SCRATCH/hcore.dump; TS=20261014-000000
export PGHOST=/tmp PGPORT=$PORT PGUSER=postgres
P() { "$PG/$1" "${@:2}"; }
Q() { P psql -X -d "${2:-$DB}" -Atc "$1"; }
goose() { (cd backend/migrations && go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir . postgres "postgres://postgres@/$DB?host=/tmp&port=$PORT&sslmode=disable" "$@"); }

cleanup() { P pg_ctl -D "$SCRATCH" -m immediate stop >/dev/null 2>&1 || true; rm -rf "$SCRATCH"; }
trap cleanup EXIT
rm -rf "$SCRATCH"
P initdb -D "$SCRATCH" -U postgres --auth=trust >/dev/null
P pg_ctl -D "$SCRATCH" -o "-p $PORT -k /tmp" -l "$SCRATCH/log" -w start >/dev/null

printf 'DATABASE_URL=postgres://x@h/%s?sslmode=disable\n' $DB > "$SCRATCH/ok.env"
Q "CREATE ROLE hm_owner LOGIN" postgres >/dev/null
P createdb -O hm_owner $DB
goose up-to 15 >/dev/null
[[ $(Q "SELECT max(version_id) FROM goose_db_version") == 15 ]]
Q "INSERT INTO hatamoto_agent_hits (occurred_at, source) VALUES
   ('2026-10-05+00','backend'),('2026-11-05+00','log'),('2030-01-01+00','log')" >/dev/null
Q "SELECT count(*) FROM hatamoto_agent_hits_default" | grep -qx 1
Q "GRANT SELECT ON hatamoto_agent_hits TO PUBLIC" >/dev/null
# database-level state a dump does not carry: ACL, ALTER DATABASE / ROLE .. IN DATABASE settings
Q "CREATE ROLE other LOGIN" postgres >/dev/null
Q "REVOKE ALL ON DATABASE $DB FROM PUBLIC; GRANT CONNECT ON DATABASE $DB TO hm_owner;
   ALTER DATABASE $DB SET statement_timeout = '7s'; ALTER ROLE hm_owner IN DATABASE $DB SET search_path = public, pg_catalog;
   ALTER DATABASE $DB CONNECTION LIMIT 11" postgres >/dev/null
ACL_BEFORE=$(Q "SELECT datacl FROM pg_database WHERE datname='$DB'" postgres)
if P psql -X -U other -d $DB -Atc "SELECT 1" >/dev/null 2>&1; then echo "FAIL: setup: other can connect"; exit 1; fi
BEFORE=$(Q "SELECT count(*) FROM hatamoto_agent_hits")

# dump exactly as ship.sh does (-Fc, as the superuser)
P pg_dump -Fc $DB > "$DUMP"; P pg_restore --list < "$DUMP" >/dev/null
goose up >/dev/null
[[ $(Q "SELECT max(version_id) FROM goose_db_version") -gt 15 ]]

echo "-- old command (must fail)"
if P pg_restore --clean --if-exists --single-transaction -d $DB < "$DUMP" 2> "$SCRATCH/old.err"; then
  echo "FAIL: old restore unexpectedly succeeded"; exit 1
fi
grep -q 'cannot drop inherited constraint' "$SCRATCH/old.err" && echo "reproduced: $(grep -m1 'cannot drop' "$SCRATCH/old.err")" \
  || { echo "FAIL: old restore failed differently:"; cat "$SCRATCH/old.err"; exit 1; }
[[ $(Q "SELECT max(version_id) FROM goose_db_version") -gt 15 ]]   # single-transaction: nothing changed

echo "-- guards and failures leave the live database alone"
H() { PGBIN=$PG PG_AS="" API_STOP="echo api-stop" scripts/ship-db-restore.sh "$@"; }
NDB=$(Q "SELECT count(*) FROM pg_database" postgres)
printf 'DATABASE_URL=postgres://x@h/other_db?sslmode=disable\n' > "$SCRATCH/wrong.env"
if CONFIG_ENV="$SCRATCH/wrong.env" H $DB "$DUMP" $TS 2>&1; then echo "FAIL: wrong DATABASE_URL accepted"; exit 1; fi
echo garbage > "$SCRATCH/bad.dump"
if H $DB "$SCRATCH/bad.dump" $TS >/dev/null 2>&1; then echo "FAIL: bad dump accepted"; exit 1; fi
[[ $(Q "SELECT count(*) FROM pg_database" postgres) == "$NDB" ]] || { echo "FAIL: leftover database after failed restore"; exit 1; }
[[ $(Q "SELECT max(version_id) FROM goose_db_version") -gt 15 ]]
echo "-- new restore"
OUT=$(CONFIG_ENV="$SCRATCH/ok.env" H $DB "$DUMP" $TS)
echo "$OUT"
[[ $(Q "SELECT max(version_id) FROM goose_db_version") == 15 ]] || { echo "FAIL: version not 15"; exit 1; }
[[ $(Q "SELECT count(*) FROM hatamoto_agent_hits") == "$BEFORE" ]] || { echo "FAIL: rows"; exit 1; }
[[ $(Q "SELECT count(*) FROM hatamoto_agent_hits_default") == 1 ]]
[[ $(Q "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='$DB'" postgres) == hm_owner ]] || { echo "FAIL: db owner"; exit 1; }
[[ $(Q "SELECT tableowner FROM pg_tables WHERE tablename='hatamoto_agent_hits'") == postgres ]]
Q "SELECT has_table_privilege('hm_owner','hatamoto_agent_hits','SELECT')" | grep -qx t
[[ $(Q "SELECT max(version_id) FROM goose_db_version" ${DB}_failed_$TS) -gt 15 ]] || { echo "FAIL: failed db missing"; exit 1; }
grep -q "api-stop" <<< "$OUT" && ! grep -q "api-start" <<< "$OUT"
[[ $(Q "SELECT datacl FROM pg_database WHERE datname='$DB'" postgres) == "$ACL_BEFORE" ]] || { echo "FAIL: database ACL"; exit 1; }
if P psql -X -U other -d $DB -Atc "SELECT 1" >/dev/null 2>&1; then echo "FAIL: unrelated role can connect after swap"; exit 1; fi
[[ $(P psql -X -U hm_owner -d $DB -Atc "SHOW statement_timeout") == 7s ]] || { echo "FAIL: ALTER DATABASE setting"; exit 1; }
[[ $(P psql -X -U hm_owner -d $DB -Atc "SHOW search_path") == "public, pg_catalog" ]] || { echo "FAIL: role-in-database setting: $(P psql -X -U hm_owner -d $DB -Atc "SHOW search_path")"; exit 1; }
[[ $(Q "SELECT datconnlimit FROM pg_database WHERE datname='$DB'" postgres) == 11 ]] || { echo "FAIL: connection limit"; exit 1; }
grep -q "failed databases still" <<< "$OUT"
# ship.sh contract: failed restore => exit 3, the helper never starts the API
grep -q 'RB_DB_FAILED=1' scripts/ship.sh && grep -q 'exit 3' scripts/ship.sh
[[ $(Q "SELECT count(*) FROM pg_database WHERE datname='${DB}_restore_$TS'" postgres) == 0 ]]
echo "-- rollback fingerprint sees an out-of-order migration"
FP_SQL="SELECT count(*) || ':' || coalesce(max(id), 0) FROM goose_db_version"
grep -qF "$FP_SQL" scripts/ship.sh || { echo "FAIL: ship.sh fingerprint SQL differs from the one tested here"; exit 1; }
FP0=$(Q "$FP_SQL"); MAX0=$(Q "SELECT max(version_id) FROM goose_db_version")
# what goose does for an older migration merged late: a new row with a smaller version_id
Q "INSERT INTO goose_db_version (version_id, is_applied) VALUES (3, true)" >/dev/null
[[ $(Q "SELECT max(version_id) FROM goose_db_version") == "$MAX0" ]] || { echo "FAIL: setup: max(version_id) changed"; exit 1; }
[[ $(Q "$FP_SQL") != "$FP0" ]] || { echo "FAIL: fingerprint unchanged after an out-of-order migration"; exit 1; }
echo "PASS: ship-db-restore"
