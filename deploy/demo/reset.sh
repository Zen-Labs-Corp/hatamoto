#!/usr/bin/env bash
# reset.sh - put the public demo back to its seeded state. Run hourly by
# hatamoto-demo-reset.timer, as root, on the demo's own database and data dir.
#
#   0. stop the demo service, delete the ballast file and empty the data dir (uploads): FIRST, before any
#      Postgres step, so a demo filesystem that visitors filled has room again; if the demo's Postgres is
#      down (it can fail to restart on a full disk) start it now that there is room
#   1. (stopped above)
#   2. drop the plugin roles visitors created since the template (Postgres roles are cluster-wide)
#   3. drop the demo database and create it again from the template database
#   4. empty the demo data dir and fill it from the template data dir
#   5. recreate the ballast, start the service and wait until it is healthy
#
# It touches nothing else. Before any change it checks, and refuses (exit 2) unless:
#   * the database is named hatamoto_demo* , is not the main site's database, and the
#     name in the reset config is the one in the demo service's DATABASE_URL;
#   * the data dir is the demo service's HATAMOTO_DATA, lies under /var/lib/hatamoto-demo
#     (or DEMO_DATA_ROOT) and holds the marker file .hatamoto-demo;
#   * the database carries the comment "hatamoto-demo" (set once by the runbook);
#   * the Postgres cluster it talks to is the demo's own: its port is DEMO_PG_PORT (also the port in
#     the demo service's DATABASE_URL), its data_directory is DEMO_PG_DATA (under the demo root,
#     never inside the live data dir) and its cluster_name is hatamoto-demo.
# So a wrong config can never drop the main database or wipe /opt/hatamoto.
# It is safe to run again at any point: every step is repeatable.
#
# Usage: reset.sh [--dry-run] [--config FILE]     (default /etc/hatamoto-demo/reset.env)
#   --dry-run  run every check (read-only) and print each step instead of doing it.
# Config (reset.env, see deploy/demo/reset.env.example): DEMO_APP_ENV DEMO_DB DEMO_TEMPLATE_DB
#   DEMO_DATA_DIR DEMO_DATA_TEMPLATE DEMO_ADMIN_URL DEMO_OWNER_ROLE DEMO_UNIT DEMO_USER
#   DEMO_HEALTH_URL DEMO_DATA_ROOT DEMO_PGPASSFILE DEMO_PG_DATA DEMO_PG_PORT, and optionally DEMO_PG_UNIT
#   (hatamoto-demo-pg.service), DEMO_BALLAST (default $DEMO_DATA_ROOT/.ballast) and DEMO_BALLAST_MB (64; 0 = none).
# DEMO_ADMIN_URL must not carry a password (it would show in the process list): the password
# goes in a 0600 pgpass file named by DEMO_PGPASSFILE, e.g. /etc/hatamoto-demo/pgpass with the line
#   127.0.0.1:5442:*:hatamoto_demo_reset:<password>
# Overrides for tests: PSQL, SYSTEMCTL, CURL, DEMO_LOCK (default /run/hatamoto-demo-reset.lock).
set -Eeuo pipefail

DRY=0; CONFIG=/etc/hatamoto-demo/reset.env
while [ $# -gt 0 ]; do
  case "$1" in
    -n|--dry-run) DRY=1 ;;
    --config) CONFIG=${2:?--config needs a file}; shift ;;
    -h|--help) sed -n '2,38p' "$0"; exit 0 ;;
    *) echo "reset: unknown argument $1" >&2; exit 64 ;;
  esac
  shift
done

PSQL=${PSQL:-psql}; SYSTEMCTL=${SYSTEMCTL:-systemctl}; CURL=${CURL:-curl}
say() { echo "reset: $*"; }
refuse() { echo "reset: REFUSED: $*" >&2; exit 2; }
step() { if [ "$DRY" = 1 ]; then say "would: $*"; else say "$*"; fi; }

