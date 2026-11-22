#!/usr/bin/env bash
# Seed for review-edit-flow.mjs: an author, one published post of theirs
# ("QA Review Post"), and the site's review switch on. The post is created before the switch is
# turned on, so it is published directly; every change the author makes afterwards waits for the
# editor. Throwaway QA databases only. Run by run-local.sh when review-edit-flow.mjs is in the list.
# Needs the owner and the posts seed.sh creates, and psql (in PG_BIN or on PATH).
# Usage: QA_USE_DATABASE_URL=1 DATABASE_URL=... HATAMOTO_BIN=... scripts/admin-qa/review-edit-seed.sh
set -Eeuo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the QA database}"
. "$(dirname "${BASH_SOURCE[0]}")/qa-env.sh"
qa_guard_database_url
BIN=${HATAMOTO_BIN:-hatamoto}
PSQL=psql
[ -n "${PG_BIN:-}" ] && [ -x "$PG_BIN/psql" ] && PSQL=$PG_BIN/psql
PASS=${QA_PASSWORD:-qa-password-123}
AUTHOR=${QA_AUTHOR_EMAIL:-author@qa.test}

printf '%s' "$PASS" | "$BIN" user create --email "$AUTHOR" --name "QA Author" --role author --password-stdin 2>&1 | tail -1
"$BIN" entity create --schema post --name "QA Review Post" --publish --as "$AUTHOR" >/dev/null
"$PSQL" "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "UPDATE hatamoto_site_config SET review_enabled = true"
echo "review seed: $AUTHOR owns a published post, review is on"
