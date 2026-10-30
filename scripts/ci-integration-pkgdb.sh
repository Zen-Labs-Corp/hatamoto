#!/usr/bin/env bash
# Run every integration test package in parallel, each on its own copy of the
# migrated template database, so packages cannot collide on shared tables.
# Run from backend/ with DATABASE_URL pointing at the template database.
#
# Exit codes: 0 all packages passed, 1 a package failed, 2 setup failed.
# Env: JOBS (max concurrent packages, default nproc),
#      PKGDB_PKGS (space separated package list, local checks only),
#      PKGDB_DRY_RUN (create the copies, print the plan, run no tests).
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL must point at the template database}"
tmpl=hatamoto
admin=${DATABASE_URL/\/$tmpl\?/\/postgres\?}
if [[ $admin == "$DATABASE_URL" ]]; then
  echo "cannot derive an admin DSN from DATABASE_URL" >&2
  exit 2
fi
command -v psql >/dev/null || { echo "psql not found" >&2; exit 2; }

jobs_max=${JOBS:-$(nproc)}
if [[ -n ${PKGDB_PKGS:-} ]]; then
  read -r -a pkgs <<<"$PKGDB_PKGS"
else
  mapfile -t pkgs < <(go list -tags=integration -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)
fi
if ((${#pkgs[@]} == 0)); then
  echo "no test packages found" >&2
  exit 2
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# All copies first, serially, from the postgres database: the template stays idle.
dsns=()
for i in "${!pkgs[@]}"; do
  ok=0
  for _ in 1 2 3; do
    if psql "$admin" -v ON_ERROR_STOP=1 -qc "CREATE DATABASE hm_pkg_$i TEMPLATE $tmpl"; then
      ok=1
      break
    fi
    sleep 1
  done
  if ((ok == 0)); then
    echo "could not create hm_pkg_$i" >&2
    exit 2
  fi
  dsns[i]=${DATABASE_URL/\/$tmpl\?/\/hm_pkg_$i\?}
done
echo "created hm_pkg_0 .. hm_pkg_$((${#pkgs[@]} - 1)) from $tmpl"

if [[ -n ${PKGDB_DRY_RUN:-} ]]; then
  for i in "${!pkgs[@]}"; do
    echo "would run ${pkgs[i]} on hm_pkg_$i (DATABASE_URL=HATAMOTO_TEST_DB_URL=${dsns[i]})"
  done
  exit 0
fi

for i in "${!pkgs[@]}"; do
  while (($(jobs -rp | wc -l) >= jobs_max)); do
    wait -n || true
  done
  (
    rc=0
    DATABASE_URL=${dsns[i]} HATAMOTO_TEST_DB_URL=${dsns[i]} \
      go test -tags=integration -timeout 30m "${pkgs[i]}" >"$tmp/$i.log" 2>&1 || rc=$?
    echo "$rc" >"$tmp/$i.rc"
  ) &
done
wait

failed=0
for i in "${!pkgs[@]}"; do
  rc=$(cat "$tmp/$i.rc" 2>/dev/null || echo 1)
  if [[ $rc == 0 ]]; then
    echo "=== ${pkgs[i]} (db hm_pkg_$i): ok"
    tail -n 1 "$tmp/$i.log"
  else
    failed=1
    echo "=== ${pkgs[i]} (db hm_pkg_$i): FAIL"
    cat "$tmp/$i.log"
  fi
done
exit "$failed"
