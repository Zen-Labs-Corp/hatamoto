# Themes: server-rendered public site

Compatibility promises for everything on this page: [STABILITY.md](../STABILITY.md).

Hatamoto can render the public site itself. The Go binary reads stored pages,
builds a `PageView` and renders it through an `html/template` theme. The result
is a complete HTML document with a small, stable set of `data-hm-*` markers so
an editor (the admin canvas) can map every visible piece of text back to the
JSON field it came from.

Rendering is always on in the `hatamoto` binary: the root HTML routes are
registered at boot next to the JSON API.

- [Directory layout](#directory-layout)
- [Per-schema layouts and entity data](#per-schema-layouts-and-entity-data)
- [Template funcs reference](#template-funcs-reference)
- [Template hierarchy](#template-hierarchy)
- [Installing and activating themes](#installing-and-activating-themes)
- [Section schemas](#section-schemas)
- [Marker contract v1](#marker-contract-v1)
- [Theme endpoint](#theme-endpoint)
- [Preview endpoint](#preview-endpoint)
- [Plugins](#plugins)
- [Writing and submitting a theme](#writing-and-submitting-a-theme)
- [Interactivity](#interactivity)
- [Headless mode](#headless-mode)
- [Known gaps](#known-gaps)

## Directory layout

```
my-theme/
  theme.json            name, version, settings_schema, optional screenshot
  layouts/
    base.html           the document shell; defines blocks
    page.html           defines "content"; the last fallback of every kind
    single.html         optional: a single entity; single-<schema>.html for one schema
    home.html           optional: the homepage
    archive.html        optional: a schema archive; archive-<schema>.html for one schema
    archive-release.html  shipped in the default theme: the changelog, see below
    taxonomy.html       optional: a category/tag page; taxonomy-<kind>.html for one kind
    author.html         optional: an author page
    search.html         optional: /search
    registry.html       optional: a plugin or theme detail page, see Routes
    404.html            defines "content" for not-found
    error.html          defines "content" for 5xx
  partials/
    head.html header.html nav.html footer.html section.html scripts.html
    plugin-assets.html  optional: the plugin-head / plugin-body slots (default theme has them)
  sections/
    hero.html           defines "section-hero"
    faq.html            defines "section-faq"
    ...
  schemas/
    hero.json           JSON Schema (subset) for the section's content
    ...
  assets/
    css/base.css        bundled first, then every other css/*.css (wp-compat.css, blog.css, docs.css), then css/sections/*.css
    css/sections/hero.css
    js/hm.js
    sitemap.xsl         (optional) stylesheet for /sitemap.xml
    screenshot.webp     (optional) the preview image named by theme.json "screenshot"
```

Rules the engine enforces when it loads a theme (a theme that breaks them is
rejected; the previous good version keeps serving):

- `layouts/base.html`, `page.html`, `404.html`, `error.html` (the embedded default theme supplies the rest), and the partials `head`, `header`,
  `nav`, `footer`, `section`, `scripts` must exist.
- **Smoke render.** An installed theme that loads is also trial-rendered
  against a fixture view of every layout kind (page, single, home, archive,
  taxonomy, author, search, registry, 404, error). If any of those fails to execute (a
  partial called with the wrong argument, a missing field), the error is logged
  and the engine serves the embedded **default theme** instead of returning a
  500 on every page. Installing or activating a theme runs the same check up front
  and refuses one that fails it, so this is only a safety net. Files that
  fail to *parse* still reject the theme as above.
- A file in `layouts/` that is not a layout name (an old `layouts/<schema>.html`)
  is logged as a warning and ignored.

**Partial contract (compatibility).** The default partials `nav`, `nav-items`
and `nav-link` accept both call styles, so overrides written for either keep
working: `{{template "nav" .Site.Nav}}` / `{{template "nav-link" .}}` (a
`[]NavItem` / `NavItem`) and `{{template "nav" dict "Items" .Site.Nav "Path" .Path}}`
/ `{{template "nav-link" dict "Item" . "Path" $.Path}}`. The dict form marks the
link for the current path `aria-current="page"`. In an override use the
`navctx` and `navlink` funcs to normalise either shape.
- Every `sections/<type>.html` must `{{define "section-<type>"}}`. `<type>` is
  `[a-z][a-z0-9_]*`. A section type is available when its template exists;
  its schema is `schemas/<type>.json`.
- Templates are `html/template`: values are escaped for their context. Do not
  build `data-hm-*` attributes by hand; use the helper funcs below.

A section template receives the prepared section as dot:

| Field | Meaning |
| --- | --- |
| `.Content` | the section's `content` map, sanitised, with `x-hm-alias` names mapped to canonical names |
| `.ID`, `.Type`, `.Title` | stored section identity |
| `.Page` | `.Forms` (form definitions by slug), `.Path`, `.Submitted` |

Template funcs available to every theme: `hmsec`, `hmf`, `hment` (markers),
`cta`/`safeurl`/`safeHref` (validated hrefs; `http`, `https`, `mailto`, `tel`,
`sms` and relative only), `telHref`, `imgsrc`, `embedAllowed`, `dict`, `list`,
`seq`, `join`, `default`, `hasPrefix`, `year`, plus `sanitisedHTML`,
`formatDate` and `readingTime` (see
[Per-schema layouts and entity data](#per-schema-layouts-and-entity-data)),
the arithmetic, string and JSON funcs, `sanitisedSectionHTML`, `pageNum`, `queryParam`,
`query` and `attrValues` (see [Template funcs reference](#template-funcs-reference)).

## Per-schema layouts and entity data

A theme can render an entity from its own data instead of from
the page's sections, for example a blog post.

**Layout selection.** See [Template hierarchy](#template-hierarchy). A
schema-specific layout is `layouts/single-<schema-slug>.html` (the old
`layouts/<schema-slug>.html` is no longer read). The name must match
`[a-z][a-z0-9_-]*`; it must `{{define "content"}}` like the other layouts, and a
broken one rejects the theme version like any layout. A schema slug that is not
a valid name falls back down the chain. The slug is only used as a lookup key,
never to build a path.

**Layout data.** Besides the fields above, layouts of entity pages see:

| Field | Meaning |
| --- | --- |
| `.Attrs` | `map[string]any` deep copy of the entity's attributes, without `page_sections` (already in `.Sections`). Numbers are `float64`. Strings are plain text: the template escapes them. Missing keys read as empty |
| `.SchemaSlug` | the entity's schema slug |
| `.PublishedAt`, `.UpdatedAt` | `time.Time`; zero when unknown |
| `.Related` | `func(limit int) []RelatedItem`, see below |
| `.Series` | `func() *SeriesView`, see Series below |
| `.PostsByAuthor` | `func(limit, page int) PostsPage`, see Posts by author below |
| `.PageNum` | `int`: the request's `?page=N`, 1 when absent or not a positive integer, clamped to 1..1000 |

Authors: `.Entity.Author` is the entity's **byline**: the person it credits
(`byline_person_id`, an entity of the `person` type; see Authors below), the
site organisation (`byline_org`: `.IsOrg`, `.Name` is the site name, no `.URL`),
else a plain `.Attrs.author` / `.Attrs.author_name` string (no link, no bio); it
is never the account that created the entity. `.Entity.Author.URL` is set only
when the person has made their profile public and the entity's schema has an
archive (see Routes), otherwise the name is shown unlinked. `author_entity_slug`
stays raw; core does no lookup.
Drafts never reach `.Attrs` on the public site; the admin preview fills it from
the posted attributes.

**Funcs.**

- `sanitisedHTML v`: rich text (a TipTap body such as `.Attrs.content`) run
  through the rich-text sanitiser, with `data-hm-*` stripped. This is the only
  way attribute HTML is emitted unescaped. A missing or non-string value gives
  an empty string.
- `formatDate t layout`: `t` is a `time.Time`, `*time.Time` or an
  RFC3339 / `2006-01-02` string; `layout` is a Go time layout
  (`"2 January 2006"` gives `13 October 2026`). Zero or unparseable values give
  `""`. Month and weekday names are English: Go has no localised names.
  Dates print in the value's own zone, which is UTC for stored timestamps, so
  a post published at 22:00Z on the 13th prints the 13th. Pass an optional
  IANA zone as a third argument to convert first:
  `formatDate .PublishedAt "2 January 2006" "Australia/Sydney"` gives
  `14 October 2026` (an unknown zone is ignored). Use this to match
  `toLocaleDateString('en-AU')` output in a browser or server set to Sydney time.
- `readingTime v`: whole minutes (200 words per minute) of an HTML string's
  text, at least 1 for any text and 0 for none. Add your own " min read".

**Related.** `{{range call .Related 3}}...{{end}}` lists other published
entities of the same schema, newest first, excluding the current page. The limit
is clamped to 1..12. It runs lazily: pages that do not call it run no query, and
the result is cached with the page. Each item is a `RelatedItem`, the card view
shared by `recent`, `related`, `query` and the archive, taxonomy, author and
search listings: `Title` (`attributes.title`, else the entity name), `Name` (the
entity's own name), `URL`, `Excerpt`, `Image`, `Category` (strings),
`PublishedAt` (`time.Time`), `Author` and `AuthorURL` (set by listings and
`query`; empty on `related`), `WordCount` and `ReadingTime` (of
`attributes.content`, counted like `readingTime`; 0 without content) and `Attrs`
(a read-only `map[string]any` copy of the entity's attributes **without**
`content` and `page_sections`, so a card stays light; numbers are `float64`,
strings are plain text the template escapes).
`.Related` is never nil in a layout: it
returns nothing on archive, 404, error and unsaved-preview views, so `call` is
always safe, including in a partial shared with those layouts. The query runs
at most once per render, however many times or with whatever limit it is
called. If it fails, the page is served without the block but is not cached.

- *Staleness.* Rendered pages are cached for `HATAMOTO_PAGE_CACHE_TTL` (default
  5 minutes, or the lifetime saved in Admin > Settings > Performance) and every cached page is cleared on entity create, update,
  publish, unpublish and delete, including a scheduled publish by the worker, so
  the list is current as soon as the change is saved (see
  [Page cache](#page-cache)).
- *Scope.* The list is not filtered beyond the schema (and its aliases): it
  suits schemas such as blog posts.

**Series.** `{{with call .Series}}...{{end}}` returns the editorial series
named by the entity's `series_slug` attribute, or nil (no `series_slug`, or no
such posts). It holds what a series listing
returned: `Slug`, `Name` (the `series` attribute), `Total` (the `series_total`
attribute: numbered parts, the index not counted), `IndexURL` (empty until the
index is published) and `Parts` ordered by part number. Each part has `Title`,
`URL`, `Part`, `Index` (part 0, the series index page), `Published` and
`PublishDate` (`time.Time`). Published **and scheduled** posts of the entity's
schema (and aliases) are listed so an index can show what is coming, but only
published parts have a `URL`: guard links with `{{if .Published}}`, never link
a part with an empty `URL`. Same laziness, memoisation and failure rule as
`.Related` (a failed query serves the page uncached). Never nil in a layout.

**Posts by author.** `{{with call .PostsByAuthor 12 .PageNum}}...{{end}}` on a
team-member entity page lists one page of published `blog` posts (schema and
aliases) written by that member, newest first. `limit` is clamped to 1..100 and
`page` to 1..1000; pass `.PageNum`. It returns `Items` (`RelatedItem`s, same
fields and rules as `.Related`; `Author` and `AuthorURL` are empty), `Total`
(all matches), `Page` and `Pages` (at least 1). A post is by the member when
its `author_entity_slug` equals the entity's slug; else when `author` is an
object whose `slug` equals the slug or `name` equals the entity's name; else
(no author object) when `author` is a string, or `author_name`, equals the
entity's name. Pagination links are yours to build (`?page=2`). It is lazy and
memoised per (limit, page) per render; a failed query serves the page uncached.
A page past the last one gets empty `Items`. Only the call whose
page is the request's own `.PageNum` (2 or more) makes `?page=N` a cache key,
and a `.PageNum` past the last page is a quiet 404, as for a paged `query`
loop; a constant page (a sidebar) never does.
Without an entity slug it returns an empty page.

```gotemplate
{{define "content"}}<article>
  <h1>{{.Attrs.title}}</h1>
  <time>{{formatDate .PublishedAt "2 January 2006"}}</time>
  {{readingTime .Attrs.content}} min read
  {{sanitisedHTML .Attrs.content}}
  {{range call .Related 3}}<a href="{{.URL}}">{{.Title}}</a>{{end}}
</article>{{end}}
```

## Template funcs reference

Every func is generic (nothing here knows a site, a language or a schema) and
fail-open: bad input gives a zero value, never a failed render. None returns
trusted markup except `sanitisedHTML` and `sanitisedSectionHTML`, so
`html/template` escapes every other result.

**Arithmetic** (integers; arguments may be ints, floats (truncated) or numeric
strings, anything else counts as 0; operands are clamped to about ±2·10⁹ so a
result never wraps): `add a b`, `sub a b`, `mul a b`, `div a b` (truncates
toward zero), `mod a b`. **`div` and `mod` by zero give 0.**

**Strings.** The subject comes first, like `hasPrefix`. Numbers are accepted
where a string is expected; other values (`nil`, maps) read as `""`.

| Func | Result |
| --- | --- |
| `lower s`, `upper s` | case-mapped string |
| `trim s` | surrounding whitespace removed |
| `trimPrefix s p`, `trimSuffix s p` | `s` without the prefix / suffix when present |
| `contains s sub`, `hasSuffix s p` | `bool` (`hasPrefix s p` already exists) |
| `replace s old new` | every `old` replaced by `new` |
| `split s sep` | `[]string`, at most 1000 parts (an empty `sep` splits into characters) |
| `atoi v` | `int` from a string, number or `nil`; anything unparseable is `0` |

**`fromJSON s`** decodes a JSON string (for example the text a plugin template
func returns, which is always a string) into `map[string]any`, `[]any`,
`string`, `float64`, `bool` or `nil`. Input over 1 MiB, empty input and invalid
JSON give `nil`. `{{with fromJSON (plugin_x_rows .Path)}}{{range .}}...{{end}}{{end}}`.

**`pageNum .`** is the request's `?page=` as an `int`: 1 by default, clamped to
1..1000, on every view (pass `.` or `$`; inside a section pass its own `.`, which
carries the page number too). Pair it with `query`:
`query (dict "schema" "blog" "page" (pageNum .))`. A request whose only query
is `?page=N` (N 2..1000) is cached per page when a paged `query` or
`.PostsByAuthor` reads it; any other query string is never served from or
stored in the page cache.

**`queryParam . "name"`** is the first value of the request's query parameter
`name` as a string, cut to 200 bytes, `""` when absent. It works on every view
and, with the section's `.`, inside sections. It is only read: a request with a
query string is never served from or stored in the page cache, so a theme can
branch on it (for example `?days=7`) without a cache key. Never put the value
in markup unescaped (html/template escapes it).

**`query dict`** lists published entities on any view (an archive is not
required) and returns `{Items []RelatedItem, Total, Page, Pages, Limit int}`.
Only `schema` is required:

| Key | Meaning |
| --- | --- |
| `schema` | schema slug (its aliases are included) |
| `attr` + `eq` | `attributes[attr]` equals `eq`: a string, or a number or boolean spelled the same (`"eq" "3"` matches `3`). `attr` without an `eq` key requires the attribute to exist. With an `eq` key the test is always equality: `"eq" ""` matches the empty string only, `"eq" true` the JSON boolean, and a nil `eq` (for example `.Attrs.missing`) matches nothing, so guard optional values with `{{with}}`. Keys are `[A-Za-z0-9_-]`, at most 64 |
| `author` | person slug (a `person` entity; see Authors). A person who is not public matches nothing |
| `term` | `"<kind>:<slug>"`, e.g. `"category:news"` |
| `order` | `-published_at` (default), `published_at`, `name`, `-name`, `attr:<key>`, `-attr:<key>`. Attribute order sorts numeric values as numbers (so part 2 precedes part 10), then other text; entities without the attribute come last |
| `limit` | 1..100, default 12 |
| `status` | `published` (default) or `published+scheduled`: scheduled items are listed for a "Publishing <date>" row and carry only `Name`, `Title`, `Schema`, `PublishedAt` and, in `Attrs`, just the `attr` filter key and the `attr:<key>` order key (so a series part number is available). `URL`, `Excerpt`, `Image`, `Category`, `Author`, `AuthorURL`, `WordCount`, `ReadingTime` and every other attribute are empty, so embargoed content never leaks. Drafts and archived items never appear; any other value means `published` |
| `page` | 1..1000 and at most `Pages`: a page past the end returns the last page |

An unknown `author`, a `term` that does not exist or is malformed, an invalid
`attr` key and an unknown schema give an empty result (never an unfiltered
one), with `Page` and `Pages` 1. Items are base published rows only (never
drafts, archived or variant rows; scheduled ones only with `status`) and carry the `RelatedItem` fields above,
including `Author`. Cost: one `COUNT`, one `SELECT` of the page, one batched
author lookup and, only for schemas whose URL pattern uses `{path}` or
`{parent}`, one batched ancestor lookup, whatever the limit (no query per item).
A section that calls `query` runs it on each of the section's two executions,
and the result is not memoised per render: call it once and keep it in a
variable (`{{$r := query ...}}`) rather than repeating the same call. A failed
ancestor lookup counts as a failed query.
Attribute filters use a JSONB containment test, which the GIN index on
`attributes` serves. A failed or timed-out query returns no items and the page
is served but not cached. The result is not cached beyond the page cache.

```gotemplate
{{with .Attrs.series_slug}}
{{$r := query (dict "schema" "post" "attr" "series_slug" "eq" . "order" "attr:series_part" "limit" 20 "page" (pageNum $))}}
{{range $r.Items}}<a href="{{.URL}}">{{.Attrs.series_part}}. {{.Title}} ({{.ReadingTime}} min)</a>{{end}}
{{if lt $r.Page $r.Pages}}<a href="?page={{add $r.Page 1}}">Next</a>{{end}}
{{end}}
```

**`attrValues schema attr limit`** is the distinct non-empty string values of an
attribute across a schema's published entities, as `[]{Value string; Count int}`
sorted by value (`limit` 1..500). Entities where the attribute is missing, empty
or not a string are ignored. One aggregate query.
`{{range attrValues "post" "category" 50}}{{.Value}} ({{.Count}}){{end}}`.
Arguments are converted like `query`'s (a number from JSON works as the limit).

**`sanitisedSectionHTML v`** runs an HTML string through the policy html
sections are sanitised with on save, and emits it unescaped. Use it for markup
a plugin func built (`sanitisedHTML`, the rich-text policy, drops `id`, `style`,
`aria-*` and `data-*`, which such fragments need). It keeps `class`, `id`,
`role`, `style` (rejected when it holds `expression()`, `javascript:`,
`@import`, a non-image `url()` ...), a fixed set of `aria-*`, `data-*`, the
section element set (containers, `details`, `button`, inline `svg`) and
links with `http`, `https`, `mailto`, `tel`, `sms` or relative targets. It
removes `script`, `style` blocks, forms, event handlers, `javascript:` URLs,
`data-hm-*` (renderer markers; content must not spoof them) and iframes off the
allowlist below. A non-string gives `""`.

**Iframe embed hosts.** Both sanitiser policies (rich text and html sections,
on save and in `sanitisedHTML` / `sanitisedSectionHTML`) and `embedAllowed`
keep an `<iframe src>` only for https URLs on an allowed host.
`HATAMOTO_EMBED_HOSTS` is a comma-separated list of host names (no scheme,
port or wildcard). Unset, the default is today's: `youtube.com`,
`www.youtube.com`, `youtube-nocookie.com`, `www.youtube-nocookie.com` (path
`/embed/`) and `player.vimeo.com` (path `/video/`). A set list **replaces** the
default, and the editor's embed tool and the WordPress importer only ever
produce YouTube-nocookie and Vimeo addresses, so **always list those hosts
again** if editors embed videos, or existing videos disappear at the next save
and at render; the stock hosts keep their
path restriction and any other listed host may be embedded at any path.
Invalid entries are logged and ignored, and a list with no valid entry leaves
the default, so a typo never opens the policy. The host must be followed by `/`
in the URL, so userinfo (`host@evil`), ports and look-alike suffixes
(`host.evil.test`) do not match. Security reasoning: an iframe is the one
element that can run third-party script inside the page, so the allowlist is
the only gate; it is operator configuration (environment), not editable content,
and it applies at the write boundary and again at render time. `embedAllowed`
additionally accepts Google Maps and OpenStreetMap embeds. Add a map host to the
env list to let editors paste it in content. Changing the variable needs a
restart; content saved earlier is not rewritten, but it is re-sanitised at
render when emitted through the funcs above.

**`navPos items path`** locates a page in a menu tree:
`func(items []NavItem, path string) NavPosition`. `NavPosition` has `.Trail`
(the ancestors, root first, without the page), `.Current`, `.Prev` and `.Next`
(`*NavItem`, nil when absent). Neighbours follow depth-first menu order and skip
items without an `Href`. Paths and hrefs compare after dropping a scheme and
host, the query, the fragment and a trailing `/`. A path not in the menu, or an
empty menu, gives the zero value, so layouts need no guard beyond `with`.

```
{{$pos := navPos (menu "sidebar") .Path}}
{{range $pos.Trail}}{{.Label}} / {{end}}{{with $pos.Current}}{{.Label}}{{end}}
{{with $pos.Next}}<a rel="next" href="{{.Href}}">{{.Label}}</a>{{end}}
```

**`docBody body`** takes the already sanitised `.Body` and returns a `DocBody`:
`.HTML` (the body with a unique `id` on each `h2` and `h3`) and `.TOC`, a list of
`TOCItem` with `.ID`, `.Text` (plain text, escaped by the template) and `.Level`
(2 or 3). Ids are a lowercase ASCII slug of the heading text; repeats get `-2`,
`-3`; an empty slug becomes `section`; `main-content`, `toc` and `top` are never
used. An `id` the heading already has is kept. Empty input gives the zero value.

```
{{$b := docBody .Body}}
<div class="hm-prose">{{$b.HTML}}</div>
<ol>{{range $b.TOC}}<li><a href="#{{.ID}}">{{.Text}}</a></li>{{end}}</ol>
```

The ids are added when the page renders and are never stored: the rich-text
sanitiser still drops `id` on save, and only layouts that call `docBody` emit
them.

## Template hierarchy

The engine picks the first layout that exists, trying the candidates in order:

| View | Candidates |
| --- | --- |
| entity page | `single-<schema>` → `single` → `page` (a `doc` entity uses `single-doc`) |
| homepage | `home` → `page` |
| schema archive (`/blog`) | `archive-<schema>` → `archive` → `page` |
| term archive (`/category/x`, `/tag/x`, `/genre/x/y`) | `taxonomy-<kind>` (`category`, `tag` or a custom kind) → `taxonomy` → `page` |
| author (`/author/x`) | `author` → `page` |
| search (`/search?q=`, optional `&type=<schema slug>`) | `search` → `page` |
| registry detail page (`/plugins/<name>`, `/themes/<name>`) | `registry` → `page` |
| not found / error | `404` / `error` |

The default theme ships `archive-release.html`, a changelog layout for a schema
with slug `release` and an `archive_path`. It lists up to 100 entries inline
(not the 12-per-page archive), newest first by the numeric `version_sort`
attribute, each as `<section id="{version}">`. Attributes: `version`,
`version_sort`, `released_at` (`YYYY-MM-DD`), `channel`, `yanked`, `summary`
and the string arrays `features`, `fixes`, `docs`, `chores` (`docs` and
`chores` share one "Docs / Chores" heading). A group saved as a single string
is rendered as sanitised rich text instead of a list.

A theme may omit any of them. Lookup tries all candidates in the highest
layer of the [lookup chain](#installing-and-activating-themes) (the active
theme) before moving to the next layer, ending at the embedded default theme,
which ships every layout. So a theme that only ships `page.html` renders posts
and archives through its own `page.html`, not the default's `single.html`.

**View data.** Every layout receives the fields above plus:

| Field | Meaning |
| --- | --- |
| `.Kind` | `single`, `home`, `archive`, `taxonomy`, `author`, `search` or `page` |
| `.Entity` | entity pages: `.Title`, `.Slug`, `.URL`, `.Schema`, `.Excerpt`, `.Image`, `.Attributes` (same as `.Attrs`), `.Terms` (plus `.Categories`, `.Tags`), `.Author`, `.PublishedAt`, `.UpdatedAt`. Nil on listings |
| `.Body` | `attributes.content` sanitised with the rich-text policy **at render time** (stored content is never trusted); empty when there is none. A post with only `attributes.content` renders from this |
| `.Blocks` | the prepared sections of the page, derived from its `page_doc` (a page without a doc: its legacy `page_sections`) |
| `.Items` | listings: cards with `.Title`, `.URL`, `.Excerpt`, `.Image`, `.Category`, `.PublishedAt`, `.Author`, `.AuthorURL`. An excerpt falls back to the first 200 characters of the content |
| `.Pagination` | listings: `.Page`, `.TotalPages`, `.Total`, `.HasPrev`, `.HasNext`, `.PrevURL`, `.NextURL`, `.URL n` |
| `.Term`, `.Author`, `.Query`, `.SearchType` | taxonomy (`.Kind`, `.Slug`, `.Name`, `.URL`, `.Description`, `.Parent`), author (`.Name`, `.Slug`, `.URL`, `.Avatar`, `.Bio`, `.JobTitle`, `.IsOrg`, `.SameAs`, a list of profile links) and search views (`.SearchType` is the schema slug a search is limited to, empty for the whole site) |
| `.Site` | `.Name`, `.Tagline`, `.LogoURL`, `.Language` (the site setting `language`, BCP 47, default `en`; also `<html lang>` and JSON-LD `inLanguage`), `.Phone`, `.Hours` and `.Address` (`.Street`, `.City`, `.State`, `.PostalCode`, `.Country`; the site settings, also emitted in the Organization JSON-LD), `.Settings` (the theme settings object), `.Nav`, `.FooterNav`, `.SocialLinks` |

**Authors** are people (the built-in `person` content type: name, job title,
bios, photo, links), not accounts. An entity credits one through
`byline_person_id`, or the site organisation through `byline_org`; saving an
entity whose `attributes.author` (else `author_name`) text names nobody creates
a person from it (not public, no login), and text equal to the site name credits
the organisation. A person's page is its own entity page, by default at
`/author/{slug}` (the type's `url_pattern`), and renders with the `author`
layout: the profile and their published posts (a profile with no posts still
renders). A person is hidden everywhere public (page, sitemap, llms.txt, search,
`query`) until they switch **Public profile** on; their byline then shows the
name without a link. No account email or login is ever exposed: only the
person's own `public_email` is. Renaming a person rewrites the plain byline on
their posts. `hatamoto_authors` and `author_id` are kept only so a rollback
still renders bylines; nothing reads them to render.

**Person pages.** The person's entity page renders with the `author` layout and
`.Kind` `author`. `.Entity` is the person (their own `.Attrs`, including any
field the site added to the `person` type, such as `.Attrs.pronouns`); `.Author`
is their `AuthorView`: `.Name`, `.Slug`, `.URL` (the page itself, from the
type's `url_pattern`), `.Avatar` (the `photo`), `.JobTitle`, `.Bio` (the
`short_bio`, else the first 2000 characters of `bio` as plain text; a byline
card on a post gets 300), `.SameAs`
(website, LinkedIn, X, GitHub in that order, empty ones left out). `.Items` and
`.Pagination` list their published posts, limited to schemas with an archive,
12 per page; there are no Page Builder sections. The person's address, archive
title and breadcrumb label are the type's settings (Admin > Settings > Entity
types > Person), for example `/team/{slug}`.

**Team listings.** `on_team` is a boolean field of the person type. A theme lists
the team with `query`; people who are not public are never listed, so the filter
only has to say who is on the team:

```
{{$team := query (dict "schema" "person" "attr" "on_team" "eq" true "order" "name" "limit" 50)}}
{{range $team.Items}}<a href="{{.URL}}">{{.Title}}</a> {{.Attrs.job_title}}{{end}}
```

`eq` must be the boolean `true`, not the string (see the `attr` row above). A
byline card on a post uses `.Entity.Author` (the same fields, plus `.IsOrg`).

**Breadcrumb section label.** The breadcrumb above an entry and the
BreadcrumbList JSON-LD name the section with the schema's `archive_crumb_label`,
else its `archive_title`, else the type name.

**Terms** are hierarchical and their kinds are free. A term has an optional
`parent_id` (same kind, no cycles, at most 8 levels) and a `description`.
`tag` and `category` are built in; any other lower-case kind (`genre`,
`series`; 1-16 characters of `a-z0-9_-`, starting with a letter, not one of the
reserved words `author`, `search`, `api`, `admin`, `p`, `feed`, `health`,
`ready`, `static`, `media`, `mcp`, `sitemap`, `robots`, `llms`, `preview`,
`page`, `theme`, `forms`, or anything starting with `auth`, which the proxy
sends elsewhere) exists as soon as its first term does, with no registry. Its archive is
`/{kind}/{path}`: the slugs of the term's ancestors then its own
(`/genre/fiction/noir`, `/category/news/local-news`); only that canonical path
serves. A theme sees `.Term.Kind`, so `taxonomy-genre.html` styles one kind.

**Funcs** (besides the ones above). All are fail-open: an error or timeout
gives no items and the page still renders.

- `recent "post" 5`: newest published entities of a schema (and its aliases).
- `related . 3`: other published entities of the page's schema (same as
  `call .Related 3`; pass `$` inside a `range`). Limits are clamped to 1..50.
- `terms "category" 10`: terms of a kind (any kind) with published entities,
  most used first (`.Name`, `.URL`, `.Count`, `.Description`, `.Parent`).
- `menu "header"`: the items of the menu at a location, like `.Site.Nav`.
- `paginate .Pagination`: page links `{N, URL, Current, Gap}` for the first,
  last and current-±2 pages, with gaps.
- `query dict`, `attrValues schema attr limit`, `pageNum .`, `queryParam . name`: see
  [Template funcs reference](#template-funcs-reference).
- `srcset .Entity.Image`: `"/media/a_medium.jpg 300w, ..."` from the resized
  copies of a stored media file (the square thumbnail is skipped); `""` for
  remote URLs and files with no copies.

**Routes.** Archives live at the schema's `archive_path`; the built-in
`/category/{path}`, `/tag/{path}`, `/{kind}/{path}` (a custom term kind),
`/author/{slug}` and `/search` are fallbacks, tried only after redirects, schema URL patterns and archives found
nothing, so **an entity or archive at one of those paths always wins** and the
built-in page is unreachable there. Pagination is `?page=N` (page 1 has no
parameter): the canonical URL carries `?page=N` from page 2, with
`rel="prev"`/`rel="next"` in the head. A page past the end shows the last
page. 12 items per page. A person page (by default `/author/{slug}`, the `person` type's `url_pattern`) lists
the published entities that credit them, limited to schemas that have an
archive (posts and the like; never pages); a person who is not public is a 404, one with
nothing to list still renders their profile. Search uses Postgres full-text search (`simple`
configuration, no stemming) over name, excerpt and the first 200 000 characters
of content (longer bodies are indexed truncated so writes never hit the
tsvector size limit); query syntax is `websearch_to_tsquery`. `?type=<schema slug>`
limits a search to one schema and its aliases (lower-cased and trimmed, it must
match `^[a-z][a-z0-9_-]*$`); an unknown or malformed type is a normal page with
no results, never a site-wide search, and the type is kept in the pagination
links. The default `search.html` adds a hidden `type` input only when
`.SearchType` is set. `?q=` is cleaned
(invalid UTF-8 and control characters removed, at most 200 characters), a query
with only negated terms matches nothing, and the route is rate limited per
client. Results are `noindex` and exclude every entity the sitemap excludes
(`metadata.no_index`, `attributes.no_index`, `attributes.seo.robots` containing
`noindex`). An archive without `archive_title` is headed by its menu label
(the menu item linking to the archive), else the schema name pluralised.
Archives are cached like pages; taxonomy, author and search views are never
cached. A page rendered while a `recent`, `terms`, `menu`, `srcset`, `query` or `attrValues` lookup
failed, or while a plugin template func or render-time plugin filter failed, timed out or was
skipped by its circuit breaker, is served but not cached. Cached single pages embed term and author
names and `terms` widgets; term changes clear the page cache like any other
content change.

**Registry detail pages.** `/plugins/<name>` and `/themes/<name>` show one
entry of the verified registry index (the one the directory sections list).
They are a fallback after the built-in listings, so the order at an exact path
is: a published CMS page, then a built-in listing (for instance a custom term
kind named `plugins`), then the registry page. The page exists only while the
site's published `/plugins` (or `/themes`) page has a `plugin_directory` (or
`theme_directory`) section, and only for a name in the verified index; anything
else, including a registry that is off, unavailable or empty, is a normal 404.
Like a directory page it is cached for at most 60 seconds.

The layout `registry` receives `.Page.Registry`:

| Field | Meaning |
| --- | --- |
| `.Type` | `"plugin"` or `"theme"` |
| `.ListName`, `.ListURL` | the directory page to link back to (`Plugins`, `/plugins`) |
| `.Plugin` | the plugin card (`PluginCard`), set when `.Type` is `plugin` |
| `.Theme` | the theme card (`ThemeCard`), set when `.Type` is `theme` |

`PluginCard` and `ThemeCard` gain two additive fields, `About` (the optional
manifest text, one string per paragraph) and `Features` (the optional bullets).
Both are untrusted plain text: print them with `{{.}}`, never as HTML. The
existing card fields are unchanged. The default directory sections link each
card title to its page; a theme that overrides `plugin_directory.html` or
`theme_directory.html` adds its own link, and without one the cards simply stay
unlinked. A theme without `layouts/registry.html` renders the page through the
embedded default layout. The root of the layout carries `data-hm-directory`,
which gives the page the short shared-cache lifetime. `data-hm-contract` is
unchanged.

**Default theme.** Ships every layout above, `blog.css` (cards, article,
pagination), `wp-compat.css` (core Gutenberg block classes in rich-text
bodies: `alignwide`/`alignfull`, image captions, quotes, tables, columns,
buttons, responsive embeds, colour and size classes), dark mode through
`prefers-color-scheme` (the inline token `<style>` carries only values the
owner changed from the stock defaults, so an untouched site follows the OS
setting; colours the owner set win), and accessible navigation (skip link, labelled `nav`, `aria-current` on
the current link, `:focus-visible` outlines).

### Docs layout

The default theme ships `layouts/single-doc.html`, picked for entities of a
schema with the slug `doc`. It uses the whole viewport (no `hm-container`):
the sidebar sits in a 16rem column at the left edge, the "On this page" list
(built by `docBody`) in a 14rem column at the right edge, and the article is
centred in the middle column at about 46rem (breadcrumb, title, text, pager
and card grids share one left edge; card grids may extend to the right, up to 60rem wide). A page
without headings gives the article the third track, except from `90rem` wide, where the empty
track stays so the article stays centred. Blocks of a builder page are
still rendered after the body.

- **Sidebar.** The site's `sidebar` menu as a tree. Each top-level item with
  children is a `<details>`; only the one holding the current page has `open`
  (compared by `Href`). Its summary is the section name and the section's own
  link is the first entry inside, labelled "About" plus the section name (so it is never a second "Overview"). The current page carries
  `aria-current="page"`. The partial `doc-nav` (in `partials/doc.html`) takes
  `Nav` (a `[]NavItem`), `Cur` (the current item's `Href`) and `Open` (the `Href`
  of the top-level item that starts open); `doc-tree` and `doc-item` render the
  levels below.
- **Group labels.** A menu item without a link (a `custom` item with an empty
  URL, which the renderer gives the `Href` `#`) is a label: it renders as a
  `<span class="hm-doc-nav__group">`, never a link, followed by its children.
  The docs tool uses this for "Guides" and "Reference". Labels are skipped in
  the breadcrumb and as previous or next page.
- **Search box.** The partial `doc-search` is a plain GET form to `/search`
  at the top of the sidebar (both copies), with the page's `.SchemaSlug` as the
  hidden `type`, so results are limited to the same kind of page. It has no
  `id` because the sidebar is in the page twice.
- **Cards.** On a section index (a page whose menu item has children) and on
  the landing page (a page that is the parent path of the top-level items) the
  layout appends card grids (`doc-cards`): one grid for direct children (headed "Sections" on the landing page), then a
  heading and a grid per group label. Each card is the item's link plus the
  excerpt of the matching entity (its `description` attribute), read from two
  `query` calls (100 rows each; page 2 only when page 1 reports more pages) made only on those pages; a
  page without a description shows its title only. Article pages show no cards.
- **Next steps.** CSS styles a list of class `hm-next-steps`, or the list right
  after a heading with the id `next-steps`, as a row of cards at the end of a
  guide.

- **Breadcrumb.** The root crumb is the schema's archive crumb from
  `.Breadcrumb` (the item between Home and the page), so it follows the
  schema's `archive_path`. A schema without an archive path has no root crumb.
- **Menu.** Create a menu at the location `sidebar`, nested by section and page.
  A page that is not in the menu still renders, without trail or prev/next.
- **Mobile.** The sidebar (with its search box) is rendered twice. Below `60rem` the `<details>`
  copy (closed by default) shows and the aside and the TOC are hidden; from
  `60rem` the aside and TOC are sticky columns (each scrolls inside itself when taller than the window) and the `<details>` is hidden.
  No JavaScript is involved. The article comes first in the source (the
  stylesheet places the columns) so the skip link lands on it, not on the menu.
- **Styles.** `assets/css/docs.css` holds the `hm-doc` rules (the cards use `hm-doc-cards` and `hm-doc-card`, apart from the blog `hm-card`), bundled with the
  other `assets/css/*.css` files, using only `--hm-*` tokens.
- **Headings.** `docBody` adds the ids at render time only for this layout.

## Installing and activating themes

Themes are packages (`.hmp`, see [PACKAGES.md](PACKAGES.md)) stored in Postgres.
There is no theme directory and no environment variable to point at one:
`HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` are gone, and the server
refuses to boot while either is set (the message names the replacement).

**Admin > Theme > Themes** lists the built-in default theme and every
installed one.

- **Install** from the registry (`HATAMOTO_REGISTRY_URL`, default
  `https://hatamoto.dev/registry.json`; signed index) or by uploading a `.hmp`.
  Installing never changes the site. A package must be signed by a key in
  `HATAMOTO_TRUSTED_KEYS`, unless the owner allowed unsigned packages under
  Settings > Plugins (a warned, explicit opt-in).
- **Preview** renders the real site with any installed theme, whatever is
  active, for the signed-in owner or editor only. The admin mints a signed
  one-hour token; the public site turns it into an `HttpOnly` cookie
  (`hm_theme_preview`). Preview responses are `private, no-store`, never enter
  the HTML cache, and a visitor without the cookie never sees them.
  `/?hm_theme_preview=off` ends it. The token is a bearer link: anyone given it
  can view that theme's design over published content for the hour, and the
  cookie outlives a logout; it never exposes drafts.
- **Activate** first lists the section types your content uses that the theme
  does not render (a count per type); you can still activate. The theme then
  serves every visitor at once, no restart. The theme chain is
  `[active theme package, embedded default]`: a theme may be partial, and every
  file it lacks comes from the default. Switching back to the built-in theme is
  the same action. An active theme cannot be uninstalled.
- **Settings.** A theme's `settings_schema` (JSON Schema in `theme.json`) becomes
  a form on the theme's card. Values are stored per theme (kept across updates)
  and reach templates as `.Settings` (see below).
- **Colours and fonts** (primary colour, fonts, corner radii) are the site's
  design tokens, applied by whatever theme is live.
- **Builder fonts.** The builder's Font style key (`ff`) is a closed choice that compiles to theme variables, so a
  theme controls the typefaces: `body` is `var(--hm-font,inherit)`, `heading` is
  `var(--hm-font-heading,var(--hm-font,inherit))` and `mono` is
  `var(--hm-font-mono,ui-monospace,SFMono-Regular,Menlo,Consolas,monospace)`. A theme that defines `--hm-font`,
  `--hm-font-heading` and `--hm-font-mono` (the default theme does) restyles every node that uses them.

The content hash of the effective files is the theme `version`; it keys the HTML
cache, so a theme change never serves stale markup. The HTML cache is off
unless `PUBLIC_BASE_URL` is set (canonical URLs would otherwise follow the
request `Host`). See [Page cache](#page-cache) for the TTL and what clears it.

**Developing a theme.** Make a directory with the layout above and run

```sh
hatamoto theme dev ./mytheme       # pushes it as an unsigned dev package and again on every change
```

A running server (not `ENVIRONMENT=production`) applies each push within a
second; the first push also activates the theme (`--no-activate` to skip). The
command refuses to run against a production environment, and a production
server never loads dev packages. When it is ready:

```sh
hatamoto keys generate --name mykey                     # once; keep mykey.key secret
hatamoto theme pack ./mytheme --key mykey.key           # mytheme-1.0.0.hmp
hatamoto package install mytheme-1.0.0.hmp              # or upload it in the admin
```

**Moving from `HATAMOTO_THEME_DIR`.** Pack the directory (`hatamoto theme pack`,
signed or not), install it (`hatamoto package install`, after allowing unsigned
packages in Settings > Plugins if it is unsigned), activate it in the admin,
then unset the variable. The site keeps rendering with the built-in theme in
between.

**`.Settings`.** Layouts and partials read the active theme's settings as
`{{.Settings.accent}}`; section templates (whose dot is the prepared section)
read the same map as `{{.Settings.accent}}` too. Schema defaults are merged in, so a key with a `default` is always present. It
is a read-only `map[string]any`; strings are escaped by `html/template` like any
other value. (`.Site.Settings` is something else: the extras of the site's
design tokens object.)

CSS is bundled: `assets/css/base.css`, the other `assets/css/*.css` files
(such as `blog.css` and `docs.css`, sorted) plus `assets/css/sections/*.css`
(sorted) become one fingerprinted file `/theme/assets/theme.<hash>.css`
(immutable cache). `assets/js/hm.js` is served at `/theme/assets/hm.js`. Other
files under `assets/` are served from `/theme/assets/...`.

## Page cache

Rendered public pages (and archives, feeds and sitemaps) are kept in an
in-process HTML cache. It is keyed by theme version and path, never used for
requests with a query string, previews, pages that failed to render fully, or
when `PUBLIC_BASE_URL` is unset.

**TTL.** `HATAMOTO_PAGE_CACHE_TTL` is a Go duration (`90s`, `15m`, `6h`). The
default is `5m`; the allowed range is `30s` to `720h` (30 days). A value that does not
parse or is out of range is ignored: the default is used and a warning is
logged at boot. A longer TTL means more visits are served from memory (a cached
page answers in a few milliseconds, a cold render costs tens to a hundred), which
matters most on low-traffic pages that would otherwise expire between visits.
The cache lives in the process and is empty after a restart or deploy.

**Admin controls.** Owners manage the cache in Admin > Settings > Performance,
live and without a restart: switch it on or off (off bypasses reads and writes
and clears the cache once), set the lifetime (a value saved there wins over
`HATAMOTO_PAGE_CACHE_TTL`), cap its size in MB (the oldest pages are evicted
first), clear it, and preload it. Preload renders the sitemap URLs (only the missing ones by default, or all of them; optionally again shortly after a content change) through the
same public page handler a visitor reaches, so a preloaded page is byte for
byte what a visit would store; it writes no analytics or 404 log rows.

**What clears it.** Because a long TTL makes a missed purge visible for hours,
everything that can change public HTML clears the whole page cache (and the
sitemaps) at once, not at TTL expiry:

- entities: create, update, publish, unpublish, delete, from the admin, REST API
  keys, MCP and plugins (`entities.write`); a **scheduled publish** by the
  worker, so the page and every listing change the moment its time comes;
  imports;
- terms (create, edit, delete, assignment), authors, media (alt text, caption,
  delete), menus, forms, schemas (URL patterns), site settings and design
  tokens, setup and starter content;
- themes and plugins: install, update, uninstall, enable, disable, activate a
  theme, theme settings and plugin settings. Menus and terms clear it whoever
  writes them (admin, REST, MCP or a plugin through `menus.*` / `terms.*`);
  inserting a link from the linking suggestion panel, and the link rewrite that
  follows a slug or parent change, clear it too.

A page that is being rendered at the moment of a purge is served to its visitor
but not stored, so a purge is never undone by a render that read the data
before the change.

**Other processes.** The cache itself is per process, but every write to
entities, menus and menu items (from the admin, the API, the worker, a `hatamoto`
CLI command such as `entity create --publish`, an import, another replica) adds
one row to a change log in the database, once per transaction, in that transaction
(a statement that changes no rows, such as the worker's check for due posts, adds
nothing, and a rolled-back transaction adds nothing). Each server polls the log
every 3 seconds (every second outside production) and clears its own cache when a
new committed row appeared. An isolated change from another process shows on the
next request within about one poll interval (3 seconds). Purges are coalesced:
while writes keep arriving (an import) the cache is cleared at most once per 5
polls, so a change can take up to about 15 seconds to show, and one final purge
runs on the first poll with no new change once the writes stop. The writing process clears at once and again at its next poll (a second
clear up to one poll later), which is harmless. Not covered: changes to settings, terms, authors, media, forms, schemas,
themes and plugins made in another process; with several replicas those still show
on the others when the TTL expires, so keep the TTL short there. Cost: one
insert into an append-only table per writing transaction (no shared row is
locked, so writers do not wait for each other) and one small read per server per
poll; old log rows are deleted after a day. If the migration is not applied
the poller logs a warning and switches itself off.

Redirects need no purge: they are checked on every request, before the cache, so
a redirect created over a cached page wins at once and removing it brings the
page back.

**Plugin data.** Output a plugin puts on a page (template funcs such as
`plugin_<name>_<func>`, `render.page` and `render.head` filters, including data
a func reads from `kv` or `db` or fetches over `http.fetch`) is cached **with the
page for the TTL**. It changes on the page only after the TTL expires or another
purge above happens. A plugin cannot clear the page cache itself (there is no
`cache.invalidate` host function); keep the data a func shows tolerant of a
delay of one TTL, or save an entity or the plugin's settings to force a purge.
A page whose plugin func fails, times out or is skipped by its breaker is served
but never cached, and a `render.page` filter that handles a request makes that
page uncached.

## Section schemas

`schemas/<type>.json` describes a section's `content`. It drives the admin
form (via [`GET /api/v1/render/theme`](#theme-endpoint)) and tells the renderer
which strings to sanitise.

Supported JSON Schema subset: plain structural keywords (`type`, `properties`,
`items`, `enum`, `default`, `title`, `description`, `required`, and numeric or
length bounds). The admin form cannot render `$ref`, `oneOf`, `anyOf`, `allOf`
or `patternProperties`, so a schema using any of them is rejected when the
theme loads, as is an unknown `x-hm-field` kind.

Extensions:

| Keyword | Where | Meaning |
| --- | --- | --- |
| `x-hm-section` | top level | `{ "title", "description", "icon" }` shown in the section picker |
| `x-hm-field` | string property | field kind: `plaintext`, `richtext`, `html`, `url`, `image`, `code`, `css`. `richtext` and `html` strings are sanitised at render time (and `data-hm-*` is stripped from them); `plaintext` and `code` are escaped by the template (`code` is edited in a monospace textarea and rendered through `hl`, see below). `css` is edited like `code` but is for a theme's own `<style>` element: at render time every `<` that could start a tag (one followed by `/`, `!`, `?` or an ASCII letter, or the last character of the value) becomes the CSS escape `\3c ` (so values joined in one `<style>` can never form `</style>`; other `<`, as in `@media (width < 600px)`, and `>` are left alone so range syntax and child combinators work). A value containing `<!--` is kept, with that `<` escaped. A value containing `@import`, `expression(`, `javascript:`, `behavior:` or `-moz-binding` (case-insensitive) becomes empty, but that deny-list is best-effort only and can be bypassed with CSS escapes (for example `\40import`); do not rely on it. The only guaranteed property is that the value cannot leave the `<style>` element. Emit it only inside a top-level `<style>` element, as `<style>{{.Content.css}}</style>`, never inside `<svg>` and never in an attribute or elsewhere. Stored content is not rewritten |
| `x-hm-alias` | top-level property | alternative stored names, e.g. `form_slug` with alias `formSlug`; the renderer maps the alias to the canonical name before the template runs |

```json
{
  "x-hm-section": { "title": "Call to action", "description": "Heading and one button" },
  "type": "object",
  "properties": {
    "heading":    { "type": "string", "title": "Heading", "x-hm-field": "plaintext" },
    "buttonText": { "type": "string", "title": "Button text", "x-hm-field": "plaintext" },
    "buttonUrl":  { "type": "string", "title": "Button link", "x-hm-field": "url" }
  }
}
```

`theme.json` may carry a `settings_schema` (same subset) for theme-level
settings; it is returned as-is by the theme endpoint.

An unknown section type renders as an HTML comment
`<!-- unknown section type: name -->`, with no marker. Sections with
`isVisible: false` are skipped; the rest are ordered by `sort_order`.

### `code` fields and the `hl` function

`code` marks a string that holds source text. Templates render it with
`{{hl "go" .code}}`, which HTML-escapes the source first and then wraps strings,
comments and a small keyword set in `<span class="hm-tok hm-tok--k|s|c">`.
Output is built only from escaped input. Shared partials: `section-head`
(eyebrow, heading, subheading) and `code-panel` (framed `<pre>` plus optional
output strip), with `.hm-frame`, `.hm-code`, `.hm-outs` in `base.css`.

### Section library of the default theme

| Type | Fields (camelCase) | Notes |
| --- | --- | --- |
| `features` | `eyebrow, heading, subheading, variant (cards\|list\|bento), columns, features[{title, description, number, category, tags, badge}]` | `bento` is the framed grid with numbered cells |
| `content` | `layout (stack\|essay), eyebrow, asideGlyph, asideText, headline, body` | `essay` adds a sticky aside; `stack` is unchanged |
| `process_steps` | `eyebrow, heading, subheading, layout (list\|cards), steps[{number, title, description, code}]` | `cards` shows a code box per step and lifts the width cap |
| `tag_strip` | `label, items[]` | label plus a row of short items (`items.N` markers) |
| `split` | `eyebrow, heading, lede, footnote, layout, ratio, rowsStyle (definitions\|chips-grid), rows[{term, description}], panel{title, meta, code, language}, outputs[{label, value}]` | text column plus code panel |
| `card_columns` | `eyebrow, heading, subheading, columnCount, columns[{label, title, description, items[]}], notes[{title, text}]` | markers `columns.N.items.M` |
| `comparison` | `eyebrow, heading, subheading, sides[{tag, title, tone (negative\|positive), items[]}]` | glyphs are CSS pseudo-elements, never content |
| `hero` | adds optional `panel`, `commandChip` | code panel shows with `layout: split` and no image |

Each type has `sections/<type>.html`, `schemas/<type>.json`,
`assets/css/sections/<type>.css`, a fixture and a golden file.

## Marker contract v1

The markers are the interface between the rendered page and an editor that
embeds it. They are isolated in `render/markers.go` and versioned.

| Attribute | On | Value |
| --- | --- | --- |
| `data-hm-contract` | `<html>` | contract version, currently `1`. Bumped on incompatible change. |
| `data-hm-entity` | `<main>` | id of the entity being rendered |
| `data-hm-section` | `<section>` | the stored section `id` |
| `data-hm-type` | `<section>` | the section `type` |
| `data-hm-field` | any element whose text is one stored field | canonical JSON path inside the section's `content` |
| `data-hm-kind` | with `data-hm-field` | the field's kind when it is not plain text: `richtext` (sanitised rich text) or `html` (raw html section body) |
| `data-hm-preview` | `<html>` | `true` only in [preview](#preview-endpoint) output |

A section whose id is a short lower-case slug (`[a-z][a-z0-9_-]*`, up to 64 characters, not `main`, `content`, `nav`, `top`, `header`, `footer` or `hm-*`) is also rendered with `id="<section id>"`, so `/#quickstart` links to it. Other ids (UUIDs, upper case) get no `id` attribute.

`data-hm-field` path grammar: segments separated by `.`; a segment is a
property name or an array index. `headline`, `faqs.2.answer`,
`address.postal_code`, `ctas.0.text`. Paths are always canonical (alias names
are resolved), so they can be used as keys into the content of the matching section (`page_doc` component props; for a page that still has only legacy `page_sections`, `attributes.page_sections[i].content`).

```html
<section data-hm-section="s3" data-hm-type="faq" class="hm-section hm-section--faq">
  ...
  <h3 data-hm-field="faqs.0.question">Why?</h3>
</section>
```

Authoring in a theme: `<section {{hmsec .}}>` is added by the engine's
`section` partial (themes do not repeat it); inside section templates write
`<h2 {{hmf "heading"}}>` or `{{hmf "faqs" $i "answer"}}`.

**Save-time sanitising needs a theme.** `richtext`/`html` fields are also
sanitised when content is saved (every string under such a field, whatever
shape the client sent). That pass reads the active theme's schemas, so it runs
only while Go theme rendering is on; with rendering off (no theme) saves skip
it, and the content is sanitised at render time once a theme is enabled.

**Trust rule.** Tenant content is untrusted HTML. Everything the sanitiser lets
through from `richtext` and `html` fields has all `data-hm-*` attributes
**removed**, so content cannot forge a section, field or entity marker. Markers
that appear on the body wrapper itself (`.hm-html-body` for `html` fields,
`.hm-prose` for `richtext` fields) come from the theme; markers on its
descendants never do. A consumer can therefore trust
`data-hm-section`/`data-hm-field` on any element that is not inside a `richtext`
or `html` body. Text inside such a body is edited as a whole through its
wrapper's `data-hm-field` and `data-hm-kind` (`richtext` or `html`).

Section elements are omitted for hidden sections, unknown types, blank
`content` sections and sections that fail to render (those leave an
`<!-- section render failed: type -->` comment), so the number of
`data-hm-section` elements is at most the number of visible known sections.

A page with a failed section is still served, but with `Cache-Control:
private, no-cache, no-store` and never written to the HTML cache, since the
failure may be transient. Each section is currently executed twice per
uncached render (a trial pass, then the page), so plugin template funcs must
be side-effect free.

### Design tokens (`builder.tokens`)

`theme.json` may carry `"builder": {"tokens": true}`. A theme that sets it gets the site's design tokens as CSS
custom properties in the builder style block (`:root{--hm-space-4:1.25rem;...}`, only values that differ from the
defaults) and the `fs` style as `var(--hm-fs-<name>,<literal>)`. A theme may define or override the same variables
in its own CSS (`--hm-surface`, `--hm-muted`, `--hm-accent`, `--hm-space-0` to `--hm-space-12`, `--hm-fs-xs` to
`--hm-fs-4xl`, `--hm-shadow-sm|md|lg`) as defaults; a value the owner sets in Settings wins when the block comes
later in the head. Without the flag nothing changes. See [BUILDER.md](BUILDER.md#design-system).

### Strict script policy (`csp.strictScripts`)

`theme.json` may carry `"csp": {"strictScripts": true}`. Public pages of a theme that sets it are served with
`script-src 'self'` (plus the captcha provider's origins when one is configured) instead of
`script-src 'self' 'unsafe-inline'`; every other directive is unchanged. By opting in the theme promises that it
emits no inline executable `<script>`, no `on*=` attribute and no `javascript:` URL on public pages. `<script src>`,
`type="module"` islands and `application/ld+json` data blocks are fine. The default theme opts in; a theme without
the key keeps the previous policy byte for byte. There is no nonce: public HTML is shared through the page cache, so
a per-request nonce would go stale. `style-src` still allows `'unsafe-inline'` (tokens, builder CSS, `style=` in
content). The admin preview and canvas responses keep the previous policy (the canvas bridge is an inline script).

### Layout parts (`builder.parts`)

`theme.json` may list the slots the theme lets the site build in the builder:
`"builder": {"parts": ["header","footer","single","archive"]}` (unknown names are ignored). A slot is **active**
when the theme opted in, the owner assigned a part (Theme > Layout parts, stored in
`theme_extra.builder.parts`), and the part is a `theme_part` entity of that slot with a valid doc. Themes that did not
opt in render exactly as before, whatever is assigned.

Wrapper contract (nothing to write in the theme; authors never use a `.Part` field):

- `header` and `footer`: the part replaces the theme's `header` / `footer` partial.
- `single` and `archive`: the part replaces the `content` block of the layouts `single`, `single-<schema>`, `archive`
  and `archive-<schema>` (a theme whose entity pages use the `single` layout, as the default theme does, wraps pages too; the `home` layout is never wrapped). The part holds one `slot`; the theme's own content is rendered at the slot, so the part
  adds markup before and after it. The `home`, `taxonomy`, `author` and `search` layouts are never replaced.
- An `archive` part whose `slot` has `replace` set prints only its own markup before and after the slot: the theme's
  `content` block (its listing) is not drawn. Pair it with a `loop` of `from: "archive"` to build the archive in the
  builder. `replace` in any other part is `422 part_slot`; a `single` part is never replaced.
- The theme's original partial or block stays available and is the fallback: a part that is missing, of another
  slot, unparsable, has no slot (or two), or fails to render is ignored for that request and the theme's own markup is
  used.
- The part's CSS joins the page's `hm-builder-css`; the head must call `plugin-head` as for builder pages.

See [BUILDER.md](BUILDER.md#reuse).

### Builder pages (`page_doc`)

A page whose `attributes.page_doc` is a [builder](BUILDER.md) tree renders its layout nodes natively; the
theme's `section` partial renders v1-shaped nodes and is the only wrapper a `component` gets. The
`section` partial **must** print `.Comment` when set (the default partial does), because native nodes are
passed through it that way; a partial that ignores it wraps native nodes in a section. The head must
call `plugin-head`: the per-page `<style id="hm-builder-css">` is appended to it (the engine splices it before
`</head>` when the theme never prints it). Native nodes carry `class="hm-n-<id>"`. The `data-hm-node` marker
exists only in canvas output (`canvas: true` previews) and never on public pages.

Element settings (see [BUILDER.md](BUILDER.md#element-settings-builder-e6)) add markup a theme should style or leave alone:

* An image with a caption is `figure.hm-w-fig` with `figcaption.hm-w-cap`; a linked or lightbox image is `a.hm-w-imglink`
  (around the image, or the outermost element without a caption); a container with a link is `a.hm-c--link`. The core
  writes the default rules (`figure{margin:0}`, `.hm-w-cap`, block links, colour inherited, no underline) only on pages
  that use them; a theme overrides them with its own selectors. Images with a library `src` carry `width` and `height`
  attributes: keep `height:auto` in the theme CSS so the box scales.
* A page with a lightbox loads the core island `/builder/lightbox.<sha8>.js` and the dialog CSS (`.hm-lb`, class-only).
  The island builds a native `dialog` at click time; a theme can restyle the `.hm-lb*` classes but must not depend on them.
* Anchors are public `id` attributes chosen by authors. The core refuses, on save, ids the page itself uses: `main-content`,
  `toc`, `top`, `main`, `content`, any id starting `hm-`, any ending `-strip-label`, and the id of a node of the same page.
  A theme that prints its own ids should keep to the `hm-` prefix so authors cannot collide with them; the default theme is
  checked by a test that scans its templates. `scroll-behavior` and `scroll-margin-top` (a sticky header) are theme CSS:
  the core adds no scroll script.

## Theme endpoint

`GET /api/v1/render/theme` returns what the admin needs to build section forms.
Requires an authenticated `owner` or `editor`.

```json
{
  "name": "default",
  "version": "d316c2f6629b",
  "sections": [
    { "type": "hero", "title": "Hero", "description": "Headline and buttons", "schema": { "type": "object", "properties": {} } }
  ],
  "settings_schema": { "type": "object", "properties": {} }
}
```

`sections` is sorted by `type`; `version` changes whenever any theme file does.

## Preview endpoint

`POST /api/v1/render/preview` renders **unsaved** attributes as a full HTML
document. It never writes to the database or the cache.

Request (`application/json`, max 4 MiB, else `413`):

```json
{
  "entity_id": "optional uuid (puts data-hm-entity on <main>)",
  "schema": "page",
  "attributes": { "meta_title": "Draft", "page_sections": [ { "id": "s1", "type": "hero", "sort_order": 1, "content": {} } ] },
  "path": "optional local path, default /"
}
```

Response: `200 text/html`. Posted section content is sanitised exactly like
stored content (scripts removed, `data-hm-*` stripped from rich text). The
document is `noindex`, carries `data-hm-preview="true"`, and uses the live site
chrome (name, navigation, forms).

Headers: `Cache-Control: no-store`, `X-Frame-Options: SAMEORIGIN`,
`Content-Security-Policy: ... frame-ancestors 'self'`, `X-Robots-Tag: noindex`.

Access: `owner` or `editor`. `401` without credentials, `403` for other roles.
With cookie auth on (`ENABLE_COOKIE_AUTH`), the session-bound CSRF check
applies to unsafe methods: send the `csrf_token` from the login response body in
`X-CSRF-Token` (it is bound to the API session cookie; no CSRF cookie is set).
The request must also be same-origin. Bearer-token callers are exempt.

Canvas flag: with `"canvas": true` the response additionally carries the
editor bridge script (`<script id="hm-canvas-bridge">`, injected before the last
`</body>`, embedded in the Go binary). Nothing else differs from the same
request without the flag. The admin canvas editor uses it; see
`backend/web/admin/js/canvas/protocol.js` for the postMessage protocol v1; protocol v2 (any node,
in-place patching) is in [CANVAS.md](CANVAS.md). `data-hm-t` (section type or widget kind) is a canvas-only marker like
`data-hm-node`: it never appears in public output.

Fonts: `/theme/assets/*` responses carry `Access-Control-Allow-Origin: *`,
because an opaque-origin (sandboxed) iframe loads fonts in CORS mode.

Embedding: the preview HTML contains tenant-authored content and the theme's
inline scripts, so it must **never** run in the admin's origin. Do not use a bare
`<iframe srcdoc>`: a `srcdoc` document inherits the parent's origin and could
act with the editor's session. Fetch the HTML with the request above and embed
it as `<iframe sandbox="allow-scripts" srcdoc="...">` **without**
`allow-same-origin`; the document then runs in an opaque origin, with no access
to the admin's cookies, storage or DOM. (Its own `/theme/assets/hm.js` still
loads and runs.) The admin host intercepts link clicks if navigation is not
wanted. Pointing the iframe at a same-origin URL instead is only acceptable when
that response carries its own `Content-Security-Policy: sandbox allow-scripts`.

## Writing and submitting a theme

`theme.json` may carry `"screenshot": "assets/screenshot.webp"`, a preview image
under `assets/`. It is optional for a package you install yourself and required
for the registry. The file must be a PNG, WebP or JPEG (never SVG) of at most
2 MiB, 640 to 2560 pixels wide and 360 to 1600 high, at most 4,200,000 pixels
in total; the extension must match the format. Installing or loading a package
ignores a screenshot that breaks these rules. Only `theme check --registry`,
`theme pack --registry` and the registry build require it and check it
strictly. Check a theme directory with:

```sh
hatamoto theme check ./my-theme               # the same checks as pack, writes nothing, needs no key
hatamoto theme check ./my-theme --registry    # also the registry rules: a valid screenshot is required, "default" is refused
```

To publish a theme in the registry, see [THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md).

## Plugins

Plugins built against SDK `0.2.0` or later can extend the renderer. A plugin
built against `^0.1` keeps working; it simply has no template or head
extensions.

**Template funcs** – `Deps.TemplateFunc(name, fn)` registers `plugin_<plugin>_<name>`
for every theme template. A `-` in the plugin name becomes `_` (plugin `acme-seo`,
func `badge` is `plugin_acme_seo_badge`; partials likewise: `plugin/acme_seo/badge`).
A registration the host rejects (bad name, trusted return type, duplicate) fails
the plugin's wiring with that error. `fn` returns a value or `(value, error)` of `string`,
`bool`, a number, or a slice/map of those. Funcs returning `template.HTML`,
`template.JS`, `template.URL`, `template.CSS`, `template.HTMLAttr` or
`template.Srcset` are rejected at registration: plugin output is always
escaped for its context, never trusted.

**Partials** – `Deps.TemplatePartial(name, src)` registers an `html/template`
source as the partial `plugin/<plugin>/<name>`; a theme includes it with
`{{template "plugin/acme/banner" .}}`. A partial that fails to parse is skipped
with a warning; the page still renders.

**Head tags** – the `render.head` filter (`sdk.RenderHead`) receives the page
(`Path`, `Language`, `SchemaSlug`, `EntityID`) and appends structured tags. A
plugin never supplies raw HTML:

| `Kind` | Allowed `Attrs` |
| --- | --- |
| `meta` | `name`, `property`, `content` |
| `link` | `rel`, `href`, `type`, `hreflang`, `crossorigin` |
| `script-src` | `src`, `crossorigin` |
| `jsonld` | `JSONLD` document (emitted as `application/ld+json`) |

Other attributes and unsafe URL schemes (`javascript:`, `data:`, ...) are
dropped, and values are attribute-escaped. The filter has a 2 s deadline and is
fail-open: a panic, error or timeout leaves the head without the plugin's tags.

## Feeds, internal links and plugin asset slots

**Feeds.** When the site has a `post` schema, `/feed.xml` (RSS 2.0) and
`/atom.xml` (Atom 1.0) list the 20 newest published posts with their sanitised
bodies, absolute links and the author attribute. The default `head` partial
advertises both with `<link rel="alternate">` (`.Feeds` in the head view); a
theme with its own `head.html` should range over `.Feeds` to keep that.

**Internal links.** The editor stores links to other entities as
`<a data-entity-id="UUID" href="...">`. At render time every such link in
section rich text, `html` fields and the post body (`.Body`) gets the target's
current public path, resolved in one query for the page, so a slug or URL
pattern change cannot leave a stale link. Targets that are not published keep
their stored href. Themes need to do nothing.

**Plugin asset slots.** `layouts/base.html` calls `{{template "plugin-head" .}}`
just before `</head>` and `{{template "plugin-body" .}}` just before `</body>`
(defined in `partials/plugin-assets.html`). The host fills `.PluginHead` with
`<link rel="stylesheet">` and `.PluginBody` with `<script defer>` tags for every
loaded plugin whose package ships `assets/plugin.css` and/or `assets/plugin.js`
(served at `/p/<plugin>/assets/...`; see [PACKAGES.md](PACKAGES.md)). The slots
render nothing while no plugin ships them. A theme with its own `base.html`
must call both partials to receive plugin assets.

## Interactivity

The default theme works with JavaScript off:

- Forms post `application/x-www-form-urlencoded` to `POST /forms/{slug}/submit`
  and the server answers `303` back to the page with `?submitted=1` (success
  banner) or `?form_error=1`. A hidden honeypot field (`hm_website`) silently
  drops bots. The endpoint is rate limited per IP.
- Navigation is a `<details>` element.

`/theme/assets/hm.js` (~70 lines, no dependencies) upgrades this: it submits
forms as JSON to `/api/v1/public/forms/{slug}/submit` and shows the success
message in place, falling back to the native post on any failure; Escape closes
the menu. A theme may replace or drop `hm.js`.

## Site icons

`assets/icons/` holds the site icons. Go serves them at the site root
(`/favicon.ico`, `/favicon-16.png`, `/favicon-32.png`, `/apple-icon.png`,
`/icon-192.png`, `/icon-512.png`) with a 30 day cache; the default theme ships a
set, and a theme overrides any of them by shipping a file of the same name.

## Headless mode

The public JSON page endpoints are gone: the Go renderer is the only public
front end. The remaining `/api/v1/public/...` routes are forms, sitemaps,
`llms.txt`, the knowledge graph and taxonomy archives. The same binary serves
the admin. See `docs/deploy/caddy-hatamoto.md`.

## Known gaps

- **`related_articles` deferred.** The section is not in the default theme.
- **Archive listing is minimal.** Archive URLs render the heading and
  description; a full item listing and pagination are not in the default theme.

> Widgets shipped by a theme package are not supported: a theme package cannot hold `widgets/`. Page-builder widgets come from plugin packages, see [WIDGET_PACKS.md](WIDGET_PACKS.md).
