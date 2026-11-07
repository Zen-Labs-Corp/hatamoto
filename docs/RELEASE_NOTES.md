# Release notes

## Unreleased: Form settings tab

- **Keep submissions.** The Settings tab's "Keep submissions in the admin" switch writes `store_off` (absent means keep). Off stores no answers: they are only emailed and sent to hooks. The old `store_submissions` key is no longer read, so every existing form and every form created through the API keeps storing, as before; a form whose owner had switched it off on an earlier release keeps storing until the owner saves it from the Settings tab. If email is not configured or the form has no recipient, the answers are kept anyway and a warning is logged. A send that fails later is still lost.
- **Nothing about the visitor.** With keeping off, the row holds only its id, status and time: no IP address, browser, page, referrer or attribution. The notification email still names the page and, unless "Do not record IP addresses" is on, the address. The per-form rate limit still applies.
- **Migration.** None. Rollback floor: unchanged; an older release ignores the new keys and drops them the next time it saves that form.

## Unreleased: Theme strings

- **Declare and use.** A theme can list its words under `strings` in `theme.json` and print them with `{{t . "key"}}`; `{{t . "key" n}}` replaces `{n}` in the text. A partial called with `dict` receives them as `"Strings" .Strings`. A key the theme did not declare prints as the key. A malformed `strings` block fails `hatamoto theme check` and packing; at runtime its invalid entries are ignored.
- **Translate.** Owners edit them per locale in Theme > Strings (`<admin>/theme/strings`). A blank text falls back to the default locale's text, then to the theme's own. Saving is recorded in the audit log as `theme.strings`.
- **Default theme.** Its words (search, pager, navigation labels and more) now come from theme strings, with the same default text, so existing sites look the same until an owner changes one.
- **Not covered.** The consent banner, widget text and `hm.js` are not theme strings.
- **Migration.** None. Rollback floor: unchanged.

## Unreleased: Form logic and new field types

