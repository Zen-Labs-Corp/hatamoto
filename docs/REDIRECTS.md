# Redirects

In the "Admin legacy URLs" table the left column is the old address, literal. `<admin>` stands for the install's private admin address (`hatamoto admin-url`); tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`, real installs get a random address.

Site-specific redirects (retired pages, renamed slugs, archive consolidation)
are data, not code: they live in `hatamoto_redirects` and are managed in
Admin → Settings → Redirects (`<admin>/settings/redirects`). Core ships
only generic URL handling (trailing slash) plus, when the site setting `legacy_wp_redirects` is on, the built-in
WordPress families below.

## Resolution order

1. Exact `source_path` (retried with the trailing slash toggled).
2. Wildcard rules (below), most specific first.
3. When `legacy_wp_redirects` is on: the WordPress families (dated permalinks
   `/YYYY/MM/slug`, `/author/*`, `/category/*`, `/tag/*`, `/page/N`, `/feed`,
   stray `wp-*` paths) redirect to the blog archive or post.

Matching is case-sensitive. `redirects/explain` shows every step for a path.

## Redirects Hatamoto writes itself

When an update changes the slug or the parent of a published entity, the old
URL of the entity and of every published row below it answers 301 to the new
one (`source_tag = entity-move`; see `URL_PATTERNS.md`). Earlier rules that
pointed at an old path are repointed so a visitor never follows a chain, and a
rule whose source became a live URL again is removed. The same happens when a
term (tag, category, custom kind) is renamed or gets another parent: its archive
path and those of its sub-terms redirect to the new ones. If a page is later
created or published at a path an automatic redirect still holds, the redirect
is deactivated so the page wins; rules an owner wrote are never touched. They
are ordinary rows: edit or delete them in the redirects screen. One row is kept
per old path and none is pruned automatically. Dated URLs
(`{year}/{month}/{day}`) do not get one when `published_at` changes.

## `legacy_wp_redirects`

A site setting (`GET/PATCH /api/v1/settings/site`, default **off**). Turn it on
for a site imported from WordPress so old links keep working; leave it off
everywhere else, where those paths should simply 404.

## Retired path prefixes

The locale-removal migration (`202610151230_kill_locales.sql`) adds, for every
language prefix the site used to have, the two rules

| source_path | target_url |
| --- | --- |
| `/<prefix>` | `/` |
| `/<prefix>/**` | `/$1` |

with status 301 and `source_tag = v1-09-kill-locales`, so old `/<prefix>/page`
links land on `/page`. A prefix that already has a redirect row or a page of
that name is skipped. The same two-rule pattern retires any other path
prefix.

## Wildcard source paths

A `source_path` containing `*` is a wildcard rule:

| Segment | Matches |
| --- | --- |
| `*` | exactly one path segment |
| `**` (last segment only) | zero or more remaining segments |

Wildcards capture in order and the target references them as `$1`, `$2`, ….
`**` captures the remaining segments joined with `/` (empty when none).
Exact rules always beat wildcard rules; among wildcards the one with more
literal segments wins. Wildcards must be whole segments (`/a/x*` is rejected).

Safety rules, enforced on create/update (HTTP 400) and again when the cache
loads from the database, so rows inserted by SQL are covered too:

- The first segment must be literal: `/**`, `/*` and `/*/**` are rejected,
  unless the target is an absolute `http(s)://` URL.
- A relative target must not match its own source (`/docs/**` → `/docs/archive`
  would loop). `$n` is substituted with placeholders to test.

A row that fails these checks at load is skipped, logged once per id
(`ignoring invalid wildcard redirect`, never the full row) and counted in a
`skipped invalid wildcard redirects` warning with `count=N`. Requests with
method `HEAD` are redirected like `GET`. The visitor's query string is kept
unless the target carries its own.

Examples:

| source_path | target_url | `/seo/foo` becomes |
| --- | --- | --- |
| `/seo/*` | `/guides/$1` | `/guides/foo` |
| `/old/**` | `/` | `/` |

## Admin legacy URLs

The admin was reorganised in v1-23a (Settings with its own sections) and again when Theme became a primary item (Appearance and Menus moved from Settings to `<admin>/theme`). Every old admin URL answers **301** for GET and HEAD and **308** for every other method (POST, PUT, PATCH, DELETE: the method and body are sent again to the new path), with the query string kept and the fragment dropped. A table in `backend/internal/admin/legacy_redirects.go` drives it, matching whole path segments (`/admin/settings/theme` moves, `/admin/settings/themes` does not), and it runs in the `/admin` fallback: after the real routes, which win, and before the session and CSRF checks, so a signed-out visitor to an old URL with no real route gets the 301, not a login page with `next=` set to the old path. The one exception is `GET /admin/analytics`, a real route (see below). These are admin routes, not rows in `hatamoto_redirects`; they do not appear in the redirects screen. A path that is a real route today is never redirected.

