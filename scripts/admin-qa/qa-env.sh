# Sourced by run-local.sh and seed.sh.

# qa_find_pg_bin prints the directory with initdb, pg_ctl and createdb, or nothing.
# Order: PG_BIN, initdb on PATH, postgres on PATH, the newest /usr/lib/postgresql/*/bin.
qa_find_pg_bin() {
  local d
  if [ -n "${PG_BIN:-}" ]; then
    [ -x "$PG_BIN/initdb" ] && echo "$PG_BIN"
    return 0
  fi
  if command -v initdb >/dev/null; then dirname "$(command -v initdb)"; return 0; fi
  if command -v postgres >/dev/null; then
    d=$(dirname "$(readlink -f "$(command -v postgres)")")
    [ -x "$d/initdb" ] && echo "$d"
    return 0
  fi
  d=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1)
  [ -x "${d:-/nonexistent}/initdb" ] && echo "$d"
  return 0
}

# qa_guard_database_url refuses to seed a database the caller did not opt into:
# DATABASE_URL must be allowed with QA_USE_DATABASE_URL=1 and point at this machine.
# seed.sh creates an owner with a public password and adds posts on every run.
qa_guard_database_url() {
  [ -n "${DATABASE_URL:-}" ] || return 0
  if [ "${QA_USE_DATABASE_URL:-}" != 1 ]; then
    echo "refusing to seed the database in DATABASE_URL: set QA_USE_DATABASE_URL=1 to allow it (or unset DATABASE_URL for a throwaway cluster)" >&2
    return 1
  fi
  # libpq lets a query parameter override the authority, so refuse those outright.
  case "?${DATABASE_URL#*\?}" in
    *[?\&]host=*|*[?\&]hostaddr=*|*[?\&]service=*|*%68ost=*) echo "refusing a DATABASE_URL with a host=, hostaddr= or service= override" >&2; return 1 ;;
  esac
  local host=${DATABASE_URL#*://}
  host=${host#*@}; host=${host%%[/?]*}
  case "$host" in
    localhost|localhost:*|127.0.0.1|127.0.0.1:*|"[::1]"|"[::1]:"*) ;;
    *) echo "refusing to seed a non-local database (host '$host')" >&2; return 1 ;;
  esac
}