[ -r "$CONFIG" ] || refuse "cannot read $CONFIG"
# shellcheck disable=SC1090
. "$CONFIG"
: "${DEMO_APP_ENV:?}" "${DEMO_DB:?}" "${DEMO_TEMPLATE_DB:?}" "${DEMO_DATA_DIR:?}" "${DEMO_ADMIN_URL:?}" "${DEMO_PG_DATA:?}" "${DEMO_PG_PORT:?}"
DEMO_OWNER_ROLE=${DEMO_OWNER_ROLE:-hatamoto_demo}
DEMO_UNIT=${DEMO_UNIT:-hatamoto-demo.service}
DEMO_USER=${DEMO_USER:-hatamoto-demo}
DEMO_DATA_ROOT=${DEMO_DATA_ROOT:-/var/lib/hatamoto-demo}
DEMO_HEALTH_URL=${DEMO_HEALTH_URL:-http://127.0.0.1:8091/health}
DEMO_DATA_TEMPLATE=${DEMO_DATA_TEMPLATE:-}
DEMO_PGPASSFILE=${DEMO_PGPASSFILE:-}
DEMO_PG_UNIT=${DEMO_PG_UNIT:-hatamoto-demo-pg.service}
DEMO_BALLAST=${DEMO_BALLAST:-$DEMO_DATA_ROOT/.ballast}
DEMO_BALLAST_MB=${DEMO_BALLAST_MB:-64}

# ---- checks: nothing below runs unless every one passes ----------------------------
ident='^[a-z][a-z0-9_]{0,62}$'
[[ $DEMO_DB =~ $ident && $DEMO_DB == hatamoto_demo* ]] || refuse "DEMO_DB '$DEMO_DB' must be a plain name starting with hatamoto_demo"
[[ $DEMO_TEMPLATE_DB =~ $ident && $DEMO_TEMPLATE_DB == hatamoto_demo* ]] || refuse "DEMO_TEMPLATE_DB '$DEMO_TEMPLATE_DB' must be a plain name starting with hatamoto_demo"
[[ $DEMO_OWNER_ROLE =~ $ident ]] || refuse "DEMO_OWNER_ROLE '$DEMO_OWNER_ROLE' is not a plain role name"
[ "$DEMO_DB" != "$DEMO_TEMPLATE_DB" ] || refuse "DEMO_DB and DEMO_TEMPLATE_DB are the same"
[[ $DEMO_PG_PORT =~ ^[0-9]{2,5}$ ]] || refuse "DEMO_PG_PORT '$DEMO_PG_PORT' is not a port number"
[ "$DEMO_PG_PORT" != 5432 ] || refuse "DEMO_PG_PORT is 5432, the port of a default (the main site's) cluster: the demo has its own"
case "$DEMO_DB" in hatamoto_core|hatamoto|postgres|template0|template1) refuse "DEMO_DB '$DEMO_DB' is not a demo database" ;; esac

# the data dir: absolute, no dot segments, under the demo root, never near /opt/hatamoto
case "$DEMO_DATA_DIR" in /*) ;; *) refuse "DEMO_DATA_DIR '$DEMO_DATA_DIR' is not absolute" ;; esac
case "/$DEMO_DATA_DIR/" in */../*|*/./*) refuse "DEMO_DATA_DIR '$DEMO_DATA_DIR' has dot segments" ;; esac
[ "$DEMO_DATA_DIR" != "$DEMO_DATA_ROOT" ] && [[ $DEMO_DATA_DIR == "$DEMO_DATA_ROOT"/* ]] || refuse "DEMO_DATA_DIR '$DEMO_DATA_DIR' must be a directory under $DEMO_DATA_ROOT"
# the ballast: a file directly in the demo root, named .ballast*, of a plain size
[[ $DEMO_BALLAST_MB =~ ^[0-9]{1,5}$ ]] || refuse "DEMO_BALLAST_MB '$DEMO_BALLAST_MB' is not a number"
[ "$(dirname -- "$DEMO_BALLAST")" = "${DEMO_DATA_ROOT%/}" ] && [[ $(basename -- "$DEMO_BALLAST") == .ballast* ]] || refuse "DEMO_BALLAST '$DEMO_BALLAST' must be a file named .ballast* directly in $DEMO_DATA_ROOT"
# the cluster's data dir: absolute, no dot segments, under the demo root, beside (not inside) the live data dir
case "$DEMO_PG_DATA" in /*) ;; *) refuse "DEMO_PG_DATA '$DEMO_PG_DATA' is not absolute" ;; esac
case "/$DEMO_PG_DATA/" in */../*|*/./*) refuse "DEMO_PG_DATA '$DEMO_PG_DATA' has dot segments" ;; esac
[[ $DEMO_PG_DATA == "$DEMO_DATA_ROOT"/* ]] || refuse "DEMO_PG_DATA '$DEMO_PG_DATA' must be a directory under $DEMO_DATA_ROOT (the size-capped filesystem)"
case "$DEMO_PG_DATA/" in "$DEMO_DATA_DIR"/*|"${DEMO_DATA_TEMPLATE:-/nonexistent}"/*) refuse "DEMO_PG_DATA '$DEMO_PG_DATA' lies inside the data dir or the data template" ;; esac
case "$DEMO_PG_DATA/" in /var/lib/postgresql/*|/opt/hatamoto/*) refuse "DEMO_PG_DATA '$DEMO_PG_DATA' is a protected location" ;; esac
case "$DEMO_DATA_DIR/" in /opt/hatamoto/*|/opt/hatamoto-core/*|/|/root/*|/etc/*|/usr/*|/var/lib/postgresql/*) refuse "DEMO_DATA_DIR '$DEMO_DATA_DIR' is a protected location" ;; esac
[ -d "$DEMO_DATA_DIR" ] && [ ! -L "$DEMO_DATA_DIR" ] || refuse "DEMO_DATA_DIR '$DEMO_DATA_DIR' is not a directory (or is a symlink)"
[ -f "$DEMO_DATA_DIR/.hatamoto-demo" ] || refuse "$DEMO_DATA_DIR/.hatamoto-demo is missing: only a directory the runbook marked is wiped"
if [ -n "$DEMO_DATA_TEMPLATE" ]; then
  case "$DEMO_DATA_TEMPLATE" in "$DEMO_DATA_ROOT"/*) ;; *) refuse "DEMO_DATA_TEMPLATE '$DEMO_DATA_TEMPLATE' must be under $DEMO_DATA_ROOT" ;; esac
  [ "$DEMO_DATA_TEMPLATE" != "$DEMO_DATA_DIR" ] && [[ $DEMO_DATA_TEMPLATE != "$DEMO_DATA_DIR"/* ]] || refuse "the data template lies inside the data dir"
  [ -d "$DEMO_DATA_TEMPLATE" ] || refuse "DEMO_DATA_TEMPLATE '$DEMO_DATA_TEMPLATE' is not a directory"
fi

case "$DEMO_DATA_TEMPLATE" in *"/../"*|*"/./"*|*/..|*/.) refuse "DEMO_DATA_TEMPLATE '$DEMO_DATA_TEMPLATE' has dot segments" ;; esac