| Old | New |
| --- | --- |
| `/admin/analytics` (exact), `/admin/analytics/agents...` | `<admin>/analytics/crawlers` (`?tab`, `from`, `to`, `agent`, `group`, `page`, `npage` kept) |
| `POST /admin/analytics/agents/robots` | `<admin>/analytics/crawlers/robots` |
| `/admin/settings/site`, `homepage`, `contact`, `notifications` | `<admin>/settings/general` |
| `/admin/settings/branding` | `<admin>/theme/colours` (Theme → Colours and fonts) |
| `/admin/settings/api` | `<admin>/settings/api-keys` (Settings → API, since v1-26) |
| `/admin/settings/api/secret`, `/admin/settings/api/{id}/...` | `<admin>/settings/api-keys/secret`, `<admin>/settings/api-keys/{id}/...` |
| `/admin/settings/plugins...` (including `POST fetch`, `upload`, `install`, `unsigned`, and `{name}/grant`, `settings`, `enable`, `disable`, `uninstall`) | `<admin>/plugins...` (same suffixes) |
| `/admin/settings/theme...` (including `POST theme/install`) | `<admin>/theme/themes...` (same suffixes); `/admin/settings/theme/design` goes to `<admin>/theme/colours` |
| `/admin/settings/themes...` (fetch, install, `{name}/activate`, `settings`, `uninstall`) | `<admin>/theme/themes...` (same suffixes) |
| `/admin/settings/themes/design` | `<admin>/theme/colours` |
| `/admin/settings/themes/tokens...` (including `POST reset`) | `<admin>/theme/design-system...` |
| `/admin/settings/themes/presets...` (including `new`, `{name}/delete`) | `<admin>/theme/presets...` |
| `/admin/settings/themes/parts` | `<admin>/theme/parts` |
| `/admin/settings/sections...`, `/admin/sections...` | `<admin>/theme/templates...` |
| `/admin/settings/menus...`, `/admin/menus...` | `<admin>/theme/menus...` |
| `/admin/settings/appearance` (exact) | `<admin>/theme` |
| `/admin/settings/menus-forms` (exact) | `<admin>/theme/menus` |
| `/admin/schemas...` | `<admin>/settings/types...` |
| `/admin/taxonomies...` | `<admin>/settings/taxonomies...` |
| `/admin/forms...` (builder) | `<admin>/settings/forms...` |
| `/admin/forms/{id}/submissions` | `<admin>/settings/submissions?form={id}` |
| `/admin/forms/{id}/submissions/{sid}`, and its `status`, `email`, `delete` | `<admin>/settings/submissions/{sid}`, and the same suffixes |
| `/admin/forms/{id}/submissions/bulk`, `.../export.csv` | `<admin>/settings/submissions/bulk?form={id}`, `<admin>/settings/submissions/export.csv?form={id}` |
| `/admin/redirects...` (rules, 404 log, explain) | `<admin>/settings/redirects...` (same suffixes) |
| `/admin/linking...` | `<admin>/settings/linking...` |
| `/admin/users...` | `<admin>/settings/users...` |
| `/admin/import` | `<admin>/settings/import` |
| `/admin/import/wordpress...` | `<admin>/settings/import/wordpress...` |
| `/admin/media` (that exact path only) | `<admin>/settings/media` |

`<admin>/settings` itself is not in the table: it is a real route that answers **303** to the first Settings section the role can reach (and `?section=appearance`, `menus` or `menus-forms`, which moved, answer 303 to the Theme section that replaced them), `<admin>/theme` does the same for Theme, and the section URLs (`<admin>/settings/content-model` and the others) answer 302 to their first tab. `GET <admin>/analytics` is likewise a real route: signed in it answers **303** to `<admin>/analytics/crawlers`, signed out it goes to the login page with `next=<admin>/analytics`. Only the other methods reach the table row (308).

Two plugin addresses are real handlers, not rows of the table above (so they need no legacy rule): `GET <admin>/plugins/{name}` answers 302 to `<admin>/plugins/{name}/settings`, and `GET <admin>/plugins/{name}/ui` (the plugin page before it moved onto the Settings tab) answers 301 to `<admin>/plugins/{name}/settings`, for owner and editor alike. Only GET is redirected; other methods on `/ui` fall through to the 404 page.

These do not move: `<admin>/entities...`, `<admin>/analytics/referrals`, `<admin>/analytics/data/*.json`, `<admin>/media/upload`, `<admin>/media/picker`, `<admin>/media/{id}` (and its delete page), `<admin>/pick/*`, `<admin>/preview/theme/{name}`, `POST <admin>/plugins/{name}/bridge`, `<admin>/profile`, `POST <admin>/theme`, `POST <admin>/logout`, login, forgot, reset, accept, `<admin>/setup/*` and `<admin>/assets/*`. `<admin>` and `<admin>/` still answer 303 to the landing page, `<admin>/entities`.

**Accepted caveat.** A 301 makes a browser repeat a POST as a GET, so a form that was open on an old URL before the upgrade and is then submitted lands on the new page and does nothing. Nothing is saved and nothing is lost; reload the page and submit again. Bookmarks, emailed links (a new submission email now links to `<admin>/settings/submissions/{sid}`) and plugin links to old paths keep working.
