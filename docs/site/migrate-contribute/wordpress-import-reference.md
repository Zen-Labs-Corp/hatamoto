---
title: WordPress import reference
nav_title: WordPress importer
description: Every option of the WordPress import, what it converts, how repeated runs and the address check behave, and its limits.
---

This page lists everything the WordPress importer does: the `hatamoto import wordpress` command and its flags, the two site modes, what is converted, how re-runs work, the URL audit, the admin wizard's details and the limits. To run an import from the admin, follow [Import from WordPress](wordpress-import.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

There are two front doors to the importer: the admin wizard at `<admin>/settings/import/wordpress` (owners only) and the `hatamoto import wordpress` command. Both run the same engine, so a scan, an import and a re-sync behave the same way. Only one import runs at a time per site; the wizard and the command share the lock, and a second start fails with a message that one is already running.

The importer reads the source in one of two ways:

- Through the WordPress **REST API**. An application password (created in WordPress under Users, Profile, Application Passwords) lets it also read drafts, private posts, menus and settings.
- From a **WXR export file** (in WordPress: Tools, Export, All content). Media is fetched from the addresses inside the file.

## The command line

The command runs on the server and needs `DATABASE_URL`, like every operator command, and `MEDIA_UPLOAD_DIR` (default `./uploads`) for downloaded media. It applies pending database migrations first.

Put the application password in the environment, not on the command line, because arguments are visible to every user in the process list.

```bash
export WP_APP_PASSWORD="abcd efgh ijkl mnop"

# 1. Look first. Nothing is written except a run record.
hatamoto import wordpress --url https://old.example.com --user admin --scan-only

# 2. Import, then check every old URL against the new site.
hatamoto import wordpress --url https://old.example.com --user admin \
    --audit-url https://new.example.com

# From a file: media is fetched from the URLs inside it.
hatamoto import wordpress --wxr old-site.xml

# Continue after an interruption, or re-sync later.
hatamoto import wordpress --url https://old.example.com --user admin --resume <run-id>
```

