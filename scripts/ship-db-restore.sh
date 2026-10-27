#!/usr/bin/env bash
# ship-db-restore.sh - restore a pg_dump -Fc backup by swapping databases.
#
# `pg_restore --clean --single-transaction` into the live DB fails on
# partitioned tables ("cannot drop inherited constraint ..."). Instead the dump
# is restored into a fresh database, then renamed into place; the failed
# database is kept for forensics.
#
# Usage (run on the DB host, as root or any user able to `sudo -u postgres`):
#     ship-db-restore.sh <db> <dump-file> <timestamp>
#
# Env (all optional, so the test can run it against a scratch cluster):
#   PGBIN       directory with psql/pg_restore/createdb (default: from PATH)
#   PG_AS       command prefix to act as the DB superuser (default "sudo -u postgres";
#               set to "" to run as the current user)
#   API_STOP    default "systemctl stop hatamoto-api". The helper never starts the
#               API: the caller restores the old binary first, then starts it.
#   CONFIG_ENV  env file with DATABASE_URL; when set, <db> must be its database
#   PGHOST/PGPORT/PGUSER are honoured by the pg tools as usual.
#
# Ownership and object grants come from the dump (no --no-owner / --no-acl).
# Database-level state that a dump does not carry is copied from the live DB to
# the new one before the swap: owner, encoding/locale, connection limit, the
# database ACL, ALTER DATABASE/ALTER ROLE ... IN DATABASE settings.
# The live DB is untouched until the restore has fully succeeded; the two
# renames happen in one transaction.

set -Eeuo pipefail

DB=${1:?db}; DUMP=${2:?dump}; TS=${3:?timestamp}
PGBIN=${PGBIN:+$PGBIN/}
PG_AS=${PG_AS-sudo -u postgres}
API_STOP=${API_STOP-systemctl stop hatamoto-api}
NEW="${DB}_restore_$TS"
OLD="${DB}_failed_$TS"

# shellcheck disable=SC2086
pg() { $PG_AS "${PGBIN}$1" "${@:2}"; }
sql() { pg psql -X -v ON_ERROR_STOP=1 -d postgres -Atc "$1"; }
drop_new() { sql "DROP DATABASE IF EXISTS \"$NEW\"" >/dev/null || echo "restore: could not drop $NEW; drop it by hand" >&2; }

if [[ -n "${CONFIG_ENV:-}" ]]; then
  url=$(set -a; . "$CONFIG_ENV"; printf '%s' "${DATABASE_URL:-}")
  url=${url%%\?*}
  [[ "${url##*/}" == "$DB" ]] \
    || { echo "restore: $DB is not the database in $CONFIG_ENV DATABASE_URL; refusing" >&2; exit 1; }
fi
[[ -r "$DUMP" ]] || { echo "restore: dump $DUMP not readable" >&2; exit 1; }

# owner|encoding|collate|ctype|locale provider|icu locale|connection limit
INFO=$(sql "SELECT pg_catalog.pg_get_userbyid(datdba), pg_encoding_to_char(encoding), datcollate, datctype,
              datlocprovider, coalesce(to_jsonb(d)->>'datlocale', to_jsonb(d)->>'daticulocale', ''), datconnlimit
            FROM pg_database d WHERE datname = '$DB'")
[[ -n "$INFO" ]] || { echo "restore: database $DB not found" >&2; exit 1; }
IFS='|' read -r OWNER ENC COLLATE CTYPE PROVIDER ICU CONNLIMIT <<< "$INFO"

# Database-level ACL and settings of the live DB, as SQL aimed at $NEW.
META=$(sql "
  SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', '$NEW')
  FROM pg_database WHERE datname = '$DB' AND datacl IS NOT NULL
  UNION ALL
  SELECT format('REVOKE ALL ON DATABASE %I FROM %I', '$NEW', '$OWNER')
  FROM pg_database WHERE datname = '$DB' AND datacl IS NOT NULL
  UNION ALL
  SELECT format('GRANT %s ON DATABASE %I TO %s%s', a.privilege_type, '$NEW',
                CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(pg_get_userbyid(a.grantee)) END,
                CASE WHEN a.is_grantable THEN ' WITH GRANT OPTION' ELSE '' END)
  FROM pg_database d, aclexplode(d.datacl) a WHERE d.datname = '$DB'
  UNION ALL
  SELECT format('ALTER %s SET %I TO %s',
                CASE WHEN s.setrole = 0 THEN format('DATABASE %I', '$NEW')
                     ELSE format('ROLE %I IN DATABASE %I', pg_get_userbyid(s.setrole), '$NEW') END,
                split_part(c, '=', 1),
                -- list GUCs are stored already quoted: replay them raw, not as one string
                CASE WHEN split_part(c, '=', 1) IN ('search_path', 'temp_tablespaces', 'session_preload_libraries', 'local_preload_libraries')
                     THEN substr(c, strpos(c, '=') + 1) ELSE quote_literal(substr(c, strpos(c, '=') + 1)) END)
  FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase, unnest(s.setconfig) c
  WHERE d.datname = '$DB'
  UNION ALL
  SELECT format('ALTER DATABASE %I CONNECTION LIMIT %s', '$NEW', $CONNLIMIT)")

echo "restore: creating $NEW (owner $OWNER, encoding $ENC, collate $COLLATE)"
LOCALE_ARGS=(--lc-collate="$COLLATE" --lc-ctype="$CTYPE")
[[ "$PROVIDER" != i ]] || LOCALE_ARGS+=(--locale-provider=icu --icu-locale="$ICU")
pg createdb -T template0 -O "$OWNER" -E "$ENC" "${LOCALE_ARGS[@]}" "$NEW"
if ! pg pg_restore --exit-on-error -d "$NEW" < "$DUMP"; then
  echo "restore: pg_restore into $NEW failed; live database $DB untouched" >&2
  drop_new
  exit 1
fi
if ! sed 's/$/;/' <<< "$META" | pg psql -X -v ON_ERROR_STOP=1 -1 -d postgres -f - >/dev/null; then
  echo "restore: copying database ACL/settings to $NEW failed; live database $DB untouched" >&2
  drop_new
  exit 1
fi

echo "restore: swapping $NEW into place"
if ! $API_STOP; then
  echo "restore: could not stop the API; live database $DB untouched" >&2
  drop_new
  exit 1
fi
# One transaction: either both renames happen or neither does.
if ! pg psql -X -v ON_ERROR_STOP=1 -1 -d postgres >/dev/null \
    -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$DB','$NEW') AND pid <> pg_backend_pid()" \
    -c "ALTER DATABASE \"$DB\" RENAME TO \"$OLD\"" \
    -c "ALTER DATABASE \"$NEW\" RENAME TO \"$DB\""; then
  echo "restore: swap FAILED and was rolled back; $DB is still the live database" >&2
  drop_new
  exit 1
fi
echo "restore: done. previous (failed) database kept as: $OLD"
LEFT=$(sql "SELECT string_agg(datname, ' ' ORDER BY datname) FROM pg_database WHERE datname LIKE '${DB}\\_failed\\_%'")
echo "restore: WARNING failed databases still on this cluster (drop when no longer needed): $LEFT"