- **Types.** Forms gain `heading`, `paragraph` and `divider` (layout only: nothing is submitted, stored, exported or emailed), `hidden` (value resolved by the server from a query parameter, the page, the referrer or a fixed value; the page script sends the page URL and the visitor's referrer, so without JavaScript those three store nothing) and `consent` (always required, stored as `Yes`). Fields can be half or a third wide.
- **Logic.** A field can show only when another field `is`, `is_not`, `contains`, is `filled` or is `empty`. A hidden field is never required and its value is dropped. A rule to itself, to a missing field or in a cycle is refused on save, in the admin, REST and MCP.
- **Save endpoint.** `PUT <admin>/forms/{id}/definition` saves a whole form as JSON, with the last-saved time as its version: a stale save gets 409 with the current version, an invalid definition 422 with a message per field. The legacy editor, REST and MCP stay last write wins.
- **Public form.** The Form widget shows and hides fields live and, on a form without captcha, shows errors next to each field without a page reload. Without JavaScript it posts as before.
- **Email, CSV and API.** A field with `in_email: false` is left out of the notification email; an email field with `block_free_email` refuses free-provider addresses. CSV export skips layout fields. The public form JSON also carries the new keys (`in_email`, static hidden values), which are not secret.
- **Migration.** None. Rollback floor: unchanged; an older release ignores the new keys and drops them the next time it saves that form.

## Unreleased: idle plugin instances are closed after 5 minutes

- **Idle eviction.** A plugin's pooled instances that have not been used for 5 minutes are closed, leaving one warm instance per plugin (the most recently used one). A burst of traffic that grew the pool no longer holds that memory until restart; when traffic returns, instances start again on demand. The window is fixed.
- **Newest instance first.** The pool reuses the most recently returned plugin instance, so steady low traffic lets extra instances idle out and shrinks the pool to one.
- **Memory tab.** Settings, Performance, Memory states the policy and shows "Closed since start: N", the number of instances closed this way since the server started.
- **Migration.** None. Rollback floor: none.

## Unreleased: encoding/json v1 is banned; JSON v2 across the backend

The backend, the admin, MCP and REST output and the plugin SDK now use the JSON v2 packages (`encoding/json/v2`). The sections below on empty lists, the plugin host and the platform encoder give the detail.

- **Empty lists.** Nil slices and maps are written as `[]` and `{}` in REST, admin and MCP output; they were `null`. Clients that tested for `null` should test for an empty list.
- **Stricter requests.** Request bodies with a repeated member name, invalid UTF-8 or data after the JSON value are rejected. A member whose name differs only in case (`Title` for `title`) is usually ignored, not rejected: the request succeeds but that field has no effect, so send each member name exactly as documented (some use camelCase, e.g. `nodeId`). A few endpoints, such as the author endpoint, reject unknown or wrong-case members with 400 instead.
- **Webhooks and plugins.** Webhook bodies and their HMACs, and the bytes between host and plugin, are unchanged byte for byte.
- **SDK.** `hatamoto.dev/sdk` 0.19.0 needs Go 1.27 to build a plugin; plugins built for earlier SDKs still load.
- **Stored JSON.** The upgrade step normalises known mixed-case keys in stored JSON. `hatamoto doctor json` (read-only) reports any stored row that would fail strict reading or still has a case-variant key; it exits 1 if a row fails and 2 if it cannot connect. A row that still fails strict reading errors loudly instead of being half read.
- **Contributors.** The build fails on a new `encoding/json` (v1) import outside `backend/migrations/`.
- **Migration.** The upgrade step `202611051000_normalise_stored_json_keys` rewrites the keys. Rollback floor: unchanged; the previous release reads what this one writes (`[]` where it wrote `null`).

## Unreleased: CSV import and export

- **Export.** **Export CSV** on a content type's list downloads its entries as CSV with the columns you pick: fixed columns plus one `attr:<path>` column per attribute. Cells that could run as a spreadsheet formula are prefixed with `'`.
- **Import.** **Settings, Import, Import a CSV file instead** loads a CSV file into one content type in three steps: upload, map the columns, dry run and import. The Key column makes a second import update instead of add; an export writes `id:<id>` as the key of entries that were not imported, so any exported file imports again as updates. New entries are drafts unless the file gives a status. Updating by key changes only what the file carries: an empty cell or an unmapped column keeps the stored value, the default status applies to new rows only, and a JSON cell replaces its object. Clearing a value is not possible through CSV; edit the entry in the admin.
- **Dry run.** `entity.Service.Import` takes `DryRun`: the whole path runs in a transaction that is rolled back, with no event, cache purge or stored row.
- **Limits.** 32 MiB, 50 000 rows, 500 rows per batch. `order` is not imported. The `entity.before_save` hook runs in a dry run.
- **Migration.** None. Rollback floor: none (imported rows carry `metadata.import_key` as for any import).

## Unreleased: Email protection

- **Encoded addresses.** Email addresses and `mailto:` links on public pages are now written as character references, so simple harvesters that read the visible page miss them while visitors still see and click them, with scripts off too. It is on by default, existing sites included. Code, scripts, comments, the head, feeds, plugin routes, `llms.txt`, the API and the editor preview keep the plain address. The organisation email in structured data (Organization or LocalBusiness) also stays readable in the page source; leave that field empty if you do not want it published. Turn it off under Settings > Privacy > Protect email addresses.

## Unreleased: Public link prefetch

- **Prefetch on hover.** Public pages carry one `speculationrules` block that prefetches same-site page links when a visitor hovers them, so the next page opens faster. Prefetch only, never prerender. It skips the admin address, non-page routes, logout-like paths, links with a query string and file downloads.
- **Theme switch.** A theme turns it off with `"prefetch": false` in `theme.json`. There is no admin setting. Preview, canvas and access-notice pages carry no block.
- **CSP.** The strict script policy now reads `script-src 'self' 'inline-speculation-rules'`; the keyword permits speculation rules only, never an executable inline script. The default policy is unchanged.
- **Privacy.** A custom admin address is never printed on public pages; only the legacy `/admin` is named.
- **Migration.** None. Rollback floor: unchanged.

## Unreleased: Expiring user access

- **End date.** Invites and users take an optional **Access ends on** date (end of that day in the site time zone). Once it passes, sign-in is refused, live sessions are refused on the next request and the API keys the person created stop working. Extending or clearing the date restores sign-in, not old sessions or keys.
- **Worker.** A new hourly job `user_access` revokes sessions and keys, writes a `user.access_ended` audit entry and mails the other owners three days before an end. It can be switched off with `WORKER_DISABLE_JOBS=user_access`; the request-time check does not depend on it.
- **API.** The JSON users endpoints accept `access_ends_at`, owners only.
- **Migration.** Two additive columns (`access_ends_at` on users and on user tokens, plus two bookkeeping columns on users). Rollback floor: unchanged; an older release ignores the columns, so users with an end date can sign in again.

## Unreleased: Popular content

- **Template func.** `popular "post" 5 30` lists the published entities of a schema with the most pageviews over the last 7, 30 or 90 days (any other value or none means 30; limit 1 to 50), the newest filling any gap so the list is always full.
- **Loop order.** The Loop widget's Order accepts `popular_7`, `popular_30` and `popular_90` (Most viewed). It lists the same entities as the func and works with the term, author and attribute filters and with paging (the first 100 rows are served).
- **Data.** Views are the pageview beacons in the daily traffic rollup, summed per entity over UTC days including today. Visitors without script and bots are not counted; only the 100 busiest pages of each day are kept, so a small page on a busy site can have no views. A trailing slash, a locale prefix and upper case count for the same entity; a slug with capitals or non-ASCII letters is never ranked. The list refreshes about once a minute, and no view count is shown on pages or in structured data.
- **Failures.** If the rollup cannot be read the list falls back to the newest entities and the page is served but not cached.
- **Migration.** None. Rollback floor: remove popular loop orders first, an older release refuses them.

## Unreleased: PDF widget

- **Widget.** A new **PDF** widget (Media group) shows a PDF from the media library in the browser's viewer, with a download link (the editor preview and sites with a live community script show the link only, as those pages cannot frame a file) and, when the library knows it, the file size. On a phone only the link shows. Height is 200 to 2000 pixels. No script runs on the page.
- **Same-site framing.** A PDF served from `/media/` now allows framing by your own pages (not by a page in a CSP sandbox) (`X-Frame-Options: SAMEORIGIN`, `frame-ancestors 'self'`); other sites still cannot frame it, and every other file keeps `DENY`.
- **Page policy.** The public page's `frame-src` adds `'self'` next to `https:`, so the frame also works on a site served over plain http. `object-src` stays `'none'`.
- **Picker.** The media picker takes a `type` parameter (`document` lists PDFs only; the default stays images), and a widget property can ask for it with `x-hm-accept`.
- **Migration.** None. Rollback floor: unchanged; an older release does not know the `pdf` kind and would refuse a page that uses it, so remove the widget before you roll back.

## Unreleased: Plugin host and SDK 0.19.0 on JSON v2

The plugin host and the Go SDK read and write plugin data with the JSON v2 packages. Bytes between host and plugin are unchanged: plugins built for earlier SDKs (`^0.5` to `^0.18`) load and run as before.

- **SDK needs Go 1.27.** `hatamoto.dev/sdk` 0.19.0 declares `go 1.27`; build plugins with Go 1.27 or newer.
- **Source compatible types.** `json.RawMessage` is an alias of `jsontext.Value` in Go 1.27, so SDK types that now use `jsontext.Value` need no change in plugin code.
- **Same bytes for your types.** The guest and the host marshal SDK types and the types you write (call arguments, results, run summaries) with the v1 behaviours pinned: `omitempty`, `time.Duration` as nanoseconds, byte arrays as number arrays, `MarshalJSON` and `MarshalText` methods, and repeated member names inside raw JSON. Your tags need no change. The guest reads your types back the same way (nanosecond durations, byte arrays, `,string`, names folded by case only, merging into a filled value); the host decodes only SDK types. The one difference left: a struct with two fields that share a JSON name now fails to marshal instead of dropping both.
- **Changeset payloads.** A changeset payload that cannot be encoded now returns `invalid_argument` instead of staging `{}`.
- **Lenient manifests.** A repeated member in a `plugin.json` list element loads, the last one wins.
- **Stricter settings validation.** Plugin settings validation rejects invalid UTF-8 that v1 replaced.
- **One manifest decoder.** Plugin manifest reads in the admin packages screen, the updater, the settings CLI and at boot use the same decoder as install.
- **Migration.** None. Rollback floor: unchanged.

## Unreleased: Admin branding

Settings > Branding: admin name, logo, accent colour and a Managed by line for the sign-in page and admin; colours that fail contrast are refused.

- **Where it shows.** The sign-in page and the admin sidebar and footer. The public site, its logo and theme, and the emails are unchanged.
- **Contrast.** The accent must reach 4.5:1 on both the white admin background and the tinted highlight. The dark theme gets a lighter shade of it, worked out so every pair still passes. Reset to default clears all of it.
- **Migration.** Five expand-only text columns on site config. Rollback floor: unchanged; an older binary ignores them and shows the default look.

## Unreleased: Additional CSS

- **Setting.** Theme > Additional CSS (owners only) takes the site's own CSS, up to 64 KB. It is checked on save (`@import`, `expression()`, `javascript:` URLs, `behavior` and `-moz-binding` are refused, with the line named) and served as its own fingerprinted stylesheet, linked after the theme's.
- **WordPress import.** The old site's custom CSS comes across into the setting; refused parts are kept as a comment and listed in the report.
- **Migration.** None: the CSS is stored in the existing site config. No new dependency.

## Unreleased: Jobs screen

Settings > Jobs (owners only) lists every scheduled job, core and plugin, with its schedule, last run, duration, result, last error and next run, and has a Run now button for the safe core jobs (never for jobs that send data out or for plugin jobs).

- **Stored last run.** Each job's last run is kept in the database, so a replica without the worker shows it too. No history, one row per job.
- **Job ids.** The fixed jobs now log under the same names `WORKER_DISABLE_JOBS` uses (`retention_purge`, `agent_verify` and so on, not the hyphenated forms).
- **Migration.** One new table `hatamoto_job_status` (expand-only). No new dependency. Rollback floor: an older release ignores the table.

## Unreleased: Version history limit

- **Setting.** Settings > Versions (owners only) sets how many earlier versions of each item are kept, 10 to 500. The daily prune reads it on every run.
- **Default raised from 20 to 50.** More history is kept; nothing extra is deleted on upgrade.
- **Live versions are kept.** A version that was live in the last 30 days is never pruned and does not count against the limit. The versions panel states the rule and marks those versions Live.
- **Migration.** Two expand-only changes (a `live_at` column on versions, one column on site config). Rollback floor: unchanged; an older binary ignores both and prunes to 20 again.

## Unreleased: Custom and self-hosted fonts

Owners can upload `.woff2` fonts or download a Google font once, then pick body and heading fonts under Theme > Colours and fonts. Public pages serve the fonts from the site with `font-display: swap` and preload the body font. Visitors never contact Google.

- **Limits.** 2 MB per file, 20 uploads, 60 font files in total.
- **Export and backup.** Fonts are stored in the database and travel with export, import and backup.
- **Reserved address.** `/fonts/...` is now served by the site, like `/preview` and `/theme`: `/fonts` can no longer be the admin address, and an entry or schema URL under `/fonts/` stops resolving. Move such content to another path before you upgrade. Only `/fonts/<64 hex>.woff2` skips the site-access gate; any other `/fonts/...` page follows the mode.
- **Migration.** One new table (expand-only). No new dependency, so docs/DEPENDENCIES.md is unchanged. Rollback floor: an older release ignores the table and the chosen family falls back to the system stack.

## Unreleased: Manual order and page tree

Pages, posts and terms can be put in a chosen order. Entities get a **Reorder** page (Move up, Move down, Indent, Outdent as plain forms); terms get the same buttons in Settings > Taxonomies. Agents use `entities_reorder` and `terms_reorder`, and `PATCH /entities/{id}` accepts `position`. Themes opt in with `order: "manual"` in a loop or `query`, and `terms "kind" 10 "manual"`.

- **Behaviour.** Reordering saves no version and keeps `updated_at`; a plugin's page stays the plugin's. Moving to another parent redirects the old address.
- **Migration.** Two additive columns (`sort_order`, default 0). Rollback floor: unchanged. An older binary ignores them.

## Unreleased: ads.txt and app-ads.txt

Settings > SEO has a new ads.txt tab (owners only). Write your `ads.txt` and `app-ads.txt` there and the site serves them at `/ads.txt` and `/app-ads.txt` as plain text. Every line is checked on save and a bad one is named ("Line 2: ..."). An empty file answers a plain 404, unless a Settings > Redirects rule has `/ads.txt` as its source: then the redirect applies. A saved file takes precedence over such a rule. The AdSense card in Settings > Tracking links to the tab and adds the AdSense line for the saved id.

- **Caching.** Served with `Cache-Control: public, max-age=60, s-maxage=60`.
- **Site access.** Like the sitemap, both files answer a plain 404 (503 with `Retry-After` in maintenance mode) while coming soon, maintenance or password mode is on, never the themed page.
- **Migration.** None. Rollback floor: unchanged. An older binary ignores the two new settings keys and `/ads.txt` returns the themed 404; an SEO save on the older binary drops them.
## Unreleased: Schedule an unpublish, share a draft

- **Unpublish at.** Pages, posts and other entries can be set to go back to draft at a chosen time, with an optional redirect from the old address. Publishing again turns the redirect off.
- **Share preview.** Make a private link to a draft that works for 1 hour to 30 days and can be revoked. A link stops working once the entry is published, archived or deleted. Search engines are told not to list it.
- **Migration.** Two new tables (expand-only). The previous release ignores them: scheduled unpublishes and preview links stop working until you upgrade again.

## Unreleased: Media library plus

The media library now accepts SVG, can replace a file in place, and groups files in folders.

- **Safe SVG.** SVG uploads are cleaned by an allowlist before they are stored (scripts, event handlers, `foreignObject`, external links and DOCTYPE are removed or refused). The upload says what was removed and refusals give a reason and line. SVGs are served with a locked-down policy and shown only through an image tag. This applies to the admin, REST, MCP, plugins, changesets and site import.
- **Replace file.** Replaces the bytes at the same id, file name and address, rebuilds the resized copies, refreshes the page cache and writes an audit row. Same kind and extension only; quota counts the size difference.
- **Folders.** One level, 1 to 60 letters, digits, spaces, hyphens or underscores. Library filter, bulk move, picker filter, `folder` on the REST media list and plugin host, and `folder` on MCP `media_upload`. Only new folder names are checked: a folder value already stored, for example a nested path set earlier through the REST API or a plugin, stays listed, filterable and movable, and the REST and plugin `folder` filter still accepts it.
- **Cache header change.** Media files are now served with `Cache-Control: public, max-age=86400, must-revalidate` and an ETag, so a replaced file shows within a day.
- **No migration.** No new dependency, so docs/DEPENDENCIES.md is unchanged.

**Rollback floor: unchanged.** An older release ignores folders it does not know and serves a stored SVG as a download instead of showing it, because it sniffs content differently.

## Unreleased: Backup retention

Settings > Backups has a new "Backups to keep" page, opened from the ⋯ menu next to "Back up now". Owners choose how many finished backups of each kind (manual, pre-update, scheduled) stay on disk, from 1 to 50. Lowering a count asks for confirmation first and says how many backups it deletes.

- **Higher defaults.** Without any setting the server now keeps 10 manual, 5 pre-update and 7 scheduled backups (it kept 5, 2 and 3). Backups take more disk space: check your free space before upgrading.
- **Environment variables still win.** `HATAMOTO_BACKUP_KEEP`, `HATAMOTO_BACKUP_KEEP_PRE_UPDATE` and `HATAMOTO_BACKUP_KEEP_SCHEDULED` override the form, and the page shows which one fixes a count. A value above 50 is treated as 50 and logged as a warning; zero, a negative number or text is ignored and logged.
- **Upgrade warning: values above 50.** An environment value above 50 is now treated as 50. If you have more than 50 backups of a kind, the first backup after updating (of any kind, including a scheduled one) deletes all but the newest 50 of that kind. Copy the older ones elsewhere first, or lower the value yourself before you update.
- **Where it is stored.** The counts live in `backups/retention.json` in the data directory. It is not part of a backup archive and a restore leaves it alone.

**Rollback note.** An older release ignores `retention.json` and keeps only 5 manual, 2 pre-update and 3 scheduled backups (or what its environment variables say). Its next backup removes the older ones, so a higher count you set here is lost on downgrade. Copy backups you want to keep elsewhere first.

## Unreleased: JSON responses use [] and {} for empty lists and maps

REST, admin and MCP responses now say `[]` or `{}` where they used to say `null` for an empty list or map, and request bodies are read more strictly.

- **Empty lists and maps.** A list or map with nothing in it is now `[]` or `{}`. Zero numbers, false flags and empty values that were left out are still left out.
- **Stricter requests.** A request body with a repeated key, data after the JSON value or invalid UTF-8 gets 400, and field names must match their case exactly.
- **Plugin bridge limit.** A plugin bridge body over the size limit now gets 413.
- **MCP text.** `<`, `>` and `&` are no longer escaped in MCP tool output text.
- **Command-line JSON.** `hatamoto ... --json` output prints `[]` and `{}` for empty lists and maps, where it printed `null`, and no longer escapes `<`, `>` and `&`. A script that tests for `null` needs updating.
- **Command-line attributes.** `hatamoto entity create --attrs` rejects a JSON object with a repeated key or invalid UTF-8.
- **JSON-LD checks.** The JSON-LD check (`ValidateJSONLD`, and JSON-LD sent through MCP) rejects a repeated key and data after the JSON value, and reports the block as invalid JSON.
- **Plugin bridge.** The admin plugin bridge rejects a repeated key, a field name in the wrong case, and data after the JSON value with 400.
- **Migration.** None.

**Rollback floor: unchanged.** The previous release reads `[]` and `{}` the same way.

## Unreleased: Content packages use the new JSON reader

Content, taxonomy, changeset and settings data, the page builder, imports and JSON-LD now use the same stricter JSON reading and writing as the API.

- **Empty lists and maps.** Empty lists and maps in entity, taxonomy, changeset and settings output, and in the rows saved from now on, are `[]` and `{}` instead of `null`. The previous release reads them fine.
- **Stricter files and requests.** Builder documents now reject only a repeated key and data after the JSON value (a field name in the wrong case still loads). The archive import manifest and header now reject only data after the JSON value. Menu and tag declarations reject a repeated key, an unknown key and data after the JSON value (key case is still ignored). Locale-conversion plans, taxonomy admin request bodies, the analytics tracking beacon (400 on trailing data), reusable schemas (repeated keys) and the WordPress SEO settings upload are checked more strictly as well.
- **Repeated keys and invalid UTF-8 in stored content.** A widget schema's `object` defaults and a section template's content that repeat a key or contain invalid UTF-8 are now rejected. A `theme.json` with invalid UTF-8 now fails to load.
- **JSON-LD.** Custom and plugin JSON-LD with a repeated name or invalid UTF-8 is dropped. If a plugin's site graph has invalid UTF-8, the page falls back to the core graph. JSON-LD still escapes `<` and U+2028.
- **Stored values.** No stored hash or fingerprint changes.
- **Migration.** None.

**Rollback floor: unchanged.** The previous release reads the new rows the same way.

## Unreleased: Platform JSON on the new encoder

Webhooks, packages, releases, backups, the updater, the supervisor, two-factor, forms, plugin runs and imports now use the same JSON reader and writer as the API. Everything that is signed, verified or sent to a receiver keeps its exact bytes.

- **Agents analytics.** The admin responses for series, top, never fetched, top paths and by status say `[]` or `{}` where they said `null` for an empty list or map.
- **Author create and update.** The body is read strictly. An unknown field, a field name in the wrong case, a repeated key or data after the JSON value gets 400 with `invalid JSON body`.
- **Robots policy.** The PUT body is read strictly in the same way, and a body over the size limit gets 413 where it got 400.
- **Forms.** Form fields, settings, submission data and metadata are stored as `[]` or `{}` instead of `null`. The previous release reads them fine.
- **Backups and supervisor.** The backup manifest and its sidecar file, and the supervisor `state.json`, write `[]` and `{}` for empty lists and maps. The previous release reads them fine.
- **Imports.** The stored import options leave out the types list when it means all types. An empty list still means none.
- **Plugin runs.** A run progress or summary document with invalid UTF-8 is refused. A repeated key is still accepted.
- **Package registries.** Signing a registry index now also covers a raw `&`, `<` or `>` correctly. Signed indexes made earlier still verify and published registries do not change.
- **Package errors.** The error wording of package manifest and index parsing changed.
- **Registry meta files and release keys.** `hatamoto registry build` now reads its meta file case-sensitively: a key in the wrong case (`Title` for `title`) is an unknown member, and a repeated key or invalid UTF-8 is refused. Release key files are refused the same way for a repeated member or invalid UTF-8. Fix the file and build again.
- **Plugin manifests.** A plugin package whose `plugin.json` repeats a member name (also by case), or spells a field with a different `_` or `-` (`allowedHosts` for `allowed_hosts`, `request-headers`), is refused when a package is uploaded, installed from a URL or registry, or added to a registry index, so the permissions shown and approved are the ones enforced. Plugins already installed keep loading, even with such a manifest (a name repeated by case only reads as the last one, as before). Theme and signature manifests are still read tolerantly.
- **Uploaded backups.** A backup manifest with invalid UTF-8 in an uploaded archive is now refused.
- **Agents endpoints.** The agents, errors and hits endpoints may return `[]` where they returned `null` for an empty list.
- **Unchanged byte for byte.** Webhook bodies and their HMACs, release records, signed indexes and channel manifests, and passkey data.
- **Migration.** None.

**Rollback floor: unchanged.** The previous release reads the stored `[]` and `{}` values the same way.

## Unreleased: Contact button

A new **Contact button** widget in the page builder, and a floating contact button for the whole site (Settings > Contact button, off by default). Both are plain links that open WhatsApp, a phone call, a text message or an email. A blank number or address uses the phone or email from the business details of the page's language. No script runs and nothing is sent to a third party until a visitor taps the link.

- **Floating button.** One fixed link on every public page except the preview, 404, error and site access pages. It respects the phone's safe areas and is hidden in print. A theme with its own base layout calls the `contact-float` partial to show it.
- **No migration, no new dependency.** The settings live with the other theme settings.

**Rollback floor: unchanged.** The previous release ignores the saved setting and shows no floating button; a page that holds a Contact button widget keeps its other content but the widget is not published until you upgrade again.

## Unreleased: Off-site backups

Settings > Backups has a new Off-site copy card. Point it at an S3-compatible bucket (Amazon S3, Cloudflare R2, Backblaze B2, MinIO or similar), pick a daily or weekly time in UTC, and each scheduled backup is uploaded (the provider checks each part's SHA-256 on receipt, and the size and recorded checksum are read back) and pruned to the number of copies you keep. "Back up and upload now" runs one on demand. The backups table gains an Off-site column and the dashboard health card an Off-site backup row.

- **Scheduled backups.** The schedule takes a new backup kind, `scheduled`, kept on disk per `HATAMOTO_BACKUP_KEEP_SCHEDULED` (default 7). Uploads run inside the backup lock, so they never overlap another backup or an update's backup.
- **Restore from the bucket.** `hatamoto backup list --remote` and `hatamoto backup restore --from-offsite <id>` work on a machine with no database; the target comes from `HATAMOTO_OFFSITE_*` (the secret key from the environment only).
- **No new dependency.** The S3 client is part of the binary (see docs/DEPENDENCIES.md).
- **Keep it private.** A backup holds the database and `secrets.env`: use a private bucket with server-side encryption.
- **Migration.** One new table (expand-only).

**Rollback floor: unchanged.** The previous release runs against the migrated data and ignores the new table; scheduled backups and uploads stop until you upgrade again.

## Unreleased: Site access

- **Site access: coming soon, maintenance and password modes.** Settings > Site access holds the public site back with a themed page, a 503 with `Retry-After`, or one shared password, while signed-in admin users still see the real site. A banner reminds owners and editors on every admin page. New columns only (expand-only); the previous binary ignores them and shows the site as open.

## Unreleased: Tag packs

A plugin package can now add tracking tags to Settings > Tracking with JSON files only (`tags/<name>.json`): no code, no permissions. Each tag loads after consent, adds only its own hosts to the page policy while it is on, and shows as "Tag pack" on the page. Registries list the hosts and category before install.

**Rollback floor: unchanged.** Servers older than this release refuse a package with `tags/`. Uninstall tag packs before downgrading: a saved pack tag makes every Settings > Tracking save fail on an older server.

## Unreleased: Tag manager in Settings > Tracking

Settings > Tracking loads 13 common tags (Google Analytics 4, Tag Manager, Ads, Meta, Clarity, UET, LinkedIn, TikTok, Pinterest, Tawk.to, Crisp, HubSpot, AdSense) from one fingerprinted script, after cookie consent, with the page policy widened only for the tags you turn on.

- **Off by default.** With no tag on, public HTML and the policy header are byte-identical to before.
- **Signed-in users.** A shared switch (on by default) skips tags when the new `hm_member` cookie is present. Previews and the editor never load tags.
- **Conversions.** One `hm:conversion` event per form success carries the submission id.
- **Plugins moved into core.** The first-party `gtm` and `google-analytics` plugins are folded into this page on upgrade and removed; a one-time notice and audit entries record it. The tag is turned on only if the plugin was really running; an ID that is not valid for the tag leaves the plugin installed.
- **Migration.** One new table (expand-only).

**Rollback floor: unchanged.** The previous release ignores the new table; the removed plugins would have to be installed again and their IDs entered again, because their settings are deleted with them.

## Unreleased: Cookie consent banner and Consent Mode v2

Settings > Privacy adds an optional cookie banner. Visitors can accept all, reject all or choose Analytics, Marketing and Functional, and the choice is announced to your scripts through `window.hm.consent`, the `hm:consent` event and Google Consent Mode v2. A choice is remembered for 12 months; the page shows daily counts of the choices without storing any visitor data.

- **Off by default.** Until you turn it on, public pages are byte-identical to before.
- **EU and UK mode.** Shows the banner by the country header of your CDN (`CF-IPCountry` or `CloudFront-Viewer-Country`); the page HTML and its cache are the same for every country.
- **Public API.** `GET /api/v1/public/consent/region` (never cached) and `POST /api/v1/public/consent` (daily counter, limited per IP).
- **Migration.** Two new tables (expand-only). No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.** The previous release runs against the migrated data and ignores the new tables.

## Unreleased: Plugin SDK 0.18.0 adds eight things first-party plugins need

SDK 0.18.0 is an additive minor over 0.17: plugins built for earlier ranges keep loading. The first publish takes the public SDK repository from v0.13.0 to v0.18.0 in one commit.

- **Request headers.** `request_headers` in `plugin.json` lists extra request headers (a webhook signature, say) passed to the plugin's routes. `Authorization`, `Cookie`, hop-by-hop headers and any header that carries the visitor's address (`X-Forwarded-For`, `True-Client-IP`, `CF-Connecting-IP`, `Fastly-Client-IP` and the like, refused by name and by shape) are never passed. An update that adds a request header asks for confirmation. The plugin page lists them.
- **Cookies on public routes.** The cookies a plugin declares in `cookies` reach its public routes (`HTTPRequest.Cookies`) and the route may set them (`HTTPResponse.SetCookies`) under `hmp_<plugin>_<name>`, with the limits and flags `render.page` already uses. Responses that set a cookie are never cached, and a public route of a plugin that declares cookies is never shared-cacheable (`Cache-Control: private`, `Vary: Cookie`). Plugins built for older ranges that already declare `cookies` now receive them on public routes too.
- **Page security hosts (`csp`).** `plugin.json` can list https hosts for `script_src`, `connect_src`, `frame_src` and `img_src`. The permission prompt and the plugin page show them, and an update that adds one asks for confirmation. A community plugin may only list image hosts. Wildcard hosts are refused in `script_src`, and everywhere over a shared suffix (`*.github.io`) or an IP address. The hosts join the public pages' policy while the plugin is enabled (`script_src` and `connect_src` take effect; images and frames are already open to https).
- **Translations from a run.** `changeset.entity.upsert` takes `translation_of` (an entity id or the source key of an earlier entity). Publishing joins the staged entity to the source's translation group; reverting undoes the link.
- **Caption and credit on media.** `changeset.media.attach` takes `caption` and `credit`; publishing stores both on the media record (new nullable `credit` column). The owner edits the credit on the media page, and `media.get`/`media.list` return caption and credit.
- **Reviewed alt text changes.** `changeset.media.alt { media_id, alt }` stages a new alt text for a media item the owner uploaded; the run declares `proposes: ["media_alt"]`. Every change is listed in the review as old text and new text, and the owner decides on each. Publishing applies it, reverting restores the old text. Nothing else on the media row changes.
- **Content reads in the fetch phase.** `entities.get`, `entities.list`, `entities.translations`, `media.get` and `media.list` now work while a run fetches, and return only what an anonymous visitor can see: drafts, scheduled and archived entities, variants, hidden people, locales the site does not serve and media no public page uses are never returned. A fetch step may make 200 such reads. Terms, menus, redirects and forms stay transform only, and `http.fetch` is unchanged.
- **Content types from a plugin.** A plugin package can ship `schemas/<name>.json` without a section template. The type is created when the plugin is enabled (the prompt says "Create the content types ..."), is never overwritten, and stays when the plugin is disabled or uninstalled. Uninstall has an off-by-default option, "Also remove content types and their content", that removes only the types the plugin installed. It needs a recent sign-in and the plugin name typed back, and deletes each entry like a normal delete, so webhooks and other subscribers hear about it. New nullable `origin_plugin` column (expand-only).

**Rollback floor: unchanged.** The previous release ignores the new `credit` and `origin_plugin` columns. Content types a plugin installed stay as ordinary types after a rollback, and a translation link made by a changeset stays.

A plugin that uses `csp`, `request_headers`, `translation_of`, `media_alt` or content types must declare `"sdk": "^0.18"`: an older host ignores those fields without an error.

## Unreleased: WebP and AVIF image copies

- Resized images are also served as WebP (AVIF where it is smaller), with the original format as fallback. Settings > Media can rebuild the copies of existing images, and copies of WebP uploads are now real WebP files. Source builds need `-tags nodynamic`. Additive; no breaking change.

## Unreleased: Plugins change content through runs and changesets (breaking for plugins that write)

Core 1.1.0 with SDK 0.17.0 is a deliberate hard break of the plugin write API. A plugin can no longer change live content directly: it starts a run, stages its writes into a changeset, and the owner publishes or discards the changeset. There is no compatibility shim. Read the upgrade note before you move: [Plugins write through runs](UPGRADES.md#plugins-write-through-runs).

- **Removed permissions.** `entities.write`, `terms.write`, `menus.write`, `media.write` and `redirects.write` no longer exist. A plugin that requests one is not loaded: it stays installed, shows the reason on the plugins page, and its imports, webhooks and cron jobs stop until you install a version built for runs. Grants you stored for them are ignored.
- **Removed host functions.** `entities.create`, `entities.update`, `entities.set_status`, `entities.delete`, `terms.save`, `terms.delete`, `menus.save`, `menus.add_item`, `menus.delete_item`, `media.upload`, `redirects.create` and `redirects.delete`. A call answers `unavailable` and names the replacement.
- **Plugins that never wrote content** (built for `^0.5` to `^0.16`) load and run as before.
- **Argument limits on the new functions.** The run and changeset functions refuse arguments nested deeper than 32 levels, arrays over 10,000 elements and requests over 200,000 JSON tokens with `invalid_argument`. Functions that existed before keep their limits; see the plugin API reference.
- **The update check does not warn.** The pre-update check compares SDK ranges only, so a plugin built for `^0.16` that requests a removed permission passes it. Check your plugins before you update.
- **Migration.** New tables and columns only (expand-only).

**Rollback floor: unchanged.** The 1.0.x binary runs against the migrated data and ignores the new tables. Publish or discard every open changeset before you roll back: 1.0.x cannot show or publish one, so its staged content would only wait for the 72 hour expiry. A plugin built for runs does not load on 1.0.x.

## Unreleased: Business details live per locale

Every locale, the default one included, now has its own business details, edited under Settings > Locales > Business details. The default locale's card comes first and is your organisation and the fallback for the others. Settings > General keeps Identity, Homepage and Email, and the notification email moved into its Email card.

- **Upgrade.** A migration folds the card stored for the default locale into the default business, and copies the values it overrode into the other cards so those locales print what they printed before. A site whose default locale card differed from the site-wide details may see its Organization change once. Read the upgrade note before you move: [Business details live per locale](UPGRADES.md#business-details-live-per-locale).
- **Office switch.** The default card has **This locale has an office**. The upgrade turns it on where the old default card had an office, so a default page that printed a local business keeps it.
- **Not inherited.** A field that the old default card set but the site-wide details left empty is marked "Not inherited, empty" on the other cards, with a switch to inherit again.
- **Default locale change.** Changing the Language in Settings > General folds the new default locale's card into the default business and keeps the old default's own details (address, office, name and the rest) as its card.
- **REST (client-visible change).** `business_profile` is the default locale's business, so besides today's keys it now carries `office` and the locale-only fields (`name`, `country_name`, `image`, `business_type`, and the address extras). `locale_profiles` holds the other locales only, and a card may carry `no_inherit`. Clients must tolerate these new keys. A write of a non-null card under the default locale's tag in `locale_profiles`, which used to succeed, now answers 422: write it to `business_profile` instead. A `null` still deletes a stored leftover.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.** The previous release runs against the migrated data; see the upgrade note for what it shows.

## Unreleased: SEO settings over the API and MCP

An owner, or a script with an API key, can now read and change the site's SEO settings through `GET` and `PATCH /api/v1/settings/seo`, and an MCP client can do the same with five new tools. This is a minor, additive release.

- **Scopes.** Two new API key scopes, `read:seo` and `write:seo` (`write:seo` implies `read:seo`). Existing keys do not gain them, except `*` keys, which allow everything and so can now read and change SEO settings too (site-wide noindex, robots lines, the AI-crawler policy). Review any `*` key before you upgrade. The admin key form lists them as **Read SEO settings** and **Change SEO settings**.
- **REST.** `GET` needs `read:seo`, `PATCH` needs `write:seo`; an owner session works too. `PATCH` merges: absent fields are unchanged, `null` deletes a map entry, unknown keys are refused, and invalid input answers `422` with a `fields` map. The IndexNow key is never returned and cannot be set. Fields keep their stored polarity (for example `monitor404.disabled`). This is the only non-entity route an API key reaches besides the existing allowlist.
- **MCP.** `seo_get`, `seo_update` (`write:seo`), `seo_preview`, `seo_analyze` and `seo_404_list`; all but `seo_update` need `read:seo`.
- Every change writes an audit row with the way in, the key and the sections changed.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.**

## Unreleased: Shared content in template lists on multi-locale sites

Theme data funcs (`recent`, `query`, `terms`, `attrValues`) now include shared (`und`) content plus the default locale on multi-locale sites. Known limit: on a non-default locale page the lists still use the default locale plus shared content.

- Single-locale sites are unchanged.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.**

## Unreleased: Business details over the API and more graph fields

An owner, or a script with an owner token, can now read and write the business profile and the per-locale cards through `GET` and `PATCH /api/v1/settings/site`. The profile also gains the fields the Organization and LocalBusiness graph could not print before, all editable in Settings > General > Business details and on each locale card.

- **REST.** `GET` returns `business_profile` and `locale_profiles`. `PATCH` leaves absent fields unchanged, replaces the profile whole, replaces or deletes (`null` or `{}`) a card per tag, refuses unknown keys and answers 422 with field paths when a profile does not validate. A card tag must be in the locale list on a multi-locale site. The `phone`, `business_hours` and `address` settings always replace the profile's phone, hours note and address, even when empty. Owners only.
- **Graph.** A catalogue name for `hasOfferCatalog`, a `priceSpecification` per service (price from, price to, unit), an opt-in `provider` on every catalogue service, and an `additionalProperty` list on the Organization.
- **Service addresses.** A service address may be a site path such as `/services/seo`. On a locale page it opens under that locale, elsewhere under the default locale. Full addresses are unchanged.
- **Cards.** Every new field is also editable on a locale card. An empty card value inherits the site-wide one; a provider of Not stated inherits.
- **Final JSON field names.** `business_profile`, `locale_profiles`, `catalog_name`, `service_provider` (true/false, card can override), `services[].price_min`, `services[].price_max`, `services[].unit_text`, `additional_properties[].name`, `.value`, `.min_value`, `.max_value`, `.unit_text`.
- Sites that leave the new fields empty see no change in their graph.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.**

## Unreleased: Menus are always real data

Navigation now comes only from menus. Before, a site without a Header menu still showed an automatic list of its first pages and a Blog link; that render-time fallback is gone, so a location with no menu, or an assigned menu with no items, renders nothing.

- **Existing sites keep their header.** A site that relied on the fallback gets a real **Header** menu on first start after the upgrade (its first 50 published pages that are not hidden from the nav, plus Blog when there are posts). It runs once, so a Header you delete later stays deleted. A new site gets a Header and an empty Footer when setup content is chosen.
- **Several languages.** On a site with more than one language the one-time Header is built from the default-language pages and their links, and every language shows that menu (a notice is logged at the upgrade). Edit it under Menus, or give each language its own menu items there. A page whose link differs from what the menu would render (for example under a URL override the menu cannot reproduce) is stored as a fixed link with the old address.
- **Themes declare menu locations.** `theme.json` gains a `menus` key (not the existing `menu` presets key): up to 12 locations, each with an optional title and up to 24 default items, each a `url` or an `archive` slug. Templates read them with `menu "<location>"`. `hatamoto theme check` and `theme pack` refuse a malformed block. See [THEMES.md](THEMES.md#menu-locations-menus).
- **Seeding.** Activating a theme creates a menu for each declared location that has default items and none yet. An update seeds only locations the previous version did not declare. Seeding never overwrites or deletes a menu, a deleted menu returns only when the theme is activated again, and switching themes leaves menus alone.
- **The Menus picker** lists No location, then the core locations under Site, then the active theme's locations under its name; the list shows each menu's location as a badge, marked "not used by theme" when the theme does not declare it.
- **One menu per location in the admin.** Choosing a location for a menu in the admin takes it from the previous holder, which is left unassigned; saving a menu without changing its location never does. A WordPress import follows the same rule. Plugin `menus.save` and the REST menu endpoints do not take locations: menus sharing a location coexist as before and the site renders the oldest.
- **An assigned Header with no items loses its automatic links.** The upgrade seed leaves any site that already has a Header menu alone, so an assigned but empty Header now renders nothing. Add items under Menus.
- **New pages are no longer added to the header automatically.** The upgrade seed is a one-time snapshot; pages published later appear in the header only when you add them to the menu.
- **Plugins.** `menus.save` and the REST menu endpoints accept any well-formed location name and never take a location from another menu. There is no SDK, hook or manifest change.
- Migrations: additive `menus_seeded_at` column on `hatamoto_site_config`.

**Rollback floor: unchanged.** An older binary ignores the new column. The seeded menus stay: a site that was seeded keeps its real Header (the old automatic list returns only where no Header menu exists), and a seeded Blog link keeps working because it also stores its path. In the older admin, saving a menu at a theme-declared (non-core) location clears that location.

## Unreleased: Conversions and lead attribution

**Analytics > Conversions** shows what turns into leads. A lead is every form submission that is not spam. Each submission now keeps the **first and last touch** of the visit it came from (channel, source, medium, campaign, landing page), so the page can show leads by channel per day, a funnel (Visits, Viewed 2+ pages, Saw a form, Started the form, Submitted), **AI: what you give vs what you get** (pages each AI company fetched against the visitors and leads its assistant sent back) and the Recent leads. **Analytics > Traffic** now fills its Leads and Conv. rate columns and the First touch | Last touch choice works (First is the default). Forms > Submissions gets a **Came from** card on each submission, and the CSV export gains six attribution columns after Referrer. MCP gets an `analytics_conversions` tool.

- **Privacy.** Still cookieless, with no visitor id, IP address or user agent stored. The browser keeps its visit inputs only in tab-scoped `sessionStorage`; they leave it only inside a form that is actually submitted to the site (a hidden `hm_attr` field), and are stored only with that submission and deleted with it. A form that stores no submissions stores no attribution. Nothing is sent to anyone else.
- Submissions made before the upgrade, without script or from a headless front end count as leads with channel **Unknown**; nothing is backfilled and a submit is never blocked by missing attribution.
- Two new funnel events (`form_seen`, `form_start`) are counted per visit and day. The engagement switch on the Traffic page turns them off with the other engagement counters.
- The Recent leads emails and the CSV export on the Conversions page show only to users who can view form submissions.
- **SDK 0.14.0**, additive: `form.submitted` (webhook and plugin hook) gains an optional `attribution` object with `first` and `last` touches. Plugins built for `^0.5` to `^0.13` still load; no manifest change.
- Migrations: ten nullable attribution columns on `hatamoto_form_submissions` and a funnel counter column pair on `hatamoto_traffic_daily`, both additive.

**Rollback floor: unchanged.** An older binary ignores the new columns and the new events.

## Unreleased: Traffic tab

**Analytics > Traffic** shows where visits come from: Visits, Pageviews, Channels (direct, organic search, paid, email, social, referral, AI assistants), Sources, Campaigns, Landing pages, Devices, Countries and Engagement, with a compare period. Overview gets a **Visits by channel** chart. Everything is cookieless: only daily counts are stored, with no visitor id, IP address, user agent or referrer URL. A visit is the first pageview of a tab or an arrival from another site. MCP gets an `analytics_traffic` tool.

- A small core script, `hm-track.js`, is now added to public pages and sends the pageview beacon; the default theme's `hm.js` no longer does. A custom theme that forked the old `hm.js` and still contains the `/api/v1/track` beacon keeps counting pageviews as before but records no visits or channels (remove the beacon from its `hm.js` to get them). Headless front ends that post the old body keep counting pageviews.
- The track endpoint now ignores requests with an empty or bot-like user agent, and the script sends nothing in automated browsers, so pageview counts can drop slightly. Past data is not rewritten.
- Countries need an optional `HATAMOTO_GEOIP_DB` (a country `.mmdb` you supply; read at boot, restart to reload). Nothing is downloaded. See the [configuration reference](site/operate/configuration-reference.md).
- Engagement counting can be switched off by an owner in the Measurement card of the Traffic page.
- Channel AI assistants here and the Referrals tab can differ, because they count different signals.
- Migrations: a new `hatamoto_traffic_daily` table and a small `hatamoto_analytics_settings` table, both additive. A nightly job prunes source, campaign and landing rows after 13 months and compacts old days.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.** An older binary ignores the new tables, and the legacy `pageview` and `ai_referral` counters are written as before.

## Unreleased: Search engines and a Google section in Analytics

"AI analytics" is now **Analytics**, with a new **Google** section next to Crawlers (same URLs, `/analytics/google` is new). Crawlers gains a Search engines group and an **AI only / AI + search** toggle on the hero chart. Google shows Googlebot crawls, the verified share, pages crawled of published, the median time from publishing to first crawl, a stacked response-class chart and a page table with All, Not crawled, Errors and Stale filters. Spoofed fetches are left out of page numbers, and dates are whole UTC days. Impressions, clicks and position appear only when a search performance source is connected; otherwise one notice says so. MCP gets `analytics_google` and `search_first_crawl_at` on a page's activity (additive).

- Search engine hits outside the published ranges are now decided by forward-confirmed reverse DNS before they are called spoofed.
- Migrations: a new `hatamoto_agent_search_first_crawl` table, and a relabel that turns spoofed hits from a CDN edge address into unverifiable (the daily counters move with them; it reads stored IPs only inside the existing retention and stores none). After the upgrade a background job attributes past crawler hits to pages and seeds the first-crawl table; nothing to run by hand. Both are idempotent.
- No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.** An older binary ignores the new table; relabelled hits stay unverifiable.

## Unreleased: Builder canvas and inspector, redrawn

The page builder's canvas and inspector are easier to read and to use. On the canvas: a toolbar beside the selected element (Parent, Duplicate, Wrap, Hide on this device, Delete, More), breadcrumbs and Fit, 100, 75 and 50 zoom above the page, a coloured band on Tablet and Phone, round **+** handles between elements (with **Choose an element…** and **Insert a ready-made section…**), **Enter** to edit a selected text and **Esc** to cancel, `Ctrl+K` for the command palette from the page, a shorter format bar on headings, a link panel that lists your pages and anchors, and `Ctrl+Z` to undo the last format while typing. In the inspector: a header with an **Element options** menu, a **Class** row (the word "preset" is gone from the screen), a Style tab that opens with the State switch and a few Essentials per element, source labels with one **Reset** per control, **Show on** switches for Desktop, Tablet and Phone, padding nested in margin, colours with site names, **None** and recent colours, a reordered Content tab, **Number of columns**, an **Items** panel for Tabs, Accordion and Gallery, and **Restore from an earlier version…** for a single element (new read-only route `GET /entities/{id}/builder/node-versions`).

No migration; the stored document and its keys are unchanged, and nothing new is stored. **Rollback floor: none.**

## Unreleased: Public fingerprint scrub and opaque-origin page scripts

Public responses no longer say what serves them. **Everyone is signed out once after this upgrade**: the session cookie is renamed (`__Host-s` over HTTPS, `s` on plain-HTTP dev), so each person signs in again once. The old `hatamoto_session` cookie is ignored. The admin sets no CSRF cookie; the only cookie like it is the JSON API session cookie, sent at login when `ENABLE_COOKIE_AUTH=true`. It is now its own session kind named `__Host-a` (or `a`), and the admin cookie no longer authenticates `/api/v1`. People who use the API with a cookie sign in again once; the login response carries the `csrf_token` to send in `X-CSRF-Token`.

- `/health` and `/ready` answer `ok` (and `503 not ready`) to anyone; the JSON detail needs an admin session, an `X-API-Key` or a Bearer token. Status codes are unchanged, so container and load balancer checks, `hatamoto healthcheck` and `scripts/ship.sh` keep working.
- `X-Hatamoto-Renderer` is gone, and the shipped Caddy config strips `Server`, including on Caddy's own error pages. Check a deployed site with `deploy/test-caddy-headers.sh`. A Caddy file you copied earlier needs `header -Server` added by hand.
- New `PUBLIC_API_PREFIX` renames the public forms and page-view endpoints (at least two segments, for example `/x/v1`); the default `/api/v1/public` stays mounted. The theme script follows the prefix. See the [configuration reference](site/operate/configuration-reference.md).
- Plugins are now classed first-party, community or self-built. A community plugin is refused the `site.scripts` grant, and a stored grant is ignored at render. A page that carries a live community script is served in a CSP sandbox (opaque origin), so that script cannot read cookies or call the admin API; the page's own forms and page-view beacon still work (they are answered for `Origin: null` with a page token). No community package can be granted `site.scripts` today.
- Browser acceptance for the sandbox is `scripts/admin-qa/opaque-origin.mjs`, which needs the test-only `hatamoto_qa` build tag (never in a release binary; see `scripts/admin-qa/README.md`).
- `PUBLIC_API_PREFIX` adds a path; it does not hide the default. `/api/v1/public` stays mounted, so a scanner still finds it.
- A CDN or cache rule keyed on the `hatamoto_session` cookie name must be updated to `__Host-s` (or `s` on plain HTTP).
- A browser that still holds a pre-upgrade `hatamoto_session` cookie for a live admin session has that session revoked server-side and both legacy cookies (`hatamoto_session`, `hatamoto_csrf`) expired the first time it visits after the upgrade. The legacy cookie never signs anyone in. Any other legacy value, or a `hatamoto_csrf` alone, gets no response cookie, so the product name is not sent to clients that do not hold a live legacy session. Until that first visit the old session stays valid, up to the 7-day idle limit (or 30 days of age).
- `builder.island` is refused for community packages (grant and render; an island of a community package is dropped even with a stored grant), for the same reason as `site.scripts`: an island is a same-origin script.
- Themes are trusted and outside this boundary: a theme controls every public template and its JavaScript, so install community themes only from sources you trust.
- A no-JS form post from a sandboxed page without a valid token is answered with the usual redirect and `form_error=1` (nothing is stored); the JSON endpoint still answers 403.
- No migration; pages and stored content are unchanged.

**Rollback floor: unchanged.** An older binary reads the database as before. Sessions created under the new cookie names are not recognised by an older binary (it reads `hatamoto_session`), so everyone signs in once more after a rollback. A legacy session is revoked the first time its browser visits after the upgrade, so a rollback does not sign that browser back in; one that has not visited since the upgrade still holds a valid legacy session, and a rollback inside the idle window signs it back in. An older binary answers JSON on `/health` and `/ready`, which `scripts/ship.sh` accepts.

## Unreleased: Recovery copies live on the server

Unsaved work on a published post or page is now kept on the server, one copy per user and entity, instead of in the browser. It is written by four POST routes (`/entities/{id}/edit/recovery`, `/builder/recovery` and a `/discard` twin of each) and delivered inside the editor page. A copy survives sign-out and follows the user to another device. It is deleted by the user's own save, publish or update, by Discard, after 30 days (prune job), and with the entity or user. Drafts rely on draft autosave. Copies that older versions kept in `localStorage` are removed when the admin loads and are never restored, so a copy that was only in a browser is lost at the upgrade.

## Unreleased: MCP builder tools

An MCP agent can now read the builder's vocabulary and build or edit a page by ops, with the same checks as the admin builder. New tools: `builder_kinds`, `builder_stylespec`, `builder_design`, `builder_get`, `builder_validate`, `builder_render`, `builder_templates_list`, `builder_template_nodes` (all `read:entities`) and `builder_apply_ops` (`write:entities`). `builder_apply_ops` takes the `version` `builder_get` returned: a stale version is refused with `conflict: ... current version is N`, and a batch with one bad op stores nothing. It runs the validator, the element-setting rules, the slot and block-reference rules and the sanitiser, stores one version ("Edited by <key> via MCP") and can publish. `wrap`, `unwrap` and `duplicate` are available beside the editor's six verbs; omitted node ids are generated and returned. Limits: 500 ops and 1 MiB of ops per call, 2 MiB of rendered output. See [MCP.md](MCP.md#builder-tools) for the tools and an example session that the test suite replays.

- **Fix:** `entities_update` with a `page_doc` now meets the builder's reuse rules too. Before, a `slot` node on a page or a block that embeds itself was stored through MCP although the admin builder refused it; it is now refused with `invalid page_doc: ...`. A client that wrote such a doc through MCP fails from this release (the REST API does not check these two rules yet).
- The admin builder is unchanged: its save, messages and boot data are the same bytes. The shared rules moved into `internal/entity`; the kinds boot JSON and the template node reader moved into `internal/builder`. No migration, no new dependency.
- Known limits: the version check and the write are not one atomic step (two writers in the same few milliseconds can both pass, as in the admin builder); pages that still have only the legacy `page_sections` layout, posts and people are refused by the builder tools (`not_builder:`); only tree templates are offered.

## Unreleased: Element settings in the builder

Buttons, images, containers and galleries get the settings Elementor users expect. **Anchor** gives an element a public `id` (link to it with `#anchor`; the Link field suggests your pages and the page's anchors). **Accessible name** and **Tooltip** set `aria-label` and `title`. Links have **Open in new tab** (always `noopener noreferrer`) and **Nofollow**, **Sponsored** and **User generated content**. Images have a link, a **Caption** (a `figure`) and **Lightbox**, which a gallery can switch on for all its images; a container can be one big link. Duplicate or reserved anchors, a link plus a lightbox on one image, and a linked container with a button, form, menu or other link inside are refused on save with the reason.

- Images from the media library now carry `width` and `height`, so layout is reserved before they load (measured CLS 0 under a throttled cold load; the same page without the attributes shifts 0.41). This is the one change to existing live pages: library images gain the two attributes. Photos that carry an EXIF rotation (phone photos taken sideways) still reserve the unrotated box and shift once.
- A page with a lightbox loads one hashed script, `/builder/lightbox.<sha8>.js` (no inline code; the dialog opens on a plain click, Esc closes, Tab stays inside, arrows move through a gallery, focus returns; without script the image links to the file). Pages without a lightbox load no extra script; `public_builder_js_scripts` stays 0.
- The widget lint now fails a `target=_blank` link without `noopener` and a link inside a link.
- A plugin widget that declares `preset` or `navlabel` is refused at install ("property name reserved") instead of panicking. A new install that declares `anchor`, `aria` or `tip` is refused too; a package already installed keeps working: the builder skips that element setting for the widget, so its own prop is untouched, and logs a warning (`plugin widget declares props the builder now uses`) at startup.
- Fix found on the way: the default theme's hidden light/dark toggle took no space and pushed the header nav 58 px sideways when the script showed it, a layout shift on every page; it now keeps its box while hidden (CSS only).
- Pages that use none of this are byte-identical in output and storage; goldens unchanged except the new `settings` page. No migration.
- Admin JS: the inspector's element settings are one lazy chunk; builder first paint is 85,152 B gz (cap 89,255; target 85,300), total 187,271 B gz (cap 220 KiB). Budgets re-baselined with reason "builder-e6-element-settings".

**Rollback floor: this release, for pages that use any element setting.** An older binary rejects the new props: the public page renders an empty body and the admin shows the page as broken (saves refused with 422 `invalid_page_doc`). Before a rollback, remove `anchor`, `aria` and `tip` (any kind), `target` and `rel`, `href` on a container or image, `caption` and `lightbox` from every page, block, part and saved version that must keep rendering (BUILDER.md, "Rollback floor for element settings").

Deferred: custom `data-*` attributes and `lang`, art-direction image sources, lightbox for video, links on list items and icons, popups, anchors on theme sections and blocks, a smooth-scroll script (use `scroll-behavior` in the theme CSS).

## Unreleased: Inline rich text in the builder

Headings and text can be formatted where they sit on the page. Double-click one: a bar offers bold, italic, underline, strike, code, link (with an Open in new tab option) and Clear; Ctrl/Cmd+B, I, U and K work too, Enter finishes, Shift+Enter adds a line break in a text, Esc restores. Pasted text keeps only those marks. The inspector's Text field for a heading or text is a small editor with the same marks, in step with the canvas, usable by keyboard and at phone width.

- Storage: formatted text is the new `html` prop of `heading` and `text` (at most 20,000 characters); the plain text stays in `text`. It is sanitised on save and again at render: `strong em b i u s code br a` only, `javascript:`, `data:` and `//host` links dropped, `rel="noopener noreferrer"` on every new-tab link. A bound text wins over `html`. The version diff shows tag-stripped text with a "formatting changed" note.
- Typing sends no `patch` to the preview, so the caret does not move; keystroke handling measured at p95 under 2 ms on a 60-node page.
- Fixes found on the way: clicking a theme component's field on the v2 canvas threw an error and did nothing; it now selects the component and focuses the field in the inspector.
- Pages without formatted text are byte-identical in output and storage; goldens unchanged. No migration.
- Admin JS: the bar, link dialog, inspector editor and component field jump are lazy chunks; builder first paint is 85,617 B gz (re-baselined from the 85,000 B target by the review fixes). The canvas bridge is 14,078 B gz.

**Rollback floor: this release, for pages with formatted heading or text.** A node that carries `html` cannot be read by an older binary: the public page renders an empty body and the admin shows the page as broken. Before a rollback, remove `html` on pages that must keep rendering: press Clear in the bar (a text with a line break keeps `html` until the break is deleted) or send the page_doc through the API with `p.html` removed; the no-JS outline cannot remove it.

## Unreleased: Builder direct manipulation

Work on the page itself. A right-click on the canvas or a structure row, Shift+F10 or the menu key opens an element menu (copy, cut, paste after or inside, duplicate, rename, wrap, unwrap, copy and paste style, delete). Copy and paste go through the system clipboard, so an element copied on one page pastes into another; the server validates and re-ids what is pasted (`POST /admin/entities/{id}/builder/paste`, writes nothing). Ctrl or Cmd plus click and Shift plus click select several elements for delete, duplicate, hide, wrap and copy in one undo step. F2 or a double-click renames a row (shown in the structure and canvas tags, never on the public page). The **Add section here** bars open a picker of layouts (1 to 4 columns, 33/67, 67/33, 2×2 grid). The selected element has a move handle on the canvas, and **History** (Ctrl+H) jumps to any earlier step of the session. See [builder-reference](site/admin/builder-reference.md) and [BUILDER.md](BUILDER.md#direct-manipulation-builder-e4).

- Fixes found on the way: empty layout cells are now visible and clickable in the editor; the first native element added to a page drawn from v1 sections re-renders the frame so it gets its layout CSS; the move handle on a v1 section moves the whole section and works when pressed before the page has finished loading; the add-section button's accessible name now contains its visible text.
- Pages without a renamed element are byte-identical in output and storage; goldens unchanged. No migration.
- Admin JS: the menu, clipboard ops, layout picker, history panel, drag and banners are lazy chunks, so builder first paint stays at 82,725 B gz (cap 85,000 B) and the total is 170,376 B gz (cap 220 KiB). Budgets re-baselined with reason "builder-e4"; no cap raised.

**Rollback floor: this release, for pages with a renamed element.** A node that carries the new `navlabel` prop cannot be read by an older binary: the public page renders an empty body and the admin shows the page as a broken doc (saves refused with 422 `invalid_page_doc`). Nothing is rewritten by the upgrade; clear the names (Rename, empty value) on pages that must keep rendering before a rollback. Everything else here is client-side or writes nothing.

Deferred: dragging several elements at once, editing the styles of several elements together, and history that survives a reload.

## Unreleased: Builder inspector controls

The Style tab's fields are now real controls: lengths have a number field, a unit menu and a slider; colours are a swatch with a palette popover (site colours, custom colour, hex); Spacing is a box model with a link toggle per ring; every row shows a set-here dot, a source label (Local, Preset, Inherited) and a Reset; groups are collapsible and show a dot when something inside is set. A slider drag is one undo step. Keyboard: Tab wraps inside the colour popover and Esc returns to the swatch. See [builder-reference](site/admin/builder-reference.md) and [BUILDER.md](BUILDER.md#style-controls).

- Style keys, validation, the document and the compiled CSS are unchanged; goldens are byte-identical. No migration.
- Admin JS: the reuse UI, shortcut sheet, component fields and preset panel moved to lazy chunks, so builder first paint is smaller (81,658 B gz, cap 89,255 B) while the total grows with the new controls (155,644 B gz, cap 220 KiB).

**Rollback floor: unchanged.** No document-model change; an older binary reads every page this release saves.

## Unreleased: Builder states (hover, focus, pressed, parent hover)

The Style tab gets a State switch: Normal, Hover, Focus, Pressed and, for an element with a parent, Parent hover. Every state has its own values per device, shown on the canvas while selected (editor only). New style keys: Transform (`tx ty sc rot skx sky torigin`), Filter (`blur bright contrast sat gray hue bblur blend`) and Transition (`trans tdur tease tdelay`). Transitions apply only for visitors who have not asked for reduced motion. A hover-lift card is Shadow Small, Transition Transform and shadow, and on Hover Move Y `-4px` and Shadow Large.

- Documents that use none of the new keys compile to byte-identical CSS. No migration.
- Cascade: a Hover value set on Desktop still applies on a phone while hovering (set the Phone Hover value to change it); a preset's Hover beats the element's own Normal value; Parent hover beats the element's own Hover. See [BUILDER.md](BUILDER.md#states).
- The style presets page lists only Normal keys and notes a preset's state styles; saving it keeps them.
- Versions diff names states ("style Shadow (Hover, Tablet)").
- Budgets re-baselined with reason "builder-e2-states" (see the budget history); no cap raised.

**Rollback floor: this release.** A page, section template or style preset with a state key (`base:hover`...) or a transform, filter or transition key cannot be read by an older binary: it rejects the key (`unknown breakpoint` for a state key, verified against the previous parse path) and the page renders an empty body publicly and is refused with 422 `invalid_page_doc` in the admin, as in the Style depth rollback note below. Nothing is rewritten by the upgrade; remove the new keys from pages that must keep rendering before a rollback.

## Unreleased: Builder style depth (StyleSpec v2)

The builder's Style tab goes from 27 to 77 keys: margins with `auto` and negative values, size limits, aspect ratio and image fit, `position` including `sticky` with offsets and stack order, typography (font, weight, line height, letter spacing, case, decoration), borders (width, per side, style, colour, per-corner radius), a background image and gradient, opacity, `display`, grid placement and gaps. Raw CSS is still never accepted: every key is a token, a listed option or a bounded pattern, and the same validation applies over REST, MCP and the admin (`GET /admin/builder/stylespec` lists them).

- Show on one device: `hide` plus `display` on a narrower breakpoint. Phone-only is hide at Desktop and show at Phone.
- Documents that use none of the new keys compile to byte-identical CSS. No migration; nothing changes until a page uses a new key.
- The style presets page now lists only the keys a preset sets, with an Add style row.
- Admin JS: first paint +381 B gz (cap unchanged), total +1955 B (the labels are in the lazily loaded Style chunk). Budgets re-baselined with reason "builder-e1-style-depth". New metric `page_css_allstylekeys_gzip` (cap 8 KiB).

**Rollback floor: this release.** A page, section template or style preset that uses a new key, or a new value of an old key (`ta: justify`, `dir: row-reverse|column-reverse`), cannot be read by an older binary. The older binary validates styles whenever it parses a doc: the public page renders an empty body (and logs it), the admin shows the page as a broken doc and refuses saves with 422 `invalid_page_doc`, and a preset loses only its entries with new keys (a preset with only new keys is ignored but stays stored). Nothing is rewritten by the upgrade, so going forward again restores the pages, but saving the Style presets page on the older binary permanently deletes the new keys from presets that mix old and new keys. Remove the new keys from a page before rolling back if it must keep rendering. See [BUILDER.md](BUILDER.md#stylespec).

## Unreleased: AI analytics in the admin sub-sidebar

AI analytics is now five sections (Overview, Crawlers, Content, Referrals, Health) plus a page per agent, in the admin sub-sidebar like Settings and Theme. Old `?tab=` and `/pageviews` links redirect. The date picker can compare with the previous period. Chart.js is no longer shipped: the charts are drawn by the admin's own SVG modules, which makes the admin download about 208 KB smaller.

## Unreleased: Default theme layout options

- Hero: optional `stripLabel` and `stripItems[]` show a labelled row of short items between the buttons and the media. Nothing changes for heroes that do not set them.
- Split: optional `panelHeight: match` stretches the code panel to the height of the text column above 960px. The default (`auto`) is unchanged.
- Process steps: `layout: cards` now stays inside the container width on every site. Before, the cards ran wider than the rest of the page. Below about 1248px the only difference is the side padding.
- Admin: the items of a tag strip (and the hero strip) are now edited one per line in a textarea, so an item can contain a comma. Before, the comma separated field split `htmx admin, no Node` into two items on save. Existing content is unchanged. A theme can use this for its own string arrays with `"x-hm-field": "lines"`.
- The strip label is now linked to its list (`aria-labelledby`) in the hero and the tag strip.

## Unreleased: Pages published from the CLI show up without a restart

A page or menu written by another process (`hatamoto entity create --publish`, an import, the scheduled-publish worker, another replica) now clears the running server's page cache. Previously the server kept serving the old cached pages (for example a header without the new page) until the cache entry expired.

How it works: a migration adds `hatamoto_content_changes`, an append-only log. Triggers on `hatamoto_entities`, `hatamoto_menus` and `hatamoto_menu_items` add one row per writing transaction, only when a statement changed rows. Each server polls the log every 3 seconds (every second when `ENVIRONMENT` is not `production`) and clears its page cache when a new committed row appeared. An isolated change shows within about one poll interval (3 seconds). While changes keep arriving (an import) it clears at most once per five polls, so a change can take up to about 15 seconds to show, and once more when they stop. There is no setting; nothing to run after the update.

- Cost: one insert per writing transaction (no row lock shared between writers; rows older than a day are deleted) and one small read per server every few seconds. A transaction that stays open longer than a day and then commits is not noticed.
- A server that has not had the migration applied logs one warning and does not poll.
- Not covered: redirects (read live, as before), settings, terms, authors, media, forms, schemas, themes and plugins changed in another process. With several replicas those still reach the other replicas when the cache entry expires (`HATAMOTO_PAGE_CACHE_TTL`).
- The cache lifetime and cache headers are unchanged.

## Unreleased: Theme directory section

A new core section type, "Theme directory", shows the themes of your signed registry
(`HATAMOTO_REGISTRY_URL`) as cards with a screenshot, inside the normal site theme.
It is served at `/themes` from the same cached registry read as the plugin directory,
with the same unavailable message when the registry cannot be reached.

Themes are submitted by pull request: add `manifests/themes/<name>.json` with
`archive_grant: true`, an allowed license, and a source that is either a repository on
any https git host (pinned to a 40 character commit) or an https archive pinned by
`sha256`. A screenshot (`"screenshot": "assets/<file>"` in `theme.json`) is required
for registry themes. Authors check a theme with `hatamoto theme check ./mytheme --registry`
and repack and sign a pinned author zip with `hatamoto theme pack --archive F --sha256 H`
(`theme pack` reads the zip; it does not build one).

## Unreleased: Plugin scripts need the site.scripts permission

Plugins' browser JavaScript (`assets/plugin.js`, `render.head` script tags) and plugin sections/partials with scripts now need the `site.scripts` permission. After updating, a plugin that ships a script stops running it until its author lists `site.scripts` and you grant it under Plugins > Permissions. Nothing is granted automatically. Stylesheets are unaffected. The permission is part of SDK 0.12.0 (Hatamoto v0.31.0). Hosts from v0.30.0 already enforce it, whatever `sdk` range the plugin declares; this is a behaviour change that should have been announced with a notice, see [STABILITY.md](../STABILITY.md).

## Unreleased: Security hardening for self-hosters

- **Trusted proxies default to loopback only.** `X-Forwarded-*` headers are believed only from `127.0.0.0/8` and `::1`; private ranges are no longer trusted. A proxy on another address, and every Docker deployment (a host proxy reaches the container from the bridge gateway), must set `HATAMOTO_TRUSTED_PROXIES`. Until then every visitor looks like one client and the log shows a one-time warning. See [Trusted proxies](site/operate/configuration-reference.md#trusted-proxies).
- **`HATAMOTO_AGENT_RANGES=off`** stops the daily download of AI-crawler IP ranges. Crawler hits are then verified from stored ranges and reverse DNS. See [Outbound connections](site/operate/outbound-connections.md).
- **Setup code rule.** `HATAMOTO_SETUP_CODE` must be at least 12 characters and not a trivially repeated pattern. A fresh install with a weaker code refuses to start; an install that already has users only logs a warning.
- **CSRF token never in a URL.** Multipart admin forms send it in the `X-CSRF-Token` header or as the first form field; a token in the query string is never accepted.
- **Archive import and `/media`.** An imported file must match the media allowlist (files that do not are skipped with a warning), and `/media` serves types outside the allowlist as downloads in a sandbox.

## Unreleased: Plugin versions and updates in Admin > Plugins

Every plugin in the list now shows its version. When the registry has a newer one
the row gets **Update to vX**, one click, and your granted permissions stay as they
are. An update that adds permissions shows **Review update to vX** and the names it
adds, and opens the usual permission prompt. **Update all (N)** at the top updates
every plugin that needs no review, one after the other, skips the ones that do
(it names them), and stops at the first error. An update that asks for more
reach (other signer, new allowed hosts, any-address fetching, site scripts, an
older version) is never applied without your confirmation, from any button. With no
registry set up, or one that cannot be reached, the list shows no update controls.

## Unreleased: Plugin directory section

A new core section type, "Plugin directory", shows the plugins of your signed
registry (`HATAMOTO_REGISTRY_URL`) as cards inside the normal site theme: header,
footer, light and dark. Add it to any page in the builder. The registry is read on
the server with the same trust and checks as Admin > Plugins > Browse registry and
cached for 3 minutes; pages with the section are cached for at most 60 seconds, so a
publish shows within about 5 minutes. If the registry is unreachable or expired the
section shows a short unavailable message and the page still answers 200.

`hatamoto registry page` stays for operators without a CMS page, and
`make registry` builds the static page only with `REGISTRY_PAGE=1`.
`scripts/publish-registry.sh` uploads `registry/plugins/` only when it exists.
The Caddy example no longer serves `/plugins` statically: it is proxied to the app.

## Unreleased: one page model (breaking changes)

`attributes.page_doc` is now the only page body model. The v1 `page_sections` list is a frozen, read-only legacy key.
No migration; public HTML and the public pages JSON are unchanged.

* **API and MCP: 422 `page_sections_removed`.** Sending a *changed* `page_sections` for a page that has a `page_doc`
  is refused (REST `422 {"code":"page_sections_removed"}`, MCP the same sentence). Send `attributes.page_doc`. Sending
  the stored or derived `page_sections` back (read-modify-write) is accepted and writes nothing. The former 409
  `builder_layout` is gone. `page_doc` together with a different `page_sections` stores the doc and ignores the sections.
* **Convert once.** A create or update that carries `page_sections` for a page without a doc converts it to a
  `page_doc` (a deprecation warning is logged). This window ends in a later release; move integrations to `page_doc`.
* **Nothing rewrites `page_sections`.** The stored key is carried forward unchanged. The public pages JSON, plugin
  host entities and `before_save` hook input show `page_sections` derived from `page_doc` at read time. A hook that
  changes `page_sections` has the change discarded (with a warning); changes to `page_doc` are kept.
* **The classic section editor is removed.** Every page opens the builder; a page that only has `page_sections` opens
  on its in-memory conversion and is stored on first save. The `POST /admin/entities/{id}/edit` section form, its
  preview variants and `pageeditor.js` are gone. A damaged `page_doc` shows a recovery screen.
* **Section ids are not repaired.** Creating or updating a page with `page_sections` whose ids are duplicate or contain
  anything but letters, digits, `-` and `_` (max 64) now fails with 422 `invalid_page_doc` instead of being stored as
  is; send a `page_doc`. A stored page like that opens a recovery screen in the admin (`hatamoto builder convert
  --entity ID` names the problem) and is never blanked.
* **CLI.** `hatamoto builder convert --revert` is removed (exits 1). `serve` logs how many pages still lack a
  `page_doc`; run `hatamoto builder convert --dry-run --verify-render`, then `hatamoto builder convert`.
* **Seeds.** The setup wizard's home page and the starter pages are stored as `page_doc`.
* **Rollback floor: v0.27.0.** An older binary cannot read `page_doc`. Because `page_sections` is no longer updated,
  a rollback binary shows the last mirror, which can be older than the doc. Convert (`builder convert`) before relying
  on the new behaviour, and keep a backup.

## Unreleased: Webhooks are built in

Outgoing webhooks are a core feature again, reversing the webhooks half of
v1-19. Search Console stays a plugin. Receiver contract and setup:
`docs/WEBHOOKS.md`.

**What you get**

- Settings > Webhooks (owner only) to add, pause, test and delete webhooks, replace
  a secret and read the delivery log. No plugin to install or grant permissions to.
- Delivery can be faster: the job runs every 10 seconds, where the plugin's cron
  ran every minute. Body, headers, signatures and secrets are unchanged for
  receivers; delivery is still at least once.

**Upgrade steps**

1. Run the migration with `hatamoto migrate` (`202610251000_webhooks_core`). It
   creates the core tables, copies subscriptions still waiting in
   `hatamoto_plugin_handover` and deletes those rows. The table stays: v0.27.0
   still deletes from it when a plugin is uninstalled.
2. On the first start the server copies the subscriptions (ids and secrets
   included) of an installed webhooks plugin into core, then uninstalls the plugin
   with its data. If the copy fails the plugin is kept and the copy retried at
   the next start. Pending deliveries and old delivery history are not copied.
3. Rolling deploy: stop the old instances first. An event handled by an old
   instance while the new one retires the plugin may be lost: its queued
   plugin deliveries are dropped with the plugin's schema, and once the old
   instance unloads the plugin nothing listens there. Queued plugin
   deliveries are never carried over.
4. SDK surface change: the `handover.list` and `handover.ack` host functions are
   removed, and with them the exported constants `sdk.FnHandoverList` and
   `sdk.FnHandoverAck` in `hatamoto.dev/sdk`; an out-of-tree plugin that
   references them stops compiling. Only the old webhooks plugin used them.
5. Subscriptions are imported **paused** when the plugin was not really sending
   (disabled, signer no longer trusted, or not granted all of `db`, `http.fetch`
   and `http.fetch.any`), and for rows from the old
   hand-over table unless they were created within the hour before the migration
   (a direct jump from the pre-plugin core keeps them live). Resume them in
   Settings > Webhooks.
6. Only a first-party `webhooks` plugin is moved. One that is unsigned,
   third-party or signed by your own registry key stays installed and
   untouched; Settings > Webhooks says so. Recreate its subscriptions there,
   then uninstall the plugin (the name is reserved for installs only).
7. `Down` refuses to roll back while any webhook exists. A binary-only rollback
   to v0.27 stops all webhooks: subscriptions now live in core and the plugin is
   gone. Reinstalling the plugin gives an empty one; re-create the subscriptions.

**Pending contract:** the next minor drops `hatamoto_plugin_handover`
(`-- hatamoto:contract unused-since v0.27`). Nothing in this release reads it.

## Unreleased: Plugin directory

**Upgrade steps**

None. The registry index stays at version 1; the new fields are optional and
older servers ignore them.

**What changed**

- `hatamoto registry index` can add optional `title`, `author`, `source`, `commit` and
  `tier` to each entry (`--meta F`, a JSON file keyed by package name) and refuses
  duplicate names, non-https sources and commits that are not 40 hex characters.
- New `hatamoto registry page <registry.json> [--out F]` renders the signed index
  as a static, self-contained plugin directory page. It refuses an unsigned,
  altered, expired or untrusted index.
- `make registry COMMUNITY_DIR=...` also builds approved community plugins from
  pinned commits, and writes `registry/plugins/index.html`. See
  `docs/PLUGINS_WRITE_AND_SUBMIT.md` and `registry/README.md`. Registry
  operators: a plain `make registry` now refuses to run unless `COMMUNITY_DIR`
  or `FIRST_PARTY_ONLY=1` is set, so a publish cannot silently drop the
  community plugins.

## Unreleased: People replace authors

**Upgrade steps**

1. Three migrations run with `hatamoto migrate` (`202610231000`, `202610231005`,
   `202610231010`). They add columns and a table, make a person for every author
   and re-point the posts. Nothing is dropped: `hatamoto_authors` and
   `author_id` stay, so rolling back to the previous binary keeps every byline
   (`docs/UPGRADES.md`, People).
2. A site with its own content type slugged `person` must rename it first; the
   backfill refuses to run against it and says so.
3. Each user is sent once to "Complete your profile" (name and photo) at the next
   sign-in.

**What changed**

* Bylines, feeds, JSON-LD (`Person`, Article `author`), the author box and the
  author page come from people. A person is hidden everywhere public until they
  switch **Public profile** on. The site organisation can be credited instead.
* **People** screen under Entities: add, search, filter by team, merge duplicates.
* The built-in `person` type can gain fields and have its address, archive title
  and breadcrumb label changed in Settings > Entity types; its core fields are
  locked. Changing a type's address pattern writes 301s from the old paths.
* Themes: `.Entity.Author` has `.JobTitle`, `.IsOrg`, `.SameAs`; the person page
  is the `author` layout; `query` can list the team with `attr on_team`.
* REST `PATCH /api/v1/entities/{id}` and MCP `entities_update` set the byline
  (`byline_person_id`, `byline_org`).
* WordPress import creates people (not public); export/import carries
  `hatamoto_user_people`, and old archives get people on import.
* `/api/v1/authors` is deprecated and only edits the rollback mirror.

## Unreleased: Page cache controls in the admin

**Upgrade steps**

1. Migration `202610211000_page_cache_settings` runs with `hatamoto migrate`. It
   adds five columns to `hatamoto_site_config`. Defaults keep today's behaviour:
   cache on, lifetime from `HATAMOTO_PAGE_CACHE_TTL` (or 5 minutes), no size limit,
   preload off.

**What changed**

- New owner-only Settings > Performance screen: page cache on or off, lifetime, max
  size in MB, Clear cache, Preload now and a scheduled preload, with live page count
  and approximate size. Changes apply immediately. See `docs/ADMIN.md`.
- A lifetime saved in the admin wins over `HATAMOTO_PAGE_CACHE_TTL`.
- Saves, clears and manual preloads are recorded in the admin audit log.
- With `HATAMOTO_WORKER=off` scheduled preload is unavailable; Preload now works.

## v1-19: Search Console and webhooks became plugins

**Upgrade steps**

1. Migration `202610201100_webhooks_to_plugin` runs with the usual `hatamoto
   migrate`. It moves your webhook subscriptions (secrets included) to the core-only
   table `hatamoto_plugin_handover` and drops `hatamoto_webhooks` and
   `hatamoto_webhook_deliveries`. API keys lose the `read:webhooks` /
   `write:webhooks` scopes. Delivery history is not carried over.
2. Install the **webhooks** plugin (Admin > Settings > Plugins; it is in the default
   registry, signed by the built-in first-party key) and grant its permissions,
   including `http.fetch.any`. The plugin imports the subscriptions on its first
   cron run (within a minute). Until it is installed nothing is delivered, and
   events before the import are not delivered to the old subscriptions.
3. Install **searchconsole** the same way if you used Search Console, enter the
   Google client ID and secret in its settings and connect again (tokens were not
   migrated).
4. The migration's `Down` refuses to run while subscriptions are still waiting in
   `hatamoto_plugin_handover`; installing the plugin empties the table.

**For webhook receivers** (details in `examples/plugins/webhooks/README.md`)

- `X-Webhook-Signature` is unchanged, so existing verification keeps working.
  New: `X-Webhook-Signature-V2` (`t=<unix>,v1=<hex>`, HMAC of `<unix>.<body>`) for
  replay protection.
- `tenant_id` is gone from the payload; `X-Webhook-Timestamp` is now the attempt
  time (unsigned), not the scheduled time.
- Custom headers cannot override `Content-Type`, `User-Agent` or hop-by-hop headers.
- New subscriptions need `https://` (plain `http://` is a per-subscription opt-in).
- Delivery happens within a minute of the event (cron), requests time out after at
  most 5 seconds, and delivery is at-least-once: de-duplicate by `X-Webhook-Delivery`.

**For operators and plugin authors**

- New permission `http.fetch.any` for `allowed_hosts: ["*"]`; `*` only reaches ports
  80 and 443 (list `*:<port>` for others). A plugin update that adds hosts or
  `http.fetch.any` needs confirmation and clears its grants, like a signer change.
  Install and update audit entries record `allowed_hosts`.
- `http.fetch` is capped per plugin (default 600 requests a minute,
  `HATAMOTO_PLUGIN_FETCH_PER_MINUTE`).
- `HATAMOTO_TRUST_FIRST_PARTY=false` drops the built-in first-party signing key.
- `hatamoto registry index --valid-days` accepts 1 to 180 only.
- Write-only plugin settings render as password inputs that never show the stored
  value; a "clear" box (or `null` in the JSON API) removes it.
- New host functions `handover.list` / `handover.ack` (permission `kv`).
- Search Console's token encryption protects the plugin's schema, not a full
  database dump or backup (the key material lives in the same database).

## v1-15: Next admin removed (breaking API changes)

* **`POST /api/v1/auth/login`** no longer returns `tokens` (`access_token`,
  `refresh_token`). The body is `{ user, session_token, tenants }`;
  `session_token` is an opaque server-side session (30 days absolute, 7 days
  idle) sent as `Authorization: Bearer <session_token>`. With
  `ENABLE_COOKIE_AUTH=true` the HttpOnly session cookie and the CSRF cookie are
  set as well. JWTs are no longer accepted anywhere.
* **Removed**: `POST /api/v1/auth/refresh` and `POST /api/v1/auth/register`.
  Create users with `hatamoto user create` or the admin; use API keys (`nxk_` prefixed, scoped) for programmatic access.
* **`POST /api/v1/auth/logout`** revokes every session the request presents
  (bearer and cookie).
* **No CORS**: the JSON API is same-origin only. Cross-origin integrations call
  it from a server with an API key. `CORS_ALLOWED_ORIGINS` is ignored.
* **`JWT_SECRET`** is now documented as the server secret and keeps working;
  `HATAMOTO_SECRET` is the preferred name and wins when both are set. The
  derived keys are unchanged for the same value.
* **Deploy**: one Go-only image and one process (`hatamoto-api`); `hatamoto-web`,
  `frontend/` and Node are gone. The image has a `HEALTHCHECK`
  (`hatamoto healthcheck`).