# the reset role's password never goes on a command line: a pgpass file, not the URL
[[ $DEMO_ADMIN_URL =~ ^postgres(ql)?://[^/@]*:[^/@]*@ ]] && refuse "DEMO_ADMIN_URL carries a password; put it in a 0600 file named by DEMO_PGPASSFILE instead"
if [ -n "$DEMO_PGPASSFILE" ]; then
  [ -f "$DEMO_PGPASSFILE" ] || refuse "DEMO_PGPASSFILE '$DEMO_PGPASSFILE' is not a file"
  [ "$(stat -c %a "$DEMO_PGPASSFILE")" = 600 ] || refuse "DEMO_PGPASSFILE '$DEMO_PGPASSFILE' must have mode 600"
  export PGPASSFILE=$DEMO_PGPASSFILE
fi

# the demo service's own config must name the same database and data dir
[ -r "$DEMO_APP_ENV" ] || refuse "cannot read the demo service env $DEMO_APP_ENV"
envval() { sed -n "s/^[[:space:]]*$1=//p" "$DEMO_APP_ENV" | tail -1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }
app_url=$(envval DATABASE_URL); app_data=$(envval HATAMOTO_DATA)
app_db=${app_url##*/}; app_db=${app_db%%\?*}
[ "$app_db" = "$DEMO_DB" ] || refuse "the demo service uses database '$app_db', the reset config says '$DEMO_DB'"
app_hostport=${app_url#*@}; app_hostport=${app_hostport%%/*}
[ "${app_hostport##*:}" = "$DEMO_PG_PORT" ] || refuse "the demo service connects to port '${app_hostport##*:}', the reset config says the demo cluster is on $DEMO_PG_PORT"
[ "${app_data%/}" = "${DEMO_DATA_DIR%/}" ] || refuse "the demo service uses data dir '$app_data', the reset config says '$DEMO_DATA_DIR'"

sql() { "$PSQL" "$DEMO_ADMIN_URL" -X -v ON_ERROR_STOP=1 -At "$@"; }
# the same connection to another database (the URL's path is the database)
sqldb() { local db=$1; shift; "$PSQL" "$(sed -E "s#^([^/]*//[^/]*)/[^?]*#\1/$db#" <<<"$DEMO_ADMIN_URL")" -X -v ON_ERROR_STOP=1 -At "$@"; }

# ---- free space first: nothing here needs Postgres ----------------------------------------------------
if [ "$DRY" = 0 ]; then
  exec 9>"${DEMO_LOCK:-/run/hatamoto-demo-reset.lock}"
  flock -n 9 || refuse "another reset is running"
fi
CURRENT="stop $DEMO_UNIT"; step "stop $DEMO_UNIT"
[ "$DRY" = 1 ] || "$SYSTEMCTL" stop "$DEMO_UNIT" 9>&-
CURRENT="free space"; step "delete the ballast $DEMO_BALLAST and empty $DEMO_DATA_DIR (keeping the directory and its marker), before any Postgres step"
[ "$DRY" = 1 ] || {
  rm -f -- "$DEMO_BALLAST"
  find "$DEMO_DATA_DIR" -mindepth 1 -maxdepth 1 ! -name .hatamoto-demo -exec rm -rf -- {} +
}
# A full disk can stop the demo's Postgres (a WAL write that fails is a PANIC) and keep it from
# restarting; with room freed, start it. Only when it is not answering at all (not on a bad password).
if ! err=$(sql -c "select 1" 2>&1 >/dev/null) && grep -qiE 'connection refused|no such file or directory|server closed the connection|starting up|not yet accepting' <<<"$err"; then
  CURRENT="start $DEMO_PG_UNIT"; step "the demo's Postgres does not answer: start $DEMO_PG_UNIT now that there is room (up to 60 s)"
  if [ "$DRY" = 0 ]; then
    "$SYSTEMCTL" start "$DEMO_PG_UNIT" 9>&- || true
    for _ in $(seq 1 30); do sql -c "select 1" >/dev/null 2>&1 && break; sleep 2; done
  fi
fi
sql -c "select 1" >/dev/null || refuse "cannot connect with DEMO_ADMIN_URL"
# the cluster behind the connection must be the demo's own, not the main site's
cluster_port=$(sql -c "show port") cluster_dir=$(sql -c "show data_directory") cluster_name=$(sql -c "show cluster_name")
[ "$cluster_port" = "$DEMO_PG_PORT" ] || refuse "the connected cluster listens on port $cluster_port, DEMO_PG_PORT is $DEMO_PG_PORT"
[ "$(realpath -m -- "$cluster_dir")" = "$(realpath -m -- "$DEMO_PG_DATA")" ] || refuse "the connected cluster's data directory is '$cluster_dir', DEMO_PG_DATA is '$DEMO_PG_DATA': this is not the demo's cluster"
[ "$cluster_name" = hatamoto-demo ] || refuse "the connected cluster is named '$cluster_name', not hatamoto-demo"
comment=$(sql -c "select coalesce(shobj_description(oid,'pg_database'),'') from pg_database where datname='$DEMO_DB'")
exists=$(sql -c "select count(*) from pg_database where datname='$DEMO_DB'")
if [ "$exists" = 1 ] && [ "$comment" != "hatamoto-demo" ]; then
  refuse "database $DEMO_DB exists but is not commented 'hatamoto-demo': it is not the demo database"
fi
[ "$(sql -c "select count(*) from pg_database where datname='$DEMO_TEMPLATE_DB'")" = 1 ] || refuse "template database $DEMO_TEMPLATE_DB does not exist"
[ "$(sql -c "select coalesce(shobj_description(oid,'pg_database'),'') from pg_database where datname='$DEMO_TEMPLATE_DB'")" = "hatamoto-demo-template" ] \
  || refuse "template database $DEMO_TEMPLATE_DB is not commented 'hatamoto-demo-template'"
if [ "$exists" = 1 ]; then
  say "before: cluster $(du -sh "$DEMO_PG_DATA" 2>/dev/null | cut -f1), database $DEMO_DB is $(sql -c "select pg_size_pretty(pg_database_size('$DEMO_DB'))") , data dir $(du -sh "$DEMO_DATA_DIR" 2>/dev/null | cut -f1)"
fi
say "checks passed: database $DEMO_DB from $DEMO_TEMPLATE_DB in the demo cluster $cluster_dir (port $cluster_port), data dir $DEMO_DATA_DIR, unit $DEMO_UNIT"

# ---- the reset ------------------------------------------------------------------------
recover() { # a failure after the stop must not leave the demo down when it can still be started
  say "FAILED at: ${CURRENT:-?}"
  if [ "$(sql -c "select count(*) from pg_database where datname='$DEMO_DB'" 2>/dev/null || echo 0)" = 1 ]; then
    say "starting the service on whatever state is there"; "$SYSTEMCTL" start "$DEMO_UNIT" 9>&- || true
  fi
}
[ "$DRY" = 1 ] || trap recover ERR

# Plugin roles are cluster-wide: a plugin a visitor installed leaves its role behind when the
# database is dropped, and installing that plugin again would then fail. Drop the roles of THIS
# database (hm_p_<its id>_*) whose plugin the template does not have, through the plugin-role
# admin function (it refuses any role this database did not create).
CURRENT="plugin roles"; step "drop the plugin roles created since the template (hm_p_<id>_* not in $DEMO_TEMPLATE_DB)"
id8=; orphans=
if [ "$exists" = 1 ]; then
  id8=$(sqldb "$DEMO_DB" -c "select left(id,8) from hatamoto_instance" 2>/dev/null || true)
  tpl8=$(sqldb "$DEMO_TEMPLATE_DB" -c "select left(id,8) from hatamoto_instance" 2>/dev/null || true)
  if [[ $id8 =~ ^[0-9a-f]{8}$ && $id8 == "$tpl8" ]]; then
    keep=$(sqldb "$DEMO_TEMPLATE_DB" -c "select replace(name,'-','_') from hatamoto_packages where kind='plugin'" 2>/dev/null || true)
    for suffix in $(sqldb "$DEMO_DB" -c "select substr(rolname, 15) from pg_roles where rolname like 'hm\\_p\\_${id8}\\_%'" 2>/dev/null || true); do
      [[ $suffix =~ ^[a-z][a-z0-9_]{0,40}$ ]] || continue
      grep -qxF -- "$suffix" <<<"$keep" || orphans="$orphans $suffix"
    done
  else
    say "note: the database ids of $DEMO_DB and $DEMO_TEMPLATE_DB differ or cannot be read; plugin roles are left alone"
  fi
fi
for suffix in $orphans; do
  role=hm_p_${id8}_${suffix}
  say "dropping plugin role $role (not in the template)"
  [ "$DRY" = 1 ] || sqldb "$DEMO_DB" -c "begin; set local role $role; drop schema if exists plugin_$suffix cascade; reset role; commit;" \
      -c "revoke connect on database $DEMO_DB from $role" -c "select hatamoto_admin.drop_plugin_role('$suffix')" >/dev/null \
    || say "note: could not drop plugin role $role (left for the owner)"
done

CURRENT="drop $DEMO_DB"; step "disconnect everyone from $DEMO_DB and drop it"
[ "$DRY" = 1 ] || {
  sql -c "select pg_terminate_backend(pid) from pg_stat_activity where datname='$DEMO_DB' and pid <> pg_backend_pid()" >/dev/null
  sql -c "DROP DATABASE IF EXISTS $DEMO_DB"
}
CURRENT="create $DEMO_DB"; step "create $DEMO_DB from template $DEMO_TEMPLATE_DB (owner $DEMO_OWNER_ROLE)"
[ "$DRY" = 1 ] || {
  sql -c "CREATE DATABASE $DEMO_DB TEMPLATE $DEMO_TEMPLATE_DB OWNER $DEMO_OWNER_ROLE"
  sql -c "COMMENT ON DATABASE $DEMO_DB IS 'hatamoto-demo'"
}

if [ -n "$DEMO_DATA_TEMPLATE" ]; then
  CURRENT="restore data"; step "copy $DEMO_DATA_TEMPLATE into $DEMO_DATA_DIR and give it to $DEMO_USER"
  [ "$DRY" = 1 ] || {
    cp -a "$DEMO_DATA_TEMPLATE"/. "$DEMO_DATA_DIR"/
    chown -R "$DEMO_USER": "$DEMO_DATA_DIR" 2>/dev/null || say "note: could not chown to $DEMO_USER (not root?)"
  }
fi

CURRENT="ballast"; step "recreate the ${DEMO_BALLAST_MB} MB ballast file $DEMO_BALLAST"
[ "$DRY" = 1 ] || [ "$DEMO_BALLAST_MB" = 0 ] || {
  fallocate -l "${DEMO_BALLAST_MB}M" "$DEMO_BALLAST" && chmod 0600 "$DEMO_BALLAST" || say "note: could not create the ballast $DEMO_BALLAST"
}

CURRENT="start $DEMO_UNIT"; step "start $DEMO_UNIT"
[ "$DRY" = 1 ] || "$SYSTEMCTL" start "$DEMO_UNIT" 9>&-

CURRENT="health check"; step "wait for $DEMO_HEALTH_URL (up to 90 s)"
if [ "$DRY" = 0 ]; then
  for _ in $(seq 1 45); do
    "$CURL" -fsS -o /dev/null --max-time 3 "$DEMO_HEALTH_URL" 2>/dev/null && { say "healthy: the demo is reset"; trap - ERR; exit 0; }
    sleep 2
  done
  false # not healthy: recover() reports it and the unit shows failed
fi
say "dry run: nothing was changed"
