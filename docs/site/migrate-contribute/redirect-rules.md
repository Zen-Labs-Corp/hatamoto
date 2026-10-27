---
title: Redirect rules
nav_title: Redirect rules
description: How the site decides where an old address goes: rule order, wildcards, safety checks, automatic redirects and the WordPress setting.
---

This page is the full reference for redirects: where rules live, the order the server checks them in, wildcard syntax, the safety checks, the redirects Hatamoto writes itself, the `legacy_wp_redirects` setting and the built-in forwards for old admin addresses. To add your first redirects after a move, follow [Keep old links working after a move](redirects-after-migration.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Redirects are data, not code. They live in the `hatamoto_redirects` table and are managed in the admin at Settings, Redirects (`<admin>/settings/redirects`, owners and editors) or with `hatamoto redirect list|create|delete`. Hatamoto ships only generic URL handling (a trailing slash is ignored) plus, when you switch it on, the built-in WordPress families described below. Everything site-specific, such as retired pages, renamed slugs and archive consolidation, is a row you control.

A rule has these fields:

| Field | Admin label | Values | Meaning |
|---|---|---|---|
| `source_path` | From | a path, optionally with `*` or `**` | The address visitors ask for. |
| `target_url` | To | a path or an absolute `http(s)://` URL | Where they are sent. A wildcard rule may use `$1`, `$2`. |
| `status_code` | Type | `301` (default), `302`, `307`, `308` | 301 moved permanently, 302 moved temporarily, 307 temporary and keeps the request method, 308 permanent and keeps the request method. |
| `is_active` | State | On, Off | A rule that is off is kept but not used. |
| `hit_count` | Hits | number | How often the rule matched. |
| `source_tag` | none | empty, `entity-move` | `entity-move` marks a rule Hatamoto wrote itself (see below). |

## Resolution order

For every public page request, before it looks for a live page and before it serves a cached copy, Hatamoto tries these steps in order. The first match wins, so a rule you write wins over a live page at the same path:

1. An exact rule on the request path. A trailing slash on the request is removed first, so the rule `/old` catches `/old` and `/old/`. Write source paths without a trailing slash.
2. Wildcard rules, most specific first.
3. When the site setting `legacy_wp_redirects` is on, the WordPress families (see [The WordPress families](#the-wordpress-families)).

Matching is case-sensitive. Requests with method `HEAD` are redirected like `GET`. A wildcard match keeps the visitor's query string, adding it with `&` when the target already has one; an exact rule and the WordPress families send the target as written. A status other than 301, 302, 307 or 308 is sent as 301.

## The admin screens

Settings, Redirects has three tabs:

| Tab | Address | What it does |
|---|---|---|
| Redirect rules | `<admin>/settings/redirects` | The list with search, bulk Delete, Turn off and Turn on, and **New redirect**. A wildcard rule is marked "(wildcard)". The **Test a URL** box opens the Explain tab for a path. |
| 404 log | `<admin>/settings/redirects/404` | Addresses visitors requested that did not exist, with hits, last seen and referrer. Each row offers Create redirect (the From field filled in), Explain, Ignore and Remove. State is Open, Has a redirect or Ignored. |
| Explain a URL | `<admin>/settings/redirects/explain` | Every step for one path, whether it matched, the rule and the answer. It is the fastest way to find out why an old URL lands where it does. |

The Explain tab also tries the path with the trailing slash toggled, and shows a last step that looks up a single-segment path as the slug of a published post. That last step is for information only; the public site does not use it. The same data is available through the API as `GET /api/v1/redirects/explain?path=/old`.

A new or edited rule takes effect at once when it is saved in the admin.

## Command line

```bash
hatamoto redirect list --search /old
hatamoto redirect create --from /old --to /new --status 301
hatamoto redirect delete <id>
```

| Command | Flags | Meaning |
|---|---|---|
| `redirect list` | `--search Q` | Up to 200 rules whose source or target contains `Q`, one per line: id, status, source and target. |
| `redirect create` | `--from PATH --to URL [--status 301]` | Create a rule. `--from` and `--to` are required; `--status` is 301, 302, 307 or 308 (default 301). Prints the rule as JSON. |
| `redirect delete` | `<id>` | Delete a rule by id. |

The command needs `DATABASE_URL` and applies the same validation as the admin. It cannot clear the running server's copy of the rules, which the server re-reads every 60 seconds, so a new rule can take up to about a minute to appear. Use the admin screen when you want immediate effect.

## Wildcard source paths

A `source_path` containing `*` is a wildcard rule.

| Segment | Matches |
|---|---|
| `*` | Exactly one path segment. |
| `**` (last segment only) | Zero or more remaining segments. |

Wildcards capture in order, and the target references them as `$1`, `$2` and so on. `**` captures the remaining segments joined with `/`, and the capture is empty when there are none. Exact rules always beat wildcard rules. Among wildcards, the one with more literal segments wins; then the longer source; then the alphabetical order. A wildcard must be a whole segment, so `/a/x*` is rejected.

| source_path | target_url | `/seo/foo` becomes |
|---|---|---|
| `/seo/*` | `/guides/$1` | `/guides/foo` |
| `/old/**` | `/` | `/` |

### Retiring a path prefix

To retire a whole prefix, add two rules, both with status 301:

| source_path | target_url |
|---|---|
| `/old-prefix` | `/` |
| `/old-prefix/**` | `/$1` |

`/old-prefix/page` then lands on `/page`. Sites that once served language prefixes already received these two rules for each prefix from the migration that removed languages, and a prefix that already had a redirect row or a page of that name was skipped. See [One site, one language](../getting-started/one-site-one-language.md).

### Safety rules

These are enforced when you create or update a rule (HTTP 400 from the API, a field error in the admin) and again when the server loads the rules from the database, so rows inserted with SQL are covered too.

- The first segment must be literal. `/**`, `/*` and `/*/**` are rejected unless the target is an absolute `http(s)://` URL.
- A relative target must not match its own source. For example `/docs/**` to `/docs/archive` would loop and is refused.
- An expanded target must be a safe location: a path that starts with a single `/`, or an absolute URL the rule itself names. A match that would produce anything else, such as `//host` or a backslash, is dropped and the path is not redirected.

A stored row that fails these checks at load time is skipped. It is logged once per id as `ignoring invalid wildcard redirect` (never the whole row), and a `skipped invalid wildcard redirects` warning carries the count.

## Redirects Hatamoto writes itself

When an update changes the slug or the parent of a published entity, the old URL of that entity and of every published row below it answers 301 to the new one. These rows carry `source_tag = entity-move`. The same happens when a term (tag, category or custom kind) is renamed or gets another parent: its archive path and those of its sub-terms redirect to the new ones. When a content type's URL pattern changes, the old address of each of its published entries gets a 301 too, unless that address already has a redirect row or the new address already forwards back to it.

Hatamoto also keeps the table tidy:

- Earlier rules that pointed at an old path are repointed, so a visitor never follows a chain.
- A rule whose source became a live URL again is removed.
- If a page is later created or published at a path an automatic redirect still holds, the redirect is turned off so the page wins. The row stays as a record.
- Rules an owner or editor wrote are never touched.

They are ordinary rows, so you can edit or delete them in the redirects screen. One row is kept per old path and none is pruned automatically. Dated URLs (`{year}/{month}/{day}`) do not get a redirect when `published_at` changes. See [URL patterns](../ai-seo/url-patterns.md) for how addresses are built.

The [WordPress import](wordpress-import-reference.md) writes rows of its own for every old URL that moved, and removes the ones a later run no longer needs. It never overwrites a rule you wrote.

## The WordPress families

`legacy_wp_redirects` is a site setting, read and written by owners through `GET` and `PATCH /api/v1/settings/site`. Its default is **off**, and it has no switch in the admin. Turn it on for a site imported from WordPress:

```bash
curl -X PATCH https://example.com/api/v1/settings/site \
  -H "Authorization: Bearer <owner-session-token>" \
  -H 'Content-Type: application/json' \
  -d '{"legacy_wp_redirects": true}'
```

`<owner-session-token>` is the `session_token` that `POST /api/v1/auth/login` returns when an owner signs in; a cookie session does not work for the API unless `ENABLE_COOKIE_AUTH` is set. `GET` on the same address shows the current value.

While it is on, these old WordPress address shapes answer 301:

| Old address | Goes to |
|---|---|
| `/page/N` (root pagination) | The post archive |
| `/YYYY/MM/slug` and `/YYYY/MM/DD/slug` (dated permalinks) | The post with that slug, at its current address |
| `/feed` and `/comments/feed` | The post archive |
| `/wp-json`, `/wp-admin`, `/wp-content`, `/wp-login.php` and anything below them | The post archive |

The post archive and post addresses come from the `post` content type's URL settings (`/blog` and `/blog/<slug>` when nothing else is set). Author, category and tag archives are not part of this list: they are ordinary routes of the site. Leave the setting off on a site that never ran WordPress, where these paths should answer 404. The WordPress import writes explicit 301 rows wherever the new URL differs, so this switch is a safety net for the long tail of old links, not a replacement for the audit.

## Old admin addresses

The admin was reorganised so that the main navigation has five primary items and Settings and Theme have their own sections. Every old admin URL answers **301** for `GET` and `HEAD`, and **308** for every other method, so a form posted to an old address keeps its method and body. The query string is kept. A table in `backend/internal/admin/legacy_redirects.go` drives it and matches whole path segments; the most specific entry wins. It runs only when no real admin route matches, and before the session and security checks, so a signed-out visitor to an old bookmark gets the redirect rather than a login page pointing at the old path. The table lists each old path as it was written under the old fixed address `/admin`; on your install the same old paths answer after your own admin address, so `<admin>/redirects...` goes to `<admin>/settings/redirects...`.

| Old | New |
|---|---|
| `/admin/redirects...` | `<admin>/settings/redirects...` |
| `/admin/menus...`, `/admin/settings/menus...` | `<admin>/theme/menus...` |
| `/admin/schemas...` | `<admin>/settings/types...` |
| `/admin/taxonomies...` | `<admin>/settings/taxonomies...` |
| `/admin/users...` | `<admin>/settings/users...` |
| `/admin/forms...` | `<admin>/settings/forms...` (`<admin>/forms/<id>/submissions...` goes to `<admin>/settings/submissions...?form=<id>`) |
| `/admin/linking...` | `<admin>/settings/linking...` |
| `/admin/sections...`, `/admin/settings/sections...` | `<admin>/theme/templates...` |
| `/admin/settings/themes...`, `/admin/settings/theme...` | `<admin>/theme/themes...` (design to `colours`, tokens to `design-system`, presets and parts keep their names) |
| `/admin/settings/plugins...` | `<admin>/plugins...` |
| `/admin/settings/api...` | `<admin>/settings/api-keys...` |
| `/admin/import/wordpress...` | `<admin>/settings/import/wordpress...` |
| `/admin/import`, `/admin/media` | `<admin>/settings/import`, `<admin>/settings/media` |
| `/admin/settings/site`, `homepage`, `contact`, `notifications`, `email` | `<admin>/settings/general` |
| `/admin/settings/branding` | `<admin>/theme/colours` |
| `/admin/settings/appearance` | `<admin>/theme` |
| `/admin/analytics`, `/admin/analytics/agents...` | `<admin>/analytics/crawlers...` |

These are admin routes, not rows in `hatamoto_redirects`, so they never appear in the redirects screen. A path with `..`, a backslash or a control character is never redirected.

## Limits and errors

| Message | Meaning |
|---|---|
| `invalid wildcard source_path: use whole-segment * or a trailing **` (admin: "Wildcards must be whole segments") | A `*` inside a segment, or `**` that is not last. |
| `unsafe wildcard rule: needs a literal first segment and a target that does not match its own source` (admin: "This wildcard rule is unsafe") | See [Safety rules](#safety-rules). |
| `redirect with this source path already exists` (admin: "A redirect from this address already exists") | Each source path has one rule. Edit the existing one. |
| `invalid redirect status code` (admin: "Choose a redirect type") | Use 301, 302, 307 or 308. |
| `--from and --to are required` | The command needs both. |
| `ignoring invalid wildcard redirect` (log) | A stored wildcard row failed the checks and is skipped. |