Exactly one of `--url` and `--wxr` is required, unless `--resume` names an earlier run.

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--url` | URL | none | REST source: the WordPress site address. |
| `--user` | text | none | WordPress user for the application password. |
| `--app-password` | text | `WP_APP_PASSWORD` | The application password. It shows in the process list, so prefer the `WP_APP_PASSWORD` environment variable. |
| `--wxr` | file | none | WXR source file. Cannot be combined with `--url`. |
| `--scan-only` | switch | off | Print the scan report, store a run record and stop. |
| `--resume` | run id | none | Run again under an earlier run id; finished work is not repeated. `--url` and `--user` default to what that run used. A run that read a file needs `--wxr` again. |
| `--audit-url` | URL | none | A running Hatamoto site to check every old URL against. Without it the audit is skipped. The audit target may be a private or loopback address. |
| `--allow-private-hosts` | switch | off | Allow a source on a private or loopback address. Off by default, because the importer uses the same guard against requests to internal addresses as the plugin `http.fetch` function. Use it only for a site you control. |
| `--no-permalinks` | switch | off | Keep this site's URL patterns instead of copying the WordPress permalink structure. Old URLs then redirect. |
| `--keep-edited` | switch | off | On a re-run, leave alone items you edited in Hatamoto since the last import, and list them as kept. |
| `--json` | switch | off | Print the final report (or the scan report with `--scan-only`) as JSON. |
| `--media-concurrency` | number | `4` | Parallel media downloads. |
| `--max-items` | number | `50000` | Refuse sources with more posts, pages and custom post items together than this. |

The command exits non-zero when anything failed, when a `wp-content` URL is left in imported content, when an old URL could not be redirected because a live page answers there, or when the audit found a URL that does not resolve. That makes it safe to use in a script. A failed run prints the `--resume` command to continue it.

## Empty site or site with content

The importer inspects the target before writing, and the scan report states which mode applies.

- **Empty site.** There is no published content except the first-boot home page and the setup wizard's starter content. The WordPress permalink structure becomes the `post` and `page` URL patterns, and the site title, tagline, time zone, language and home page are set. Each pattern is validated like a schema save, and an invalid one is skipped with a warning.
- **Site with content.** The importer never changes a URL pattern, a site setting or the home page. It reports where WordPress differs, such as "site title is X here, Y in WordPress". Imported items take the patterns that exist, and each old URL that moved gets a 301. The wizard does not offer the URL structure choice in this mode.

In both modes a **live page always wins**. If an imported item's slug is taken (a WordPress `about` and a Hatamoto `about` in the same language), the imported item becomes `about-2`. A redirect from the old URL to the renamed item is written only when no live page answers at the old URL. Otherwise the URL is listed as unresolved in the run report and the audit, and the command exits non-zero. A redirect you created yourself is never overwritten. Redirects from an earlier run that are no longer wanted are removed on the next run. The rules every redirect follows are in [Redirect rules](redirect-rules.md).

## The scan report

A scan lists counts (posts, pages, custom post types, drafts, private items, media, terms, authors, menus and URLs in the old sitemap) and the permalink structure, including whether it can become a Hatamoto pattern. It also names:

- The SEO plugin (Yoast, Rank Math, SEOPress, All in One SEO).
- Page builders (Elementor, Divi, WPBakery, Avada, Beaver Builder, Bricks).
- Form plugins (Contact Form 7, Gravity Forms, WPForms, Ninja Forms, Formidable).
- Multilingual plugins (WPML, Polylang). Every language is imported and tagged with a locale, but only the site language is public, because one install serves one language. Over REST only Polylang is read once per language; WPML over REST is one unfiltered pass and items are tagged only when the response carries their language. A plain WPML export usually carries no language, so its items take the site language. A script-only code such as `zh-hans` is the site language when the site shares its primary language and script (`zh-CN`).
- Other plugins, listed as unsupported, and every shortcode left in a body.

The report ends with the expected fidelity: the share of items that import without loss, with each medium or low item and the reason. A page builder other than WPBakery makes an item low fidelity; removed shortcodes, forms or embeds, and private or password-protected status, make it medium. A WPBakery item is converted: if layout is its only loss it is a small loss, and if forms, other shortcodes, dropped embeds or images missing from the library come on top, it is low fidelity.

## What is imported

| WordPress | Hatamoto |
|---|---|
| Title, tagline, time zone, language | Site settings. The time zone matters because `{year}` and `{month}` are read in it. |
| Permalink structure such as `/%year%/%monthnum%/%postname%/` | The `post` schema's URL pattern. Structures with a post id, category or author cannot be patterns, so posts keep the schema pattern and every old URL gets a 301. |
| Pages (nested) | `page` entities with parents, so `/services/seo/local` stays. |
| Posts | `post` entities. Categories become hierarchical terms and tags become terms. |
| Custom post types | A schema per type, created when missing, with its URL structure and archive. |
| Authors | People (`person` entities), **not public** until the owner switches Public profile on. |
| Media library | Media, downloaded through the internal-address guard and deduplicated by content hash. Every size variant maps to the original. |
| Body | The `content` attribute, sanitised by the rich-text policy. |
| SEO title, description, canonical, noindex, social image | `meta_title`, `meta_description`, `seo.canonical`, `no_index`, `og_image`. |
| Menus | Menus with nesting. Pages become entity links. |
| Private, protected and draft content | Drafts, listed in the report. |
| Comments (WXR file only) | Status kept (approved, pending, spam, trash), threading from `comment_parent` (deeper than the maximum depth attaches to the deepest allowed reply), original dates, and commenters from name and email with trust capped at *known*. Author website URLs are dropped, IP addresses are stored only as hashes and the user agent is not imported. Bodies become comment text; empty or over-16 KiB comments, comments without a usable email, and pingbacks and trackbacks are skipped and listed or counted. Re-runs skip comments already imported. **Importing never turns comments on.** |
| Contact Form 7 forms (WXR file only) | Hatamoto forms with the WordPress slug (`-2`, `-3` added when the slug is taken). See [Forms](#forms-contact-form-7). |
| ACF field groups and values (WXR file only) | New fields on custom types, and values on every item. See [Custom fields (ACF)](#custom-fields-acf). |

Body conversion strips Gutenberg comments, keeps YouTube and Vimeo embeds, removes form markup and shortcodes (and reports them rather than guessing; a Contact Form 7 shortcode of an imported form is replaced by `[form slug=...]` instead), turns `[caption]` into a figure and drops any other iframe. No `wp-content` URL remains in imported content: images and files are rewritten to their new media URL, and the run checks the database at the end. Internal links such as `/2024/05/hello/` or `?p=12` become entity links, so they follow later renames.

In the wizard, switching **Download images and files** off copies no file, and images and attachments are left out of the text.

## Custom fields (ACF)

A WXR file from a site with Advanced Custom Fields carries the field group definitions and the values. The importer reads both. A live (REST) import brings no ACF values, and ACF options pages are not imported.

Each field is mapped like this:

| ACF type | Hatamoto field | Stored value |
|---|---|---|
| Text, password, colour picker | Text | The string. |
| Textarea | Long text | The string. |
| WYSIWYG | Rich text | HTML with `wp-content` URLs rewritten, sanitised by the rich-text policy. |
| Email | Text (email format) | The string. |
| URL, oEmbed | Text (URL format) | The string. |
| Link | Text (URL format) | The link's URL. The link title is dropped and reported. |
| Number, range | Number | The number. An unreadable value is kept as `<name>_raw`. |
| True / false | Switch | True or false. |
| Select, radio, button group | Text with fixed choices | The chosen key. A value outside the choices is kept as `<name>_raw`. |
| Multi select, checkbox | List of text | The chosen keys. |
| Date picker | Date | `YYYY-MM-DD`. An unreadable value is kept as `<name>_raw`. |
| Date time picker | Date and time | RFC 3339, read in the site time zone from the export (UTC when it has none). |
| Time picker | Text | The raw time, such as `14:30:00`. |
| Image, file | File URL | The URL of the imported media file. |
| Gallery | List of file URLs | The URLs of the imported files. Captions are not carried. |
| Relationship, post object, page link | List of entries | The imported entries, linked in a second pass once every item exists. |
| Group | Group of fields | An object. |
| Repeater | Repeating rows | A list of rows. |
| Flexible content | Repeating rows | A list of rows, each with a `layout` key that names its layout. |
| Repeater or flexible content inside a row | Long text | Readable text. The report lists it as Partly. |
| Message, tab, accordion | None | These only lay out the editor. |
| Any other type | Text | The raw string, with a warning that names the type. |

What to know:

- Fields are added to custom types only. An existing field is never removed or retyped. A field whose name clashes with an existing field of another type is skipped and reported.
- A field that you deleted in Hatamoto after an import is not added back by a re-run.
- Built-in types cannot gain fields. On posts and pages the values are kept as data on the entry, and the report shows a Partly line such as "values kept as data". They show in exports and templates but have no editor field.
- Flexible content becomes a list of rows with a `layout` key. Page builder sections are not created from it.
- Names are cleaned to letters, digits and underscores. A name that clashes with a core field, such as `content` or `title`, gets an `acf_` prefix.
- Schemas have a size limit. When a type would pass it, the remaining fields are left out and listed.
- Without field group definitions in the export, field types are guessed from the values, and a warning says so.

## Forms (Contact Form 7)

Each Contact Form 7 form in a WXR file becomes a Hatamoto form, created before the content, with its WordPress slug. The form is active.

| Contact Form 7 | Hatamoto |
|---|---|
| text, email, url, tel, number, date, textarea | The matching field type. `tel` becomes phone. A `*` marks the field required. |
| select, radio, checkbox | A choice field with the same options. |
| file | A file field. Allowed types and the size limit are read from the tag and clamped to what Hatamoto allows. |
| acceptance | A consent field. |
| hidden | A hidden field with a fixed value. |
| submit | The submit button text. |
| quiz, captcha, other tags | Dropped, and listed in the report. |

Mail settings and notifications are not imported, and one report line says so. Set the recipient under Forms. Conditional add-on rules are dropped and listed. A form that cannot be built is reported as an error, and the rest of the import goes on.

## The status lines

The report groups ACF and form results under "Custom fields (ACF)" and "Forms (Contact Form 7)". Each line has a status:

| Status | Meaning |
|---|---|
| Mapped | A field or form was created as it was in WordPress. |
| Partly | It came across with a loss, such as values kept as data or a nested repeater turned into text. |
| Imported | Values or forms were written. |
| Skipped | Nothing was written, for example a field that clashes with an existing one. |

The first line reads "Run N", where N counts the runs of this source. A run that finds nothing to do reads "Run N: nothing new, 0 changed".

## Re-runs and re-sync

Running the import again changes nothing that is already up to date. Each run is a row of `hatamoto_import_runs`, and the id of everything imported is kept in `hatamoto_import_map`, scoped by site. A re-run updates the same rows. Entities are updated in place, media with the same source id or bytes is reused, and a person you edited is left alone.

Forms and ACF fields are kept in the same map, by kind `form` (keyed by the WordPress post id) and `acf_field` (keyed by the ACF field key). A re-run creates no form and adds no field, and reports 0 changed. A form you deleted in Hatamoto is created again. A form you edited is never overwritten. A field you deleted is not added back.

Content edited in Hatamoto after the import is overwritten by the next sync, so re-sync only before cutover. The report lists every overwritten item. With `--keep-edited` (in the wizard: **On a re-import, leave pages alone that I edited here since the last import**) those items are left alone and listed as kept.

## The URL audit

After the import, every URL of the old sitemap is requested on the new site without following redirects. A URL passes when it answers 200, or answers one 301 or 308 to a URL on the same site that answers 200. A 302, a chain, a 404, a redirect to another host or a redirect to nowhere fails and is listed. Media files (`wp-content` paths) are skipped. For a WXR file, the links of the published items are used.

## The admin wizard

The wizard lives at `<admin>/settings/import/wordpress`. It has five plain pages that work without JavaScript:

1. **Source**: the site address with a username and application password, or an uploaded WXR file up to 256 MB.
2. **Scan report**: the same report as above, run as a background job.
3. **Options**: which post types, drafts and menus, media on or off, whether to keep this site's URL patterns, and whether to keep items edited since the last import.
4. **Run**: a background job with live progress. **Stop the import** cancels it and keeps what was imported, and a run interrupted by a restart offers Resume.
5. **Report**: errors, warnings, redirects, the audit and the pages to review, each linking to its edit screen.

The application password is sealed (encrypted) in the database so a run can resume after a restart, and it is deleted when the run completes. Unfinished runs keep it, and their uploaded file, for 7 days at most. The wizard cannot lift the private-host guard, so a source on a private address must be imported from the command line with `--allow-private-hosts`.

## Limits and errors

| Limit or message | Meaning |
|---|---|
| WXR file over 512 MB (command) or 256 MB (wizard) | Refused. Export fewer content types, or import from the site's address. |
| More than 50 000 items, or `--max-items` | Refused before anything is written. |
| WXR nested deeper than 64 levels or with more than 20 million elements | Refused as malformed. |
| `use --url or --wxr, not both` | Pass one source only. |
| `--resume: the run was a file import; pass --wxr again` | Give the file again when resuming a file import. |
| `import finished with N problems (run ID)` | The report above lists the failures, leftover `wp-content` URLs, unresolved URLs and audit failures. |

What is not imported or reproduced:

- One language per install: items in other languages are stored with their locale but are not public, and get no redirects.
- Page-builder layouts are not reproduced. WPBakery content (headings, images, buttons, raw HTML, map links) is converted; for other builders the text is kept and the page is listed for review.
- Forms are not converted; recreate them with Hatamoto forms.
- WooCommerce orders and coupons are not imported, and comments come from a WXR file only. Products and event posts are ordinary custom types: they come across as plain pages (text and images, no prices, cart, stock or event dates logic).
- Sites with plain permalinks (`?p=12`) import, but their old URLs are audited from the sitemap only.

See [Redirect rules](redirect-rules.md) for what happens to old URLs at cutover, including the `legacy_wp_redirects` setting.
