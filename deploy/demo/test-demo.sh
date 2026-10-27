#!/usr/bin/env bash
# test-demo.sh - proves the demo's deploy side on a throwaway PostgreSQL cluster and a local
# server, with nothing on this machine touched outside $TMPDIR:
#   setup.sql (roles, template database), the seeded template, reset.sh (dry run, a real run, a
#   second run, refusals), isolation of the demo roles from a stand-in main database, the unit
#   files, and the Caddy block (validate + live headers).
# Needs PG_BIN (initdb, pg_ctl, psql), Go (or HATAMOTO_BIN), caddy (REAL_CADDY) and python3.
# Without PG_BIN it prints a note and exits 0. Env: PG_BIN, HATAMOTO_BIN, REAL_CADDY, PGPORT_T (55951), APP_PORT (18192).
set -Eeuo pipefail
cd "$(git rev-parse --show-toplevel)"
here=deploy/demo
for f in $here/reset.sh $here/test-demo.sh scripts/demo-seed.sh; do bash -n "$f"; done
PG_BIN=${PG_BIN:-$(dirname "$(command -v initdb 2>/dev/null || echo /nonexistent/x)")}
if [ ! -x "$PG_BIN/initdb" ]; then echo "test-demo: no PostgreSQL (set PG_BIN); only the syntax was checked"; exit 0; fi
CADDY=${REAL_CADDY:-$(command -v caddy || true)}
export TMPDIR=${TMPDIR:-/tmp}
D=$(mktemp -d "$TMPDIR/demo-reset.XXXXXX")
PORT=${PGPORT_T:-55951}; APP=${APP_PORT:-18192}
cleanup() {
  [ -f "$D/svc.pid" ] && kill "$(cat "$D/svc.pid")" 2>/dev/null || true
  [ -f "$D/caddy.pid" ] && kill "$(cat "$D/caddy.pid")" 2>/dev/null || true
  [ -f "$D/up.pid" ] && kill "$(cat "$D/up.pid")" 2>/dev/null || true
  "$PG_BIN/pg_ctl" -D "$PGDEMO" -m immediate stop >/dev/null 2>&1 || true
  "$PG_BIN/pg_ctl" -D "$D/main-pg" -m immediate stop >/dev/null 2>&1 || true; rm -rf "$D"; }
