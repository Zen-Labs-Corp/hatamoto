# Importing a WordPress site

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

There are two ways in: the **admin wizard** (Admin, Import from WordPress, owners only) and the
`hatamoto import wordpress` command. Both run the same engine.

`hatamoto import wordpress` copies a WordPress site into Hatamoto and keeps every old URL working.
It reads the site through its **REST API** (an application password adds drafts, private posts,
menus and settings) or from a **WXR export file** (Tools → Export → All content). It can be run again
before cutover to pick up changes: a re-sync updates what it imported and never duplicates.

```sh
# The application password goes in the environment, not on the command line
# (arguments are visible to every user in the process list).
export WP_APP_PASSWORD="abcd efgh ..."

# 1. Look first: nothing is written except a run record.
hatamoto import wordpress --url https://old.example.com --user admin --scan-only

# 2. Import. The audit checks every old URL against the new site (running at --audit-url).
hatamoto import wordpress --url https://old.example.com --user admin \
    --audit-url https://new.example.com

# From a file (no sitemap, media is fetched from the URLs inside the file):
hatamoto import wordpress --wxr old-site.xml

# Continue after an interruption, or re-sync later (same options; the id map skips finished media):
hatamoto import wordpress --url https://old.example.com --user admin --resume <run-id>
```

| Flag | |
|---|---|
| `--url`, `--user` | REST source. The application password comes from `WP_APP_PASSWORD` (preferred) or `--app-password`, which shows in the process list. Create one under Users → Profile → Application Passwords. |
| `--wxr FILE` | WXR source: read as a stream, but the parsed items are kept in memory until the import starts, so memory grows with the content. Refuses files over 512 MB, over 50 000 items, nested deeper than 64 levels or with more than 20 million elements. |
| `--scan-only` | Print the scan report and stop. |
| `--resume RUN` | Run again under an earlier run's id; `--url`/`--user` default to what that run used. |
| `--audit-url URL` | A running Hatamoto site to check every old URL against. Without it the audit is skipped (a running server clears its page cache within seconds of the import, so the pages are not stale). The audit target is your own site and may be local; the flag below does not apply to it. |
| `--allow-private-hosts` | Allow a **source** on a private or loopback address. Off by default: the importer uses the same SSRF guard as plugin `http.fetch`. It lifts the guard for every URL the source names (media, sitemaps), so use it only for a site you control. |
| `--no-permalinks` | Keep the post and page URL patterns instead of copying WordPress's; old URLs then redirect. |
| `--keep-edited` | On a re-run, leave alone items you edited in Hatamoto since the last import. Without it they are overwritten and listed. |
| `--json` | Print the report as JSON. |
| `--media-concurrency N`, `--max-items N` | Downloads in parallel (default 4); refuse sources bigger than N items. |

The command exits non-zero when anything failed, when a `wp-content` URL is left in imported content,
or when the audit found a URL that does not resolve.

## Empty site or site with content

The importer looks at the target before it writes and the scan report says which mode applies.

- **Empty site**: no published content except the first-boot home page and the setup wizard's starter
  content (recognised by their markers). The WordPress permalink structure becomes the `post`/`page` URL
  patterns (each value is validated like a schema save; one that is not valid is skipped with a warning),
  and the site title, tagline, time zone, language and home page are set.
- **Site with content**: the importer never changes a URL pattern, a site setting or the home page. It
  reports where WordPress differs ("site title is X here, Y in WordPress"). Imported posts and pages take
  the patterns that exist, and each old WordPress URL that moved gets a 301.

In both modes a **live page always wins**. When an imported item's slug is taken (a WordPress `about` and a
Hatamoto `about`), the imported item is renamed `about-2`. A redirect from the old URL to the renamed item
is written only when no live page answers at the old URL; otherwise the URL is listed as *unresolved* in the
run report and in the audit (`url_conflicts`, `audit.unresolved`) and the command exits non-zero. A redirect
you created yourself for an old URL is never overwritten (also reported). Redirects of an earlier run that
are no longer wanted (the old URL is now live, or the item is gone) are removed on the next run.

