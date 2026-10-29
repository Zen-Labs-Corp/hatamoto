# Changelog page (`/changelog`)

One inline section per released version, newest first, each a CMS entity of the
schema slugged `release`. The layout (`archive-release.html`, default theme) is
generic core and carries no site data; the schema, the entries and the footer
link are site data created by the steps below. Entries are created by
`go run ./tools/changelog` (see [CHANGELOG_RELEASE_HOOKS.md](CHANGELOG_RELEASE_HOOKS.md))
and stay editable by hand: a re-run never overwrites notes that were edited.

Quick path: `scripts/changelog-bootstrap.sh` does steps 1 and 2. It needs
`HATAMOTO_URL` and `HATAMOTO_SESSION_TOKEN` (an owner `session_token` from
`POST /api/v1/auth/login`; sent on curl stdin, never argv); `DRY_RUN=1` previews.

Owner session token, kept out of argv and shell history (the password is read
from a prompt, the JSON goes to curl on stdin, the Bearer header on curl stdin):

```bash
read -r  -p 'Owner email: '    HM_EMAIL
read -rs -p 'Owner password: ' HM_PASSWORD; echo
export HM_EMAIL HM_PASSWORD
HATAMOTO_SESSION_TOKEN=$(jq -n '{email: env.HM_EMAIL, password: env.HM_PASSWORD}' |
  curl -fsS -X POST "$HATAMOTO_URL/api/v1/auth/login" \
    -H 'Content-Type: application/json' --data-binary @- | jq -r .session_token)
unset HM_EMAIL HM_PASSWORD; export HATAMOTO_SESSION_TOKEN

scripts/changelog-bootstrap.sh

# Revoke the session afterwards.
printf 'Authorization: Bearer %s\n' "$HATAMOTO_SESSION_TOKEN" |
  curl -fsS -X POST "$HATAMOTO_URL/api/v1/auth/logout" -H @-
unset HATAMOTO_SESSION_TOKEN
```

Keep the token in the environment only. Do not write it to
`.hatamoto-changelog.env`, which holds the long-lived API key (keep that file
`chmod 600`).

## 1. Schema `release` (owner only)

Settings > Schemas > New, slug `release`. Or `POST /api/v1/schemas` (owner
session; API keys cannot reach `/schemas` or `/menus`).

Schema JSON:

```json
{
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
}
```

Settings JSON:

```json
{
  "url_pattern": "/changelog/{slug}",
  "archive_path": "/changelog",
  "archive_title": "Changelog",
  "archive_description": "Every Hatamoto release, newest first.",
  "sitemap": {
    "exclude": true
  }
}
```

Notes are string arrays, not `content`: list items drop `content`, and arrays
are rangeable in the layout. `version_sort` = major*1e6 + minor*1e3 + patch, so
the layout sorts numerically (`-attr:version_sort`), not by text.
`generated_hash` is the generator's edit detector; leave it alone.
`pending_publish` is true only between the generator creating a draft and
publishing it. A later sync publishes a draft only while it is true, so an
entry you archive, schedule or unpublish stays as you left it (its notes and
metadata are still updated by sync, its status never is).

### Why `url_pattern` is set (spike finding, P10)

`archive_path` alone is valid and routes: `ValidateSchemaSettings` only requires
it to start with `/`, and `public_resolve.go` serves `/changelog` as the
`archive-release` layout for any schema config with that `archive_path`.
**But a schema with no `url_pattern` is not "no per-entry pages":**
`url_registry.go` substitutes the default pattern `/{slug}`, so each entry would
be a public page at the site root (`/v0-27-0`) and join the `misc` sitemap
group. Setting `url_pattern` to `/changelog/{slug}` keeps those pages under
`/changelog/`, and `sitemap.exclude` keeps them out of the sitemap. This is a
deliberate deviation from the plan's "no `url_pattern`"; the per-version pages
are an unlinked side effect, not a feature (the layout anchors `#vX.Y.Z`).
Entity slugs come from the name (`v0.27.0` -> `v0-27-0`), so `v0.2.1` and
`v0.2.10` do not collide.

### Why array items are `format: textarea` (spike finding)