trap cleanup EXIT
ok() { echo "ok   $*"; }
fail() { echo "FAIL $*"; for f in "$D"/*.log; do [ -f "$f" ] && { echo "--- $f"; tail -8 "$f"; }; done; exit 1; }
BIN=${HATAMOTO_BIN:-$D/hatamoto}
[ -x "$BIN" ] || (cd backend && go build -o "$BIN" ./cmd/hatamoto)

# ---- the demo's own cluster, built as the runbook does (docs/deploy/public-demo.md, step 3) ----------
# Two clusters: the demo's (PORT, the deploy/demo conf files) and a stand-in for the main site's (MPORT).
MPORT=$((PORT+1)); SOCK=$D/sock; mkdir -m 0700 "$SOCK"
PGDEMO=$D/var-lib/pg
"$PG_BIN/initdb" -D "$PGDEMO" -E UTF8 -U postgres --auth-local=trust --auth-host=scram-sha-256 >/dev/null
cp $here/postgresql.demo.conf "$PGDEMO/postgresql.demo.conf"; echo "include 'postgresql.demo.conf'" >> "$PGDEMO/postgresql.conf"
cp $here/pg_hba.demo.conf "$PGDEMO/pg_hba.conf"
HBA=$PGDEMO/pg_hba.conf
# seeding only (runbook step 6): the demo role may reach the template database; removed again after the seed
sed -i '1i host  hatamoto_demo_template  hatamoto_demo  127.0.0.1/32  scram-sha-256' "$HBA"
# the unit passes the port and the socket directory on the command line; so does this test
"$PG_BIN/pg_ctl" -D "$PGDEMO" -o "-p $PORT -k $SOCK" -l "$D/pg.log" -w start >/dev/null
SU() { "$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U postgres -X -At -v ON_ERROR_STOP=1 "$@"; }
AS() { # AS ROLE PASSWORD DB sql...
  local r=$1 p=$2 db=$3; shift 3; PGPASSWORD=$p "$PG_BIN/psql" -h 127.0.0.1 -p "$PORT" -U "$r" -d "$db" -X -At "$@"; }
[ "$(SU -c "show cluster_name")" = hatamoto-demo ] && [ "$(SU -c "show max_connections")" = 24 ] && [ "$(SU -c "show temp_file_limit")" = 32MB ] \
  && [ "$(SU -c "show max_parallel_workers_per_gather")" = 0 ] && [ "$(SU -c "show listen_addresses")" = 127.0.0.1 ] || fail "the demo cluster does not carry postgresql.demo.conf"
ok "own cluster: cluster_name hatamoto-demo, max_connections 24, temp_file_limit 32MB (per process; no parallel workers), loopback only"
"$PG_BIN/psql" -h 127.0.0.1 -p "$PORT" -U postgres -d postgres -X -At -c "select 1" >/dev/null 2>&1 && fail "the superuser connects over TCP"
ok "the cluster's superuser has no TCP login (socket only)"

# a stand-in for the main site's cluster: its own port, data dir, role and database, with a row that must survive everything
echo pw > "$D/pw"
"$PG_BIN/initdb" -D "$D/main-pg" -E UTF8 -A scram-sha-256 --pwfile="$D/pw" -U postgres >/dev/null
"$PG_BIN/pg_ctl" -D "$D/main-pg" -o "-p $MPORT -k $D -c listen_addresses=127.0.0.1" -l "$D/main-pg.log" -w start >/dev/null
MSU() { PGPASSWORD=pw "$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U postgres -X -At -v ON_ERROR_STOP=1 "$@"; }
MSU -c "create role mainapp login password 'mainpw'" >/dev/null
MSU -c "create database hatamoto_core_fake owner mainapp" >/dev/null
PGPASSWORD=mainpw "$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U mainapp -d hatamoto_core_fake -X -At -c "create table keep(x int); insert into keep values (42)" >/dev/null
main_oid=$(MSU -c "select oid from pg_database where datname='hatamoto_core_fake'")

# ---- setup.sql, twice (idempotent), with the passwords from the environment only -------------
export DEMO_APP_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -dc A-Za-z0-9)aaaaaaaa DEMO_RESET_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -dc A-Za-z0-9)bbbbbbbb
"$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U postgres -X -q -v ON_ERROR_STOP=1 -f $here/setup.sql >/dev/null
# a re-run needs no passwords and leaves the existing ones alone (the env files stay valid)
env -u DEMO_APP_PASSWORD -u DEMO_RESET_PASSWORD "$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U postgres -X -q -v ON_ERROR_STOP=1 -f $here/setup.sql >/dev/null || fail "setup.sql re-run without passwords"
PGPASSWORD=$DEMO_APP_PASSWORD "$PG_BIN/psql" -h 127.0.0.1 -p "$PORT" -U hatamoto_demo -d hatamoto_demo_template -X -At -c "select 1" >/dev/null 2>&1 || fail "the re-run changed the demo role's password"
PGPASSWORD=$DEMO_RESET_PASSWORD "$PG_BIN/psql" -h 127.0.0.1 -p "$PORT" -U hatamoto_demo_reset -d postgres -X -At -c "select 1" >/dev/null 2>&1 || fail "the re-run changed the reset role's password"
ok "setup.sql runs twice; the second run keeps the passwords"
DEMO_ROTATE_PASSWORDS=1 DEMO_APP_PASSWORD= DEMO_RESET_PASSWORD= "$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U postgres -X -f $here/setup.sql 2>&1 | grep -q "set DEMO_APP_PASSWORD" && ok "setup.sql refuses empty passwords when it must set them"
attrs=$(SU -c "select rolname, rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls from pg_roles where rolname like 'hatamoto_demo%' order by 1")
[ "$attrs" = $'hatamoto_demo|f|f|f|f|f\nhatamoto_demo_reset|f|f|t|f|f' ] || fail "role attributes: $attrs"
ok "demo role: no superuser/createrole/createdb; reset role: createdb only"

# ---- layout: data dir, seeded template data dir, env files ------------------------------------
R=$D/var-lib; mkdir -p "$R/data" "$R/template-data"; touch "$R/data/.hatamoto-demo"
APP_URL="postgresql://hatamoto_demo:$DEMO_APP_PASSWORD@127.0.0.1:$PORT/hatamoto_demo?sslmode=disable"
cat > "$D/demo.env" <<ENV
ENVIRONMENT=development
API_HOST=127.0.0.1
API_PORT=$APP
PUBLIC_BASE_URL=http://127.0.0.1:$APP
DATABASE_URL=$APP_URL
HATAMOTO_DATA=$R/data
MEDIA_UPLOAD_DIR=$R/data/uploads
HATAMOTO_DEMO=1
CSP_ENFORCE=true
HATAMOTO_DEMO_LOGIN_EMAIL=demo@example.test
HATAMOTO_DEMO_LOGIN_PASSWORD=demo-password-123
HATAMOTO_REGISTRY_URL=off
WORKER_DISABLE_JOBS=agent_ranges_refresh,agent_verify,update_check,webhook_deliver,webhook_prune
HATAMOTO_AGENT_RANGES=off
ENV
echo "127.0.0.1:$PORT:*:hatamoto_demo_reset:$DEMO_RESET_PASSWORD" > "$D/pgpass"; chmod 600 "$D/pgpass"
cat > "$D/reset.env" <<ENV
DEMO_APP_ENV=$D/demo.env
DEMO_DB=hatamoto_demo
DEMO_TEMPLATE_DB=hatamoto_demo_template
DEMO_OWNER_ROLE=hatamoto_demo
DEMO_DATA_DIR=$R/data
DEMO_DATA_TEMPLATE=$R/template-data
DEMO_DATA_ROOT=$R
DEMO_PG_DATA=$(realpath "$PGDEMO")
DEMO_PG_PORT=$PORT
DEMO_ADMIN_URL=postgresql://hatamoto_demo_reset@127.0.0.1:$PORT/postgres?sslmode=disable
DEMO_PGPASSFILE=$D/pgpass
DEMO_UNIT=hatamoto-demo.service
DEMO_USER=$(id -un)
DEMO_HEALTH_URL=http://127.0.0.1:$APP/health
DEMO_BALLAST_MB=4
ENV

# ---- the template: roles' bootstrap, then boot + seed it (as the demo role) ---------------------
# the app's way of making plugin roles, on the empty template (it travels with every copy); then the seed
SU -d hatamoto_demo_template -q -v app_role=hatamoto_demo -f deploy/sql/role-admin-bootstrap.sql >/dev/null 2>&1 || fail "plugin role bootstrap on the template"
ok "plugin role bootstrap runs on the template"
TPL_URL="postgresql://hatamoto_demo:$DEMO_APP_PASSWORD@127.0.0.1:$PORT/hatamoto_demo_template?sslmode=disable"
( set -a; . "$D/demo.env"; set +a
  export DATABASE_URL=$TPL_URL HATAMOTO_DATA=$R/template-data MEDIA_UPLOAD_DIR=$R/template-data/uploads
  "$BIN" serve >"$D/tpl.log" 2>&1 & echo $! > "$D/tpl.pid"
  for _ in $(seq 1 60); do curl -fs -o /dev/null "http://127.0.0.1:$APP/health" && break; sleep 1; done
  curl -fs -o /dev/null "http://127.0.0.1:$APP/health" || exit 1
  DATABASE_URL=$TPL_URL HATAMOTO_BIN=$BIN BASE=http://127.0.0.1:$APP bash scripts/demo-seed.sh >"$D/seed.log" 2>&1; rc=$?
  kill "$(cat "$D/tpl.pid")"; wait "$(cat "$D/tpl.pid")" 2>/dev/null; exit $rc ) || fail "seeding the template"
ok "template database seeded by the demo role"
# seeding is over: the demo role no longer reaches the template database (runbook step 6)
sed -i '/^host  hatamoto_demo_template  hatamoto_demo  127/d' "$HBA"; "$PG_BIN/pg_ctl" -D "$PGDEMO" reload >/dev/null; sleep 1
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo_template -c "select 1" >/dev/null 2>&1 && fail "the demo role still connects to the template database"
ok "after seeding, the demo role cannot connect to the template database"
[ "$(SU -d hatamoto_demo_template -c "select count(*) from hatamoto_entities")" -gt 3 ] || fail "template has no content"

# ---- a stand-in for systemctl that runs the real server --------------------------------------
cat > "$D/systemctl" <<STUB
#!/usr/bin/env bash
echo "systemctl \$*" >> "$D/systemctl.log"
case "\$1" in
  start) if [ "\${2:-}" = hatamoto-demo-pg.service ]; then   # the demo's Postgres unit
           "$PG_BIN/pg_ctl" -D "$PGDEMO" -o "-p $PORT -k $SOCK" -l "$D/pg.log" -w start >/dev/null 2>&1; exit 0; fi
         set -a; . "$D/demo.env"; set +a
         setsid "$BIN" serve >> "$D/svc.log" 2>&1 < /dev/null & echo \$! > "$D/svc.pid" ;;
  stop)  [ -f "$D/svc.pid" ] && { kill "\$(cat "$D/svc.pid")" 2>/dev/null || true; for _ in \$(seq 1 20); do kill -0 "\$(cat "$D/svc.pid")" 2>/dev/null || break; sleep 0.5; done; rm -f "$D/svc.pid"; } ; exit 0 ;;
esac
STUB
chmod +x "$D/systemctl"
reset() { SYSTEMCTL="$D/systemctl" PSQL="${PSQL:-$PG_BIN/psql}" DEMO_LOCK="$D/lock" bash $here/reset.sh --config "${CFG:-$D/reset.env}" "$@"; }

# ---- dry run: checks pass, nothing changes -----------------------------------------------------
echo sentinel > "$R/data/sentinel"
reset --dry-run >"$D/dry.out" 2>&1 || { cat "$D/dry.out"; fail "dry run"; }
grep -q "checks passed" "$D/dry.out" && grep -q "would: stop" "$D/dry.out" && grep -q "would: create hatamoto_demo from template" "$D/dry.out" || { cat "$D/dry.out"; fail "dry run output"; }
[ -f "$R/data/sentinel" ] && [ ! -e "$D/systemctl.log" ] && [ "$(SU -c "select count(*) from pg_database where datname='hatamoto_demo'")" = 0 ] || fail "the dry run changed something"
ok "dry run: every step listed, nothing changed"

# ---- real run 1 (the demo database does not exist yet) -----------------------------------------
reset >"$D/run1.out" 2>&1 || { cat "$D/run1.out"; fail "first reset"; }
grep -q "healthy: the demo is reset" "$D/run1.out" || fail "no health line"
[ ! -e "$R/data/sentinel" ] && [ -f "$R/data/.hatamoto-demo" ] || fail "data dir not wiped / marker lost"
curl -fs "http://127.0.0.1:$APP/features" | grep -q "data-hm-demo-banner" || fail "the reset demo does not serve the seeded site"
# the seeded pages under an enforced CSP (as demo.env sets): the enforcing header is there, and the seed content
# (and the demo banner) bring no inline script or inline handler that the policy would block
for path in / /about /contact /features /blog /blog/what-to-try; do
  curl -fsS -D "$D/h.txt" -o "$D/page.html" "http://127.0.0.1:$APP$path" || fail "GET $path"
  grep -qi '^content-security-policy:' "$D/h.txt" && ! grep -qi '^content-security-policy-report-only:' "$D/h.txt" || { cat "$D/h.txt"; fail "$path: no enforcing Content-Security-Policy header"; }
  # an executable inline script would be blocked; JSON-LD data blocks (type="application/ld+json") are not executed
  ! grep -iE '<script' "$D/page.html" | grep -viE ' src=|ld\+json' | grep -q . || fail "$path: an inline <script>"
  ! grep -qiE ' on(click|load|error|mouseover|submit|focus)=' "$D/page.html" || fail "$path: an inline event handler"
done
ok "the seeded pages are served with an enforcing CSP and contain no inline script or handler"
[ "$(SU -c "select shobj_description(oid,'pg_database') from pg_database where datname='hatamoto_demo'")" = hatamoto-demo ] || fail "demo database comment"
[ "$(SU -c "select pg_get_userbyid(datdba) from pg_database where datname='hatamoto_demo'")" = hatamoto_demo ] || fail "owner"
ok "real run: database recreated from the template, data dir restored, service started, healthy"

# ---- vandalism disappears on the next run; a second run is safe ---------------------------------
DATABASE_URL=$APP_URL HATAMOTO_DATA=$R/data "$BIN" entity create --schema post --name "Vandal" --slug vandal --publish >/dev/null 2>&1 || fail "could not vandalise"
echo junk > "$R/data/uploads/vandal.txt"
[ "$(AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -c "select count(*) from hatamoto_entities where slug='vandal'")" = 1 ] || fail "vandal row missing"
# a visitor installs a plugin: the app makes its role (cluster-wide) and schema, as plugindb does
id8=$(SU -d hatamoto_demo -c "select left(id,8) from hatamoto_instance")
SU -c "create role vtmp login password 'x'" >/dev/null; verifier=$(SU -c "select rolpassword from pg_authid where rolname='vtmp'"); SU -c "drop role vtmp" >/dev/null
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -v ON_ERROR_STOP=1 -c "select hatamoto_admin.provision_plugin_role('vandalplug', '$verifier')" \
  -c "grant connect on database hatamoto_demo to hm_p_${id8}_vandalplug" \
  -c "begin; create schema plugin_vandalplug authorization hm_p_${id8}_vandalplug; set local role hm_p_${id8}_vandalplug; create table plugin_vandalplug.t(x int); commit" >/dev/null 2>"$D/prov.err" || { cat "$D/prov.err"; fail "could not make the vandal plugin role"; }
# roles the reset must NOT touch: another database's plugin role, and one made by hand for this database
SU -c "create role hm_p_deadbeef_other nologin" >/dev/null; SU -c "create role hm_p_${id8}_byhand nologin" >/dev/null
[ "$(SU -c "select count(*) from pg_roles where rolname='hm_p_${id8}_vandalplug'")" = 1 ] || fail "orphan role not created"
reset --dry-run >"$D/dry2.out" 2>&1 || { cat "$D/dry2.out"; fail "dry run with an orphan"; }
grep -q "dropping plugin role hm_p_${id8}_vandalplug" "$D/dry2.out" && [ "$(SU -c "select count(*) from pg_roles where rolname='hm_p_${id8}_vandalplug'")" = 1 ] || { cat "$D/dry2.out"; fail "dry run did not list (or dropped) the orphan"; }
reset >"$D/run2.out" 2>&1 || { cat "$D/run2.out"; fail "second reset"; }
[ "$(SU -c "select count(*) from pg_roles where rolname='hm_p_${id8}_vandalplug'")" = 0 ] || { cat "$D/run2.out"; fail "the orphaned plugin role survived the reset"; }
[ "$(SU -c "select count(*) from pg_roles where rolname in ('hm_p_deadbeef_other','hm_p_${id8}_byhand')")" = 2 ] || fail "the reset dropped a role that is not this database's plugin role"
SU -c "drop role hm_p_deadbeef_other; drop role hm_p_${id8}_byhand" >/dev/null
ok "orphaned plugin roles are dropped by the reset; other roles are left alone"
[ "$(AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -c "select count(*) from hatamoto_entities where slug='vandal'")" = 0 ] && [ ! -e "$R/data/uploads/vandal.txt" ] || fail "changes survived the reset"
reset >"$D/run3.out" 2>&1 || fail "a third, immediate reset failed"
ok "changes are gone after a reset; running it again is safe"

# ---- a full demo disk: the ballast and the uploads go first, a downed Postgres is started ---------------
# A fake full state (no mount is possible here): a big file in the uploads and the ballast. A psql wrapper records
# what exists when the reset first talks to Postgres: both must be gone by then.
BALLAST=$R/.ballast; fallocate -l 4M "$BALLAST"; mkdir -p "$R/data/uploads"; head -c 6000000 /dev/zero > "$R/data/uploads/fill.bin"
cat > "$D/psql-spy" <<SPY
#!/usr/bin/env bash
[ -e "$D/spy.out" ] || { { [ -e "$BALLAST" ] && echo ballast || echo no-ballast; [ -e "$R/data/uploads/fill.bin" ] && echo uploads || echo no-uploads; } > "$D/spy.out"; }
exec "$PG_BIN/psql" "\$@"
SPY
chmod +x "$D/psql-spy"; rm -f "$D/spy.out"
PSQL="$D/psql-spy" reset >"$D/full.out" 2>&1 || { cat "$D/full.out"; fail "a reset of a full demo"; }
[ "$(cat "$D/spy.out")" = $'no-ballast\nno-uploads' ] || { cat "$D/spy.out"; fail "the ballast or the uploads still existed at the first Postgres call"; }
[ "$(stat -c %s "$BALLAST")" = 4194304 ] && [ ! -e "$R/data/uploads/fill.bin" ] || fail "the ballast is not back at its size, or the fill survived"
ok "full disk: the ballast and the uploads are deleted before the first Postgres call; the ballast is recreated at the end"
# Postgres down (what a full disk can do): the reset frees room, starts the demo's cluster, and recovers
"$PG_BIN/pg_ctl" -D "$PGDEMO" -m immediate stop >/dev/null
: > "$D/systemctl.log"
reset >"$D/pgdown.out" 2>&1 || { cat "$D/pgdown.out"; fail "a reset with the demo's Postgres down"; }
grep -q "systemctl start hatamoto-demo-pg.service" "$D/systemctl.log" && grep -q "healthy: the demo is reset" "$D/pgdown.out" || { cat "$D/pgdown.out" "$D/systemctl.log"; fail "the downed Postgres was not started"; }
[ "$(SU -c "select count(*) from pg_database where datname='hatamoto_demo'")" = 1 ] || fail "demo database missing after the Postgres-down reset"
ok "Postgres down: the reset starts hatamoto-demo-pg.service after freeing space, then resets as usual"

# ---- the main site's database is untouched ---------------------------------------------------
[ "$(MSU -c "select oid from pg_database where datname='hatamoto_core_fake'")" = "$main_oid" ] && [ "$(PGPASSWORD=mainpw "$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U mainapp -d hatamoto_core_fake -X -At -c "select x from keep")" = 42 ] || fail "main database changed"
ok "the main cluster's database is untouched (same oid, same row)"

# ---- refusals: each one exits 2 and changes nothing -----------------------------------------------
refused() { # refused LABEL CONFIGSED...: a config variant must be refused
  local label=$1; shift
  cp "$D/reset.env" "$D/bad.env"; for e in "$@"; do echo "$e" >> "$D/bad.env"; done
  local before; before=$(SU -c "select oid from pg_database where datname='hatamoto_demo'")
  CFG=$D/bad.env reset >"$D/refuse.out" 2>&1 && { cat "$D/refuse.out"; fail "$label was not refused"; }
  grep -q "REFUSED" "$D/refuse.out" || { cat "$D/refuse.out"; fail "$label: no REFUSED line"; }
  [ "$(SU -c "select oid from pg_database where datname='hatamoto_demo'")" = "$before" ] || fail "$label changed the demo database"
  [ "$(MSU -c "select oid from pg_database where datname='hatamoto_core_fake'")" = "$main_oid" ] || fail "$label touched the main database"
  ok "refused: $label"
}
refused "DEMO_DB is the main database" DEMO_DB=hatamoto_core_fake
refused "DEMO_DB not a demo name" DEMO_DB=postgres
refused "DEMO_DB differs from the service's DATABASE_URL" DEMO_DB=hatamoto_demo_other
refused "template equals the database" DEMO_TEMPLATE_DB=hatamoto_demo
refused "data dir /opt/hatamoto" DEMO_DATA_DIR=/opt/hatamoto/data
refused "data dir outside the demo root" DEMO_DATA_DIR=$D
refused "data dir with .." DEMO_DATA_DIR=$R/data/../../etc
refused "data dir differs from the service's HATAMOTO_DATA" DEMO_DATA_DIR=$R/template-data
rm "$R/data/.hatamoto-demo"; refused "data dir without the marker"; touch "$R/data/.hatamoto-demo"
SU -c "create database hatamoto_demo_decoy" >/dev/null
sed "s#/hatamoto_demo?#/hatamoto_demo_decoy?#" "$D/demo.env" > "$D/decoy.env"
refused "a database without the demo comment" DEMO_APP_ENV=$D/decoy.env DEMO_DB=hatamoto_demo_decoy
[ "$(SU -c "select count(*) from pg_database where datname='hatamoto_demo_decoy'")" = 1 ] || fail "the decoy was dropped"
refused "unreadable service env" DEMO_APP_ENV=$D/nope.env
refused "wrong admin password" "DEMO_ADMIN_URL=postgresql://hatamoto_demo_reset:wrong@127.0.0.1:$PORT/postgres?sslmode=disable"
refused "a password in DEMO_ADMIN_URL (it would show in the process list)" "DEMO_ADMIN_URL=postgresql://hatamoto_demo_reset:$DEMO_RESET_PASSWORD@127.0.0.1:$PORT/postgres?sslmode=disable"
chmod 644 "$D/pgpass"; refused "a pgpass file that others can read"; chmod 600 "$D/pgpass"
refused "a data template with dot segments" DEMO_DATA_TEMPLATE=$R/template-data/../template-data

# ---- the guards on WHICH cluster the reset talks to ------------------------------------------------------
# a reset config that points at the main site's cluster (its port and its data dir) must be refused
MSU -c "create role hatamoto_demo_reset login password 'x-main-reset-pw'" >/dev/null
MSU -c "grant pg_read_all_settings to hatamoto_demo_reset" >/dev/null
echo "127.0.0.1:$MPORT:*:hatamoto_demo_reset:x-main-reset-pw" >> "$D/pgpass"
sed "s#@127.0.0.1:$PORT/#@127.0.0.1:$MPORT/#" "$D/demo.env" > "$D/mainport.env"
refused "the demo cluster's port is wrong (the reset config says another port)" DEMO_PG_PORT=$MPORT
refused "the admin URL points at another cluster than DEMO_PG_PORT" "DEMO_ADMIN_URL=postgresql://hatamoto_demo_reset@127.0.0.1:$MPORT/postgres?sslmode=disable"
grep -q "listens on port $MPORT" "$D/refuse.out" || { cat "$D/refuse.out"; fail "no port message"; }
refused "the connected cluster is not the one at DEMO_PG_DATA (a main-site cluster, right port, wrong data dir)" DEMO_APP_ENV=$D/mainport.env DEMO_PG_PORT=$MPORT "DEMO_ADMIN_URL=postgresql://hatamoto_demo_reset@127.0.0.1:$MPORT/postgres?sslmode=disable"
grep -q "data directory" "$D/refuse.out" || { cat "$D/refuse.out"; fail "no data directory message"; }
refused "the demo service connects to another port than DEMO_PG_PORT" DEMO_APP_ENV=$D/mainport.env
refused "DEMO_PG_PORT 5432" DEMO_PG_PORT=5432
refused "DEMO_PG_DATA outside the demo root" DEMO_PG_DATA=/var/lib/postgresql/16/main
refused "DEMO_PG_DATA inside the live data dir" DEMO_PG_DATA=$R/data/pg
refused "DEMO_PG_DATA with dot segments" DEMO_PG_DATA=$R/pg/../pg
sed -i "s/^cluster_name = 'hatamoto-demo'/cluster_name = 'other'/" "$PGDEMO/postgresql.demo.conf"; "$PG_BIN/pg_ctl" -D "$PGDEMO" restart -o "-p $PORT -k $SOCK" -l "$D/pg.log" -w >/dev/null
refused "the cluster is not named hatamoto-demo"
sed -i "s/^cluster_name = 'other'/cluster_name = 'hatamoto-demo'/" "$PGDEMO/postgresql.demo.conf"; "$PG_BIN/pg_ctl" -D "$PGDEMO" restart -o "-p $PORT -k $SOCK" -l "$D/pg.log" -w >/dev/null
reset --dry-run >"$D/dry3.out" 2>&1 || { cat "$D/dry3.out"; fail "dry run after the guards"; }
ok "the cluster guards refuse a wrong port, data dir, name; the right cluster passes"

# ---- isolation: what the demo roles can and cannot do in their cluster -----------------------------------
AS hatamoto_demo "$DEMO_APP_PASSWORD" postgres -c "select 1" >/dev/null 2>&1 && fail "the demo role connects to the postgres database"
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo_template -c "select 1" >/dev/null 2>&1 && fail "the demo role connects to the template database"
AS hatamoto_demo_reset "$DEMO_RESET_PASSWORD" hatamoto_demo -c "select 1" >/dev/null || fail "the reset role cannot read the demo database"
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -c "select 1" >/dev/null || fail "the demo role cannot use its own database"
PGPASSWORD=$DEMO_APP_PASSWORD "$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U hatamoto_demo -d hatamoto_demo -X -At -c "select 1" >/dev/null 2>&1 && fail "the demo role connects over the unix socket"
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -c "create role evil" >/dev/null 2>&1 && fail "the demo role creates roles"
AS hatamoto_demo "$DEMO_APP_PASSWORD" hatamoto_demo -c "create database evil" >/dev/null 2>&1 && fail "the demo role creates databases"
AS hatamoto_demo_reset "$DEMO_RESET_PASSWORD" postgres -c "create role evil" >/dev/null 2>&1 && fail "the reset role creates roles"
# the demo roles do not exist in the main cluster at all (its own roles, its own port)
PGPASSWORD=$DEMO_APP_PASSWORD "$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U hatamoto_demo -d hatamoto_core_fake -X -At -c "select 1" >/dev/null 2>&1 && fail "the demo role connects to the main cluster"
[ "$(MSU -c "select count(*) from pg_database where datname='hatamoto_core_fake'")" = 1 ] || fail "main database gone"
ok "isolation: roles stay inside their databases, cannot create roles or databases, and do not exist in the main cluster"
# plugin roles (hm_p_<id>_<name>) reach the demo database, and only it
SU -c "create role hm_p_00000000_probe login password 'probe-pw'" >/dev/null
AS hm_p_00000000_probe probe-pw hatamoto_demo -c "select 1" >/dev/null 2>&1 || true   # no CONNECT privilege yet, as for a real plugin role
SU -d hatamoto_demo -c "grant connect on database hatamoto_demo to hm_p_00000000_probe" >/dev/null
AS hm_p_00000000_probe probe-pw hatamoto_demo -c "select 1" >/dev/null || fail "a plugin role cannot reach the demo database"
AS hm_p_00000000_probe probe-pw postgres -c "select 1" >/dev/null 2>&1 && fail "a plugin role reaches the postgres database"
AS hm_p_00000000_probe probe-pw hatamoto_demo_template -c "select 1" >/dev/null 2>&1 && fail "a plugin role reaches the template"
SU -d hatamoto_demo -c "revoke connect on database hatamoto_demo from hm_p_00000000_probe" >/dev/null; SU -c "drop role hm_p_00000000_probe" >/dev/null
ok "plugin roles reach only the demo database (pg_hba)"

# ---- an upgrade: a new template gets a new instance id; drop-stale-roles.sql removes the old roles ------
# (docs/deploy/public-demo.md "After a new release": drop both databases, drop the stale roles, recreate the template)
"$PG_BIN/psql" -h "$SOCK" -p "$PORT" -U postgres -X -q -v ON_ERROR_STOP=1 -c "drop database hatamoto_demo with (force)" -c "drop database hatamoto_demo_template with (force)" >/dev/null
SU -c "select count(*) from pg_roles where rolname like 'hatamoto\_role\_admin\_%' or rolname like 'hm\_p\_%'" | grep -qv '^0$' || fail "the test cluster has no role-admin/plugin roles to drop"
SU -v ON_ERROR_STOP=1 -q -f $here/drop-stale-roles.sql >/dev/null 2>"$D/stale.err" || { cat "$D/stale.err"; fail "drop-stale-roles.sql"; }
[ "$(SU -c "select count(*) from pg_roles where rolname like 'hatamoto\_role\_admin\_%' or rolname like 'hm\_p\_%'")" = 0 ] || fail "stale roles survived"
[ "$(SU -c "select count(*) from pg_roles where rolname in ('hatamoto_demo','hatamoto_demo_reset')")" = 2 ] || fail "drop-stale-roles.sql dropped a demo login"
"$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U postgres -X -q -c "select 1" >/dev/null 2>&1 || true
PGPASSWORD=pw "$PG_BIN/psql" -h 127.0.0.1 -p "$MPORT" -U postgres -X -At -f $here/drop-stale-roles.sql 2>&1 | grep -q "not the hatamoto-demo cluster\|cluster_name" || fail "drop-stale-roles.sql did not refuse the main cluster"
ok "an upgrade leaves no stale roles: drop-stale-roles.sql drops the old role admin and plugin roles, keeps the logins, and refuses another cluster"

# ---- a mistyped HATAMOTO_DEMO must stop the server, not start a normal instance ----------------------
for v in enabled 2 tru; do
  HATAMOTO_DEMO=$v timeout 20 "$BIN" serve >"$D/badflag.out" 2>&1 && fail "HATAMOTO_DEMO=$v started the server"
  grep -q "invalid HATAMOTO_DEMO" "$D/badflag.out" || { cat "$D/badflag.out"; fail "HATAMOTO_DEMO=$v: no clear error"; }
done
ok "HATAMOTO_DEMO=enabled/2/tru stops the server with an error"

# ---- unit files ------------------------------------------------------------------------------------
u=$here/hatamoto-demo.service
for want in User=hatamoto-demo ProtectSystem=strict ProtectHome=true NoNewPrivileges=true ReadWritePaths=/var/lib/hatamoto-demo/data ReadOnlyPaths=/var/lib/hatamoto-demo/template-data InaccessiblePaths="-/opt/hatamoto -/etc/hatamoto -/var/lib/hatamoto-demo/pg" ExecStart="/opt/hatamoto-demo/bin/hatamoto serve" MemoryMax=; do
  grep -q -- "^$want" $u || fail "$u lacks $want"
done
grep -q '^ReadWritePaths=/var/lib/hatamoto-demo/data$' $u && ! grep -q '^ReadWritePaths=.*template-data' $u || fail "$u lets the demo write the template data dir"
for want in CSP_ENFORCE=true HATAMOTO_DEMO=1 HATAMOTO_DEMO_MEDIA_QUOTA_MB= HATAMOTO_DEMO_WRITE_BUDGET_MB=; do
  grep -q "^$want" $here/demo.env.example || fail "demo.env.example lacks $want"
done
p=$here/hatamoto-demo-pg.service
for want in User=hatamoto-demo-pg ProtectSystem=strict ReadWritePaths=/var/lib/hatamoto-demo/pg RequiresMountsFor=/var/lib/hatamoto-demo MemoryMax= "ExecStart=/usr/lib/postgresql/16/bin/postgres -D /var/lib/hatamoto-demo/pg -p 5442"; do
  grep -q -- "^$want" $p || fail "$p lacks $want"
done
grep -q 'hatamoto-demo-pg.service' $u && grep -q 'hatamoto-demo-pg.service' $here/hatamoto-demo-reset.service || fail "the demo and reset units do not wait for the demo cluster"
for f in demo.env.example reset.env.example; do
  grep -q '5442' $here/$f && ! grep -q '5432' $here/$f || fail "$f is not on the demo cluster's port"
done
grep -q '^DEMO_PG_DATA=/var/lib/hatamoto-demo/pg' $here/reset.env.example || fail "reset.env.example has no DEMO_PG_DATA"
grep -q '^OnCalendar=hourly' $here/hatamoto-demo-reset.timer || fail "timer is not hourly"
grep -q 'hatamoto-demo-reset.service' $here/hatamoto-demo-reset.timer || fail "timer does not run the reset"
if command -v systemd-analyze >/dev/null; then
  out=$(systemd-analyze verify $here/hatamoto-demo.service $here/hatamoto-demo-pg.service $here/hatamoto-demo-reset.service $here/hatamoto-demo-reset.timer 2>&1 | grep -v "is not executable: No such file" || true)
  [ -z "$out" ] || fail "systemd-analyze verify: $out"
fi
ok "unit files: own user, ProtectSystem, no /opt/hatamoto, hourly timer; systemd-analyze verify clean"

# ---- Caddy block: validates, and answers with the right headers ------------------------------------------
if [ -n "$CADDY" ]; then
  "$CADDY" validate --config $here/demo.hatamoto.dev.caddy --adapter caddyfile >/dev/null 2>&1 || fail "caddy validate"
  ok "caddy validate: $here/demo.hatamoto.dev.caddy"
  UP=$((APP + 1)); CP=$((APP + 2))
  cat > "$D/up.py" <<PY
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.send_header("Strict-Transport-Security","max-age=1"); self.send_header("X-Frame-Options","DENY")
        self.send_header("Content-Type","text/html"); self.end_headers(); self.wfile.write(b"<html>upstream</html>")
    do_HEAD = do_GET
http.server.HTTPServer(("127.0.0.1", $UP), H).serve_forever()
PY
  python3 "$D/up.py" >/dev/null 2>&1 & echo $! > "$D/up.pid"
  # the same block, served on a local port instead of the public name and the demo port replaced by the stand-in
  sed -e "s#^demo.hatamoto.dev {#http://127.0.0.1:$CP {#" -e "s#127.0.0.1:8091#127.0.0.1:$UP#" $here/demo.hatamoto.dev.caddy > "$D/Caddyfile"
  ( cd "$D" && "$CADDY" run --config "$D/Caddyfile" --adapter caddyfile >"$D/caddy.log" 2>&1 & echo $! > "$D/caddy.pid" )
  for _ in $(seq 1 30); do curl -fs -o /dev/null "http://127.0.0.1:$CP/" && break; sleep 0.5; done
  h=$(curl -sI "http://127.0.0.1:$CP/about")
  grep -qi '^x-robots-tag: noindex, nofollow, noarchive' <<<"$h" || { echo "$h"; fail "no X-Robots-Tag noindex"; }
  [ "$(grep -ci '^strict-transport-security:' <<<"$h")" = 1 ] && grep -qi 'max-age=63072000' <<<"$h" || { echo "$h"; fail "HSTS is not exactly one Caddy header"; }
  grep -qi '^x-frame-options: DENY' <<<"$h" || fail "the app's own headers must pass through"
  curl -s "http://127.0.0.1:$CP/robots.txt" | grep -q "Disallow: /" || fail "robots.txt"
  ok "caddy live: X-Robots-Tag noindex, one HSTS (Caddy's), app headers kept, robots.txt disallows"
else
  echo "note: no caddy (set REAL_CADDY): the Caddy block was not checked"
fi
echo "test-demo: all ok"
