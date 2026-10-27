#!/usr/bin/env bash
# changelog-bootstrap.sh - create the `release` schema and the footer
# "Changelog" link behind the /changelog page. Idempotent: every object is
# looked up first and only created when missing. See docs/CHANGELOG_SITE.md.
#
# Usage:
#     scripts/changelog-bootstrap.sh
#     DRY_RUN=1 scripts/changelog-bootstrap.sh   # GETs run, writes are printed as `DRY: ...`
#
# Env (HATAMOTO_URL and HATAMOTO_SESSION_TOKEN may also be set in the
# gitignored .hatamoto-changelog.env, here or in the main checkout; env wins):
#   HATAMOTO_URL            site base URL, no trailing slash (required)
#   HATAMOTO_SESSION_TOKEN  OWNER session token, the `session_token` field of
#                           POST /api/v1/auth/login (required; schemas and
#                           menus are owner-only, API keys cannot reach them).
#                           Sent as a Bearer header on curl stdin, never argv.
#                           Get it without the password touching argv or history,
#                           and revoke it after (keep it out of the env file):
#                             read -rs -p 'Owner password: ' HM_PASSWORD; export HM_PASSWORD
#                             read -r -p 'Owner email: ' HM_EMAIL; export HM_EMAIL
#                             HATAMOTO_SESSION_TOKEN=$(jq -n '{email: env.HM_EMAIL, password: env.HM_PASSWORD}' |
#                               curl -fsS -X POST "$HATAMOTO_URL/api/v1/auth/login" \
#                               -H 'Content-Type: application/json' --data-binary @- | jq -r .session_token)
#                             unset HM_EMAIL HM_PASSWORD
#                           afterwards: POST /api/v1/auth/logout with the Bearer
#                           header (full recipe in docs/CHANGELOG_SITE.md).
#   DRY_RUN                 1 = do not write
#
# Requires curl >= 7.55 and jq.

set -euo pipefail

DRY_RUN=${DRY_RUN:-0}
die() { echo "changelog-bootstrap: $*" >&2; exit "${2:-1}"; }

# KEY=VALUE lines of the first existing env file; the environment wins.
load_env_file() {
    local f k v
    for f in ./.hatamoto-changelog.env "$(git rev-parse --git-common-dir 2>/dev/null || echo .git)/../.hatamoto-changelog.env"; do
        [[ -f "$f" ]] || continue
        while IFS='=' read -r k v; do
            case "$k" in
                HATAMOTO_URL|HATAMOTO_SESSION_TOKEN) [[ -n "${!k:-}" ]] || export "$k=$v" ;;
            esac
        done < "$f"
        return 0
    done
}
load_env_file

command -v jq >/dev/null || die "jq is required" 2
: "${HATAMOTO_URL:?set HATAMOTO_URL (env or .hatamoto-changelog.env)}"
: "${HATAMOTO_SESSION_TOKEN:?set HATAMOTO_SESSION_TOKEN to an owner session token (see header)}"
BASE=${HATAMOTO_URL%/}
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT

# api METHOD PATH [JSON]: the token travels on stdin as a header file.
api() {
    local method=$1 path=$2 body=${3:-}
    local args=(-fsS -X "$method" -H @- -H 'Content-Type: application/json' "$BASE$path")
    if [[ -n "$body" ]]; then printf '%s' "$body" > "$tmp"; args+=(--data-binary "@$tmp"); fi
    printf 'Authorization: Bearer %s\n' "$HATAMOTO_SESSION_TOKEN" | curl "${args[@]}"
}
# write METHOD PATH JSON: like api, but only prints under DRY_RUN=1.
write() {
    if [[ "$DRY_RUN" == 1 ]]; then echo "DRY: $1 $2 $3" >&2; echo '{}'; else api "$@"; fi
}

SCHEMA_JSON='{
  "type": "object",
  "required": [
    "version"
  ],
  "properties": {
    "version": {
      "type": "string",
      "title": "Version"
    },
    "version_sort": {
      "type": "number",
      "title": "Sort key"
    },
    "released_at": {
      "type": "string",
      "title": "Released (YYYY-MM-DD)"
    },
    "channel": {
      "type": "string",
      "enum": [
        "canary",
        "edge",
        "stable"
      ]
    },
    "yanked": {
      "type": "boolean",
      "title": "Yanked"
    },
    "summary": {
      "type": "string",
      "format": "textarea"
    },
    "features": {
      "type": "array",
      "items": {
        "type": "string",
        "format": "textarea"
      }
    },
    "fixes": {
      "type": "array",
      "items": {
        "type": "string",
        "format": "textarea"
      }
    },
    "docs": {
      "type": "array",
      "items": {
        "type": "string",
        "format": "textarea"
      }
    },
    "chores": {
      "type": "array",
      "items": {
        "type": "string",
        "format": "textarea"
      }
    },
    "generated_hash": {
      "type": "string",
      "title": "Generated hash (do not edit)"
    },
    "pending_publish": {
      "type": "boolean",
      "title": "Pending publish (do not edit)"
    }
  }
}'

SETTINGS_JSON='{
  "url_pattern": "/changelog/{slug}",
  "archive_path": "/changelog",
  "archive_title": "Changelog",
  "archive_description": "Every Hatamoto release, newest first.",
  "sitemap": {
    "exclude": true
  }
}'

# 1. schema
schema_id=$(api GET /api/v1/schemas | jq -r '.schemas[] | select(.slug=="release") | .id' | head -n1)
if [[ -n "$schema_id" ]]; then
    echo "schema release exists: $schema_id"
else
    body=$(jq -nc --argjson schema "$SCHEMA_JSON" --argjson settings "$SETTINGS_JSON" \
        '{name:"Release",slug:"release",description:"One entry per released version (/changelog)",schema:$schema,settings:$settings}')
    schema_id=$(write POST /api/v1/schemas "$body" | jq -r '.id // empty')
    echo "schema release created: ${schema_id:-<dry-run>}"
fi

# 2. footer menu + item
menu_id=$(api GET /api/v1/menus | jq -r '[.menus[] | select(.location=="footer")][0].id // empty')
if [[ -z "$menu_id" ]]; then
    menu_id=$(write POST /api/v1/menus '{"name":"Footer","slug":"footer","location":"footer"}' | jq -r '.id // empty')
    echo "footer menu created: ${menu_id:-<dry-run>}"
else
    echo "footer menu exists: $menu_id"
fi
has_item=0
if [[ -n "$menu_id" ]] && api GET "/api/v1/menus/$menu_id" | jq -e '[.items[]? | recurse(.children[]?) | select(.url=="/changelog")] | length > 0' >/dev/null; then
    has_item=1
fi
if [[ "$has_item" == 1 ]]; then
    echo "footer item /changelog exists"
else
    write POST "/api/v1/menus/${menu_id:-MENU_ID}/items" '{"label":"Changelog","link_type":"custom","url":"/changelog"}' >/dev/null
    echo "footer item /changelog created"
fi

echo
echo "Set in .hatamoto-changelog.env: HATAMOTO_CHANGELOG_SCHEMA_ID=${schema_id:-<schema uuid>}"
echo "Then create an API key (write:entities, read:entities) and set HATAMOTO_API_KEY."