## Limits on what a source can make the importer read

Each REST collection (terms, users, media, menu items) is capped at 100 000 items and 2 000 pages;
posts, pages and custom types together at `--max-items` (default 50 000). A site that ignores `?page=`
(a cache plugin) or claims an absurd page count is stopped with an error instead of being read for ever. The
whole read is limited to 2 GiB of REST and sitemap data and 4 hours; sitemaps and WXR files cap their nesting.

## The scan report

Counts (posts, pages, custom post types, drafts, private, media, terms, authors, menus, URLs in the old
sitemap), the permalink structure and whether it becomes a Hatamoto URL pattern, the SEO plugin
(Yoast, Rank Math, SEOPress, All in One SEO), page builders (Elementor, Divi, WPBakery, Avada, Beaver
Builder, Bricks), forms (Contact Form 7, Gravity Forms, WPForms, Ninja Forms, Formidable), multilingual
plugins (WPML, Polylang: every language is imported and tagged where the source says which, and the report counts the items per language), the
other plugins found (listed as unsupported), every shortcode left in a body, and **expected fidelity**:
the share of items that import without loss, with each medium or low item and why.

## What is imported and how

| WordPress | Hatamoto |
|---|---|
| Title, tagline, time zone, language | site settings (the time zone matters: `{year}/{month}` are read in it) |
| `/%year%/%monthnum%/%postname%/`, `/%postname%/`, `/blog/%postname%/` ... | the `post` schema's URL pattern (`/{year}/{month}/{slug}`, `/{slug}`, `/blog/{slug}`). Structures with a post id, category or author cannot be patterns: posts keep the schema's pattern and every old URL gets a 301. |
| Language of an item (Polylang term, WPML postmeta, REST `lang`) | the entity `locale`. Every language is imported; only the site language is public, and items in other languages get no redirects. Over REST only Polylang is read once per language (its language list is read first); WPML over REST is read as one unfiltered pass and an item is tagged only when the response carries its language. A plain WPML export usually carries no language, so its items take the site language. A script-only code such as `zh-hans` is the site language when the site shares its primary language and script (`zh-CN`); other codes keep their own locale. |
| Pages (nested) | `page` entities with parents; the pattern becomes `/{path}`, so `/services/seo/local` stays |
| The posts page (Blog) | the post archive path and title, not a page |
| Front page | the homepage |
| Posts | `post` entities; categories as hierarchical terms, tags as terms and in `attributes.tags` |
| Custom post types | a schema per type (created when missing) with the type's URL structure and archive |
| Authors | **people** (`person` entities): name, bio and, for an avatar on the site itself, the photo, with the WordPress slug. They are **not public** (no page, sitemap entry or linked byline) until the owner switches Public profile on; posts credit them through the byline. An existing person with that slug is used as it is, never overwritten |
| Media library | media, downloaded through the SSRF guard (size cap = the media limit), deduplicated by content hash; every size (`-300x200`, `-scaled`) maps to the one original |
| Body | `attributes.content`, sanitised by the rich-text policy |
| SEO title, description, canonical, noindex, social image | `meta_title`, `meta_description`, `seo.canonical`, `no_index`, `og_image` |
| Menus | menus with nesting; pages become entity links; location from the menu name (header, footer, sidebar) |
| Private, password-protected and draft content | drafts (listed in the report) |

Body conversion: Gutenberg comments are stripped; YouTube and Vimeo embeds are kept as iframes (also from a
bare URL or an unrendered `wp-block-embed`); form markup and shortcodes are removed and reported (the
text is not guessed); `[caption]` becomes a figure; responsive-image attributes are dropped; any other
iframe is removed and reported. **No `wp-content` URL remains in imported content:** images and files are
rewritten to their new media URL, a file that cannot be fetched is removed (an image) or unlinked, and the
run's `wp_content_left` list, which must stay empty, is checked in the database at the end. Internal links
(`/2024/05/hello/`, `?p=12`) become `<a data-entity-id>` links, so they follow later renames.