The admin entity form (`internal/admin/schemaform/render.go`, `kind`) renders
`array` of short plain `string` as a **tags** field: one comma-separated input
that `Decode` splits on commas and newlines and de-duplicates. A PR title such
as `Add a, b (#1)` would be split into two items on the first admin save.
Declaring items with `"format": "textarea"` (or `maxLength` > 200) makes the
field a **repeater**, one textarea row per item, add/remove/reorder, no
splitting. This is why the schema above sets it.

## 2. Footer menu item

Settings > Menus: use the menu whose location is `footer` (create one if none),
add an item: type Custom link, label `Changelog`, URL `/changelog`. Each
top-level item renders as its own footer column (`partials/footer.html`).
REST shapes (`internal/menu/model.go`): `POST /api/v1/menus`
`{"name","slug","location":"footer"}`, `POST /api/v1/menus/{id}/items`
`{"label","link_type":"custom","url":"/changelog"}`.

## 3. API key and credentials for the generator

Settings > API keys: create a key with scope `write:entities` (it also needs
read for the lookup, i.e. `read:entities`). Keys may only reach
`/api/v1/entities` routes. Put the credentials in `.hatamoto-changelog.env`
(gitignored; env vars win):

```
HATAMOTO_URL=https://your-site.example
HATAMOTO_API_KEY=<key>
HATAMOTO_CHANGELOG_SCHEMA_ID=<schema uuid>
```

The schema UUID is printed by the bootstrap script, or
`GET /api/v1/schemas` (owner) / the URL of the schema's admin page.

## 4. Spike record: verified vs inferred

Verified by running code (throwaway tests, deleted):

- The schema JSON and settings above pass `Validator.ValidateSchema`,
  `ValidateSchemaSettingsForWrite` and `ValidateEditorSetting("release", ...)`;
  settings with only `archive_path` also pass.
- `schemaform.Render` of an array of plain strings gives a **tags** input
  (2 `<input>`, 0 `<textarea>`); with `"format":"textarea"` items it gives a
  **repeater** (2 `<textarea>` rows). `Decode` for tags splits on `,` / newline.

Verified by reading code (no instance was run):

- `/changelog` resolves to the archive for a schema with `archive_path`
  (`public_resolve.go`, after entity and section-page matches).
- No `url_pattern` -> default `/{slug}` (`url_registry.go`).
- `/api/v1/schemas` and `/api/v1/menus` are owner/session routes; a Bearer
  session token from `POST /api/v1/auth/login` works (`session_token` in the
  body); API keys cannot reach them (`apikey_route_auth.go`).
- Footer renders `FooterNav` from the `footer`-location menu; none is seeded.

Inferred, to confirm on the live site (TODO-9): the per-entry page at
`/changelog/v0-27-0` renders with the default single layout.

Verified in `sitemap_builder.go` (`planSitemap` skips an excluded config
entirely: "no entries and no index page"): `sitemap.exclude` also drops the
`/changelog` archive from the sitemap. Accepted: the alternative is to drop
`exclude`, which would list a thin per-entry page for every release. The archive
is reachable from the footer link; add `/changelog` to a hand-made sitemap or
menu entry if it must be indexed.

## 5. Runbook after merge

1. `scripts/ship.sh <PR>` (deploys the embedded layout).
2. Caddy is not touched by ship: on the server `sudo deploy/apply-caddy.sh --dry-run` (or `sudo DRY_RUN=1 deploy/apply-caddy.sh`),
   review, then `sudo deploy/apply-caddy.sh`.
3. `scripts/changelog-bootstrap.sh` (or steps 1-2 by hand), then create the key (step 3).
4. Backfill from the live channel manifests:
   ```
   cd backend
   go run ./tools/changelog refresh --dry-run
   go run ./tools/changelog refresh            # a second run is all noop
   ```
   `refresh` creates an entry for every tag that is live on some channel, sets
   its badge to the furthest channel whose ramp has started (canary, edge,
   stable; a scheduled ramp counts from its first step's start time) and its
   `yanked` mark from the manifests. Notes follow the usual rule. `backfill
   --channel v=chan,...` still exists for setting channels by hand.
5. Check: `curl -s $SITE/changelog | grep -o 'id="v[0-9.]*"'` lists newest first,
   and the home page footer contains `href="/changelog"`.
6. Install the release hooks: [CHANGELOG_RELEASE_HOOKS.md](CHANGELOG_RELEASE_HOOKS.md).
