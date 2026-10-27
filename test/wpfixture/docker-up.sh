#!/usr/bin/env bash
# Brings up the dockerised WordPress fixture, seeds it and prints the
# WP_FIXTURE_* variables the importer integration tests read.
#
#   test/wpfixture/docker-up.sh
#
# The site answers on http://127.0.0.1:${WPFIXTURE_PORT:-8089}. The variables are
# also written to out/wpfixture.env.host (source it); the WXR export is out/wpfixture.wxr.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
mkdir -p out
chmod 777 out # written by the www-data user of the wp-cli container
rm -f out/wpfixture.env out/wpfixture.wxr
compose=(docker compose -f docker-compose.yml)
"${compose[@]}" up -d db wordpress
if ! timeout 900 "${compose[@]}" --profile seed run --rm wpcli; then
  echo "wpfixture: seeding failed; container logs follow" >&2
  "${compose[@]}" ps -a >&2 || true
  "${compose[@]}" logs --no-color --tail=100 >&2 || true
  exit 1
fi
# Variables as the host sees them (the container wrote its own view of the path).
{
  grep -v '^WP_FIXTURE_WXR=' out/wpfixture.env
  echo "WP_FIXTURE_WXR=$PWD/out/wpfixture.wxr"
} > out/wpfixture.env.host
sed 's/^\(WP_FIXTURE_APP_PASSWORD=\).*/\1***/' out/wpfixture.env.host # not the password: CI logs are public