Menu items whose URL is not http, https, mailto, tel, sms or relative (a `javascript:` link) are left out and
reported.

Redirects: wherever the new URL differs from the old (changed pattern, an author or term whose slug
changed, a slug made unique), a 301 from the old path is written. When the pattern is kept, none are needed.
An old author URL (`/author/jane`) is redirected to the person's page only once the person is public; until
then it has no page to go to, so nothing is written. Run the import again after switching them on, or add the
redirect by hand.

## Idempotency

Each run is a row of `hatamoto_import_runs` (options without secrets, phase, report). The source id of
everything imported is kept in `hatamoto_import_map` (scoped by site), and entities carry
`metadata.import_key`. A re-run updates the same rows: entities are updated in place (a version snapshot
only when the body changed), terms, menus and redirects are updated, authors that became people are found again by slug (a person you edited is left alone), media with the same source
id or the same bytes is reused and not downloaded again. Entities and redirects are scoped by the source
site's host **and path**, so two WordPress sub-sites on one host stay apart.

Content edited in Hatamoto after the import is overwritten by the next sync, so re-sync only before cutover.
The run report lists every such item (`overwritten_edits`); `--keep-edited` leaves them alone
(`kept_edits`). An edit is detected by comparing the entity's `updated_at` with the one the importer left.

`--resume` refuses a run that belongs to another source site.

## URL audit

After the import every URL of the old sitemap (Yoast's `sitemap_index.xml`, WordPress's
`wp-sitemap.xml`, `sitemap.xml` or the one named in `robots.txt`; for a WXR file, the links of the published
items) is requested on the new site without following redirects. It passes when it answers **200**, or
**one 301/308 to a URL that answers 200**; a 302, a chain, a 404 or a redirect to nowhere is a failure listed
in the report. Media files are skipped.

## Limits

One language per install; page-builder layouts are not reproduced (the text is kept, the page is listed);
forms are not converted; WooCommerce, events and other plugin data are not imported; comments are not
imported; Rank Math, SEOPress and All in One SEO data over REST are read from each page's `<head>`
(published items only), Yoast's from its REST field. Sites with plain permalinks (`?p=12`) import, but their
old URLs cannot be listed from posts: they are audited from the sitemap only.

## Development: the fixture

`test/wpfixture` seeds a real WordPress (nested pages, dated permalinks, Yoast, gallery, YouTube and Vimeo,
Contact Form 7, a private post, an author with avatar, a custom post type, menus) for the integration tests.
CI runs it in Docker (`test/wpfixture/docker-up.sh`, job `wordpress-importer`); without Docker,
`test/wpfixture/run-local.sh up` downloads a pinned static PHP and WordPress and serves it with `php -S`.

```sh
test/wpfixture/run-local.sh up
set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a
cd backend && DATABASE_URL=postgres://... go test -tags=integration -run WordPress -v ./app
```

The tests check, against the fixture, the done criteria: every old URL answers 200 or exactly one 301, no
`wp-content` URL remains, imported SEO titles equal the old pages' own `<title>` (100%), and a re-run adds
no row. They run once from the REST API, once from a WXR export, and once with the permalink structure left
alone so every post redirects.

## From code

```go
deps := wpimport.NewDeps(db, wpimport.DepsConfig{MediaDir: "./uploads"}, logger)
src, _ := wpimport.NewRESTSource(wpimport.RESTOptions{ClientOptions: wpimport.ClientOptions{URL: u, User: user, AppPassword: pw}})
dump, _ := src.Load(ctx)                                  // or wpimport.ParseWXR(file, wpimport.WXROptions{})
report := wpimport.Scan(dump, "scan")                     // before writing anything
runner, _ := wpimport.NewRunner(deps, wpimport.NewStore(pool, dump.Site.URL), dump, wpimport.Options{
    Progress: progress,
    Purge:    purge,                                      // your func(ctx): clear rendered pages and sitemaps (server process only)
    Audit:    wpimport.HandlerProber{Handler: srv.Handler()},
})
res, err := runner.Run(ctx, uuid.Nil)                     // a run id resumes
```

The admin import wizard is a UI over this engine (see below).

## The admin wizard

`<admin>/settings/import/wordpress`, owners only, linked from the sidebar and from the setup wizard's "Import from
WordPress" choice (which finishes setup and opens the wizard). Five steps, each a plain page that works
without JavaScript:

1. **Source**: the site's address with a username and application password, or an uploaded WXR file (up
   to 256 MB). The password is sealed (AES-256-GCM, bound to the run, key derived from the server secret) in `hatamoto_import_secrets` so a run can be resumed after a restart, and is deleted when the run
   completes. It is never in a run's options or report, never logged, never audit-logged and never sent back
   to the browser (a form that fails validation comes back without it). An uploaded file is kept under the
   system temp directory (`hatamoto-import/`, mode 0600; a directory owned by another user is refused) until
   the run completes. A run that never completes keeps them for 7 days after it last changed: a sweep (at
   start and hourly) deletes the sealed password and the file of older runs, and of uploads that belong to no
   run; scanning a site again also replaces the older runs' stored passwords. After that, resuming asks for
   the password or the file again. The key is derived from the server secret (`HATAMOTO_SECRET`, or `JWT_SECRET` under its old name): if that secret changes, stored
   passwords become unreadable and the wizard asks again.

   Only one import runs at a time on a site: the wizard's jobs and `hatamoto import wordpress` share a
   database lock, so starting a second one (a double click, another tab, the command line) is refused with
   "another import is running". Stopping an import that is still starting wins: it never runs.
2. **Scan report**: counts, permalinks, SEO plugin, builders, forms, languages, unsupported plugins, shortcodes,
   expected fidelity and the pages that will not import without loss, and whether the target is an empty site
   or a site with content, and what that changes (the two modes above). The scan is a background job too:
   a large site is read for minutes.
3. **Options**: which post types, drafts, menus; media on or off (off imports no file and leaves images out
   of the text, so no `wp-content` URL remains); keep this site's URL patterns or copy WordPress's
   structure (a site with content always keeps its own); leave edited pages alone on a re-import.
4. **Run**: a background job on the server process. The page shows the phase and recent activity and updates
   itself by htmx polling every 3 seconds; without JavaScript a `<noscript>` meta refresh reloads it. Stop
   cancels it (what was imported stays). A run that was running when the server restarted shows as
   *interrupted* and has a **Resume** button; resuming skips finished work like `--resume` does.
5. **Report**: per-item errors, warnings, the redirects created, the URL audit (run in process against this
   site's own handler, every old URL with a link), the old URLs a live page already answers, edits that were
   overwritten or kept, and the pages to review, each linking to its edit screen. **Re-sync** opens the Source
   step prefilled with the address and username (never the password).

The wizard cannot lift the engine's private-host guard: the source is fetched through the same SSRF guard as
plugin `http.fetch`, and nothing a request carries can turn it off. `--allow-private-hosts` exists on the
command line only (a site you control on a private address is imported from the CLI).

Every wizard action (scan, start, resume, cancel, completion) is written to `hatamoto_admin_audit` (who, what,
which run, the options) and to the log.

```sh
# The wizard's end-to-end test, against the fixture and a scratch database it migrates itself:
test/wpfixture/run-local.sh up
set -a; . ~/.cache/hatamoto-wpfixture/site/wpfixture.env; set +a
cd backend && DATABASE_URL=postgres://... go test -tags=integration -run WordPressWizard -v ./internal/server
```
