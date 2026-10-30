# Schema URL patterns

Where an entity lives on the public site is **data**, not code: every entity
schema may carry a `settings` JSON object that says what URL its entities are
served at, which sitemap child lists them and which other schema slugs are
treated as the same content type. With empty settings the built-in defaults
below apply.

The same pattern drives the backend (canonical URLs, breadcrumbs, JSON-LD,
sitemap, the Go renderer's path resolution) and the admin (links, "view on site").

## Pattern syntax

A pattern is an absolute path made of literal segments and tokens:

| Token      | Meaning                                                                 |
|------------|-------------------------------------------------------------------------|
| `{slug}`   | the entity slug (the last segment: either `{slug}` or `{path}`, exactly one) |
| `{path}`   | the whole parent chain: the slugs of every ancestor, root first, then the entity's own, joined by `/` (`services/seo/local`). Takes one to 8 URL segments. The last segment, instead of `{slug}`; not combinable with `{parent}` |
| `{parent}` | slug of the entity's parent only; optional — the segment is dropped if there is none |
| `{year}`, `{month}`, `{day}` | the publication date: `2024`, `05`, `09` (zero-padded), see [Dates](#dates) |

Rules: starts with `/`, ends in `{slug}` or `{path}`, one token per segment,
each token at most once, values never contain `/` (except a `{path}` value,
whose segments are non-empty and at most 8). The first literal segment of a pattern (for example `docs` in
`/docs/{slug}`) is its *section prefix*. There is no language or region
token in a pattern. On an install with several public locales the site adds a
locale segment in front of the rendered path (`/fr/blog/{slug}`), outside the
pattern (see [Locale](#locale)).

The Go renderer resolves the path through the URL-pattern registry.

Examples: `/{slug}`, `/blog/{slug}`, `/docs/{slug}`, `/people/{parent}/{slug}`,
`/{path}`, `/{year}/{month}/{slug}`, `/news/{year}/{month}/{day}/{slug}`.

### Nested pages: `{path}`

Set `{"url_pattern": "/{path}"}` on a page-like schema to serve the content
tree at its natural URLs: a page `local` whose parent is `seo`, whose parent is
`services`, lives at `/services/seo/local`; a root page `about` still lives at
`/about`. The default pattern of the `page` schema stays `/{slug}` so existing
sites keep their URLs; opt in per schema.

* **Slug uniqueness follows the pattern.** Every rule below is per schema and
  per locale: the same slug may exist once in each locale. A schema whose
  pattern (or any override pattern) has `{path}` or `{parent}` has slugs that
  are unique **among siblings**: `/services/seo/local` and `/services/pt/local` are two pages with
  the slug `local` (root rows of the schema still need distinct slugs). Every
  other pattern (`/{slug}`, `/blog/{slug}`, dated ones) keeps one slug per
  schema and locale, because two rows with one slug would shadow each other at one URL.
  It is enforced by the database (two partial unique indexes, switched by a
  copy of the schema's nestedness on each entity) and the API answers
  `409 slug_exists` saying which rule applied. Changing a schema's pattern
  moves its rows between the rules; a change that would make slugs collide is
  refused with a `400` naming some of them. `GetBySlug`-style lookups by a bare
  slug (the admin API) return the root row first, then the oldest.
* **Strict resolution.** Only the exact chain serves: `/seo/local`,
  `/services/local` and `/services/seo/pt/local` are 404. The chain is walked
  from the root; ancestors may be of any schema and status (a draft region page
  still gives its children their URL), the page itself must be published.
* **Parents** are set with `parent_id` on entity create/update (and by
  `Service.Import`). A parent must exist, may not be the entity or one of its
  descendants (cycle), and nesting is capped at **8 levels**; the service
  refuses with `parent entity not found`, `parent would make the entity its own
  ancestor` or `entity nesting is too deep`. Reads cap their walk too, so rows
  that a raw SQL write made cyclic can never loop.
* **Moves keep old links alive.** When an update changes a published entity's
  slug or parent, every URL that changed (the entity and all published
  descendants) gets a **301** from its old path to the new one, written to
  `hatamoto_redirects` with `source_tag = entity-move`. A *draft* parent still
  moves its published children (it gives them their URL), without a redirect
  for itself; rules that pointed at
  an old path are repointed (no chains), a rule whose source is a path that
  became live again is removed, and `<a data-entity-id>` links in stored content
  are rewritten to the new URL. This now also applies to a plain slug change.
  `Service.Import` does not do this: it stores what the source says. A move that
  would push a descendant below 8 levels is refused (`entity nesting is too
  deep`), and an entity with children cannot be deleted (`409 has_children`):
  move or delete the children first.
* **A live page beats an automatic redirect.** Redirects are checked first, so
  when an entity is created, published (by hand or on schedule) or imported at a
  path where an `entity-move` redirect points elsewhere (rename `/about` to
  `/about-us`, later publish a new `/about`), that redirect is deactivated and
  the new page answers. Redirects an owner created keep their precedence: delete
  them if a page should take the path.

### Dates

`{year}`, `{month}` and `{day}` come from `published_at` (from `created_at`
while a draft has none, so previews have a URL), read in the **site time
zone** (the site setting `timezone`, an IANA name such as `Australia/Sydney`)
and **UTC when it is empty or unknown**. A request only serves when its date
equals the entity's: `/2024/05/hello-world` is 404 for a post published in June.
Changing the time zone, or an entity's `published_at`, changes dated URLs; the
URL registry is refreshed as soon as the setting is saved. (Updating `published_at`
after publishing does not create redirects: set a redirect rule if you must.)

## Locale

Every entity has a `locale`, a BCP 47 tag such as `en` or `fr`. An entity
created without one gets the site language. Slugs are unique per schema and
locale, so an `en` and an `fr` entity can share a slug.

### One locale (the default)

With no locale list in the site settings, the install is single-locale:
patterns, URLs and output are as described above. The public site serves only
entities whose `locale` equals the site language (exact match), and entities in
other locales are stored and reachable through the API but not public.
Changing the site language moves the entities in the old language to the new
one, except a row whose slug already exists in the new language (same schema and
parent scope): that row stays as stored in the old language, and the settings
save still succeeds.

### Several public locales

The site settings hold an ordered list of locales, each a BCP 47 tag with its
own URL segment (free text: `fr-CA` may use `fr`), a default locale (the site
`language`), a "prefix every locale" switch and an x-default locale.

* The segment is not a pattern token. After the redirect check, the router
  strips a leading known segment, resolves the rest with the usual patterns and
  looks entities up in that locale. `URLFor` and `Locate` put the segment back
  in front of the rendered path. `/fr/blog/hello` and `/blog/hello` use the same
  pattern, `/blog/{slug}`.
* The default locale is unprefixed unless "prefix every locale" is on. Then
  its pages live under its own segment, and the bare root answers `302` to the
  default locale's home, with `Cache-Control: no-store`.
* `/fr` serves the home page of that locale.
* Content with no locale ("shared", stored with the reserved locale `und`) has
  one unprefixed URL, is visible from every locale and has no hreflang.
* The default locale and shared content share the unprefixed namespace. When
  both have an entity at the same path, the default locale's wins.
* Entities in different locales are linked in a translation group (one copy per
  locale, each with its own slug). The head prints `hreflang` for every live
  copy in list order plus `x-default` (the x-default setting, else the first
  live copy). A locale with no copy is left out of the head; a theme language
  switcher can link it to that locale's home.
* Saving the list is refused for a segment that is reserved (`api`, `media`,
  `theme`, `admin`, `search`, `sitemap*`, `feed.xml`, `robots.txt`, taxonomy
  kinds), equals a section prefix or the archive path, equals the slug of a
  published root entity, or is the source of an active redirect. Redirects are
  never deleted for you.
* While the list is non-empty, changing the site language does not move
  entities between locales.

Sitemaps, search, feeds and llms.txt follow the visible locale; see
[Sitemaps](site/ai-seo/sitemaps.md) for the per-locale files.

A site that models regions as page trees with `overrides` on a region attribute (a `url_pattern` and a `sitemap_group` per region) can keep working as it is. To move it to real locales, use the converter in [LOCALE_CONVERSION.md](LOCALE_CONVERSION.md): it sets the locale of each page from the attribute and leaves every `url_pattern` and the attribute match in place, so URLs do not change. It removes only the `sitemap_group` key of those overrides, because the per-locale sitemap files take over.

## Schema settings

`entity_schemas.settings` (JSONB, default `{}`; exposed as `settings` on the
admin schema API). All keys optional; unknown keys are ignored.

| Key             | Type     | Meaning |
|-----------------|----------|---------|
| `url_pattern`   | string   | Pattern for this schema's entities. |
| `archive_path`  | string   | Section index URL (for example `/blog`): listed first in the sitemap child, used as the breadcrumb section crumb. Must start with `/`. |
| `sitemap_group` | string   | Child sitemap name. Default: first literal segment of the pattern, else `misc`. |
| `aliases`       | string[] | Extra schema slugs that resolve to this schema (archive, route lookup, linking). |
| `overrides`     | object[] | `{attribute, equals, url_pattern, sitemap_group}`: entities whose `attributes[attribute] == equals` use another pattern/group. |
| `sitemap`       | object   | `{priority, changefreq, exclude}`; `exclude` drops the schema from the sitemap. |
| `section_pages` | bool     | On a page-like schema: a row whose slug equals a section prefix of another schema (a `docs` page vs `/docs/{slug}`) is served at `/{slug}` and becomes that section's index in the sitemap. |
| `schema_org_type` | string | JSON-LD `@type` for entities of this schema when it has no `x-schema-org-*` annotations and no plugin JSON-LD builder. One of `Article`, `BlogPosting`, `NewsArticle`, `DefinedTerm`, `Person`, `Service`, `Course`, `HowTo`, `WebPage`, `CreativeWork`, `Organization`, `LocalBusiness`. Types that need fields the generic document cannot supply (`FAQPage`, `Product`, `Event`) are rejected: use annotations or a plugin builder. Emits `name`, `description`, `url`, `@id`, `image`, `datePublished`, `dateModified` (plus `headline` for the article types). Precedence: plugin builder > `x-schema-org-*` annotations > `schema_org_type` > `Thing`. Validated on write only; an unknown stored value is ignored for JSON-LD and never affects URL routing. |
| `editor` | string | `""` (default: the generic form) or `"post"`: edit entities of this schema in the post (TipTap) editor, as blog posts are, with autosaved drafts, preview and the SEO and AEO panel. The built-in `post`, `article`, `blog_post` and `blog` schemas always use it. Other attributes are kept on save and stay editable on the entity Details page. Validated on write: any other value is rejected, `"post"` needs the schema to declare a string `content` property, and the `person` schema cannot use it. A stored value that is not valid is ignored on read and never affects URL routing. |

Settings are validated on write (pattern syntax, `archive_path`, override
fields). Invalid stored settings are logged and the schema is routed with the
defaults, so one bad row cannot take the public site down.

### JSON-LD URLs and rollout

The entity's own JSON-LD `url` and `@id` are always set from the page's
canonical URL (`meta.canonical_url`: URL pattern, parent slug, SEO canonical
override).
Organization/company schemas keep their `website` attribute as `url`.

Before rolling this out to a site:

- Set `PUBLIC_BASE_URL`. Without it the backend derives the base from the
  request host, which for SSR fetches is the internal backend
  (`localhost:8090`), so every page would publish localhost URLs.
- Plugin templates that already emit their own entity JSON-LD
  (`application/ld+json`) now get a second, overlapping node from core. Set
  `jsonLd: false` on those templates (grep the plugin for `JsonLd` and
  `application/ld+json`).

Example — a documentation schema with a report variant:

```json
{
  "url_pattern": "/docs/{slug}",
  "archive_path": "/docs",
  "overrides": [
    {"attribute": "format", "equals": "report", "url_pattern": "/reports/{slug}", "sitemap_group": "misc"}
  ]
}
```

## Defaults (empty settings)

| Schema                                   | Pattern          | Notes |
|------------------------------------------|------------------|-------|
| `page`                                   | `/{slug}` | sitemap group `misc` |
| post family (`post`, `blog`, `article`, `blog_post`) | `/blog/{slug}` | archive `/blog`, sitemap group `blog`. The first family member present that no explicit schema claims owns the route; the other unclaimed members are its aliases. A family slug is claimed when an explicit schema is that slug or lists it in `aliases`; claimed slugs are left to that schema (e.g. `blog` at `/journal/{slug}` without a `post` alias leaves `post` on `/blog/{slug}`). |
| any other schema                         | `/{slug}` | |

Installs that already served posts keep their URLs: the locale-removal
migration (`202610151230_kill_locales.sql`) pins `url_pattern: /post/{slug}`,
`archive_path: /post` and `sitemap_group: post` on the post-family schema of
every install that already had posts, and strips the old language segment
from explicit patterns. Only new installs get `/blog/{slug}`.

## Precedence

URL shape is per schema, not per entity. When a
request path matches several patterns, the one with more literal segments wins,
then the one with fewer tokens. Plugin registries (`RegisterArchiveGroup`,
`RegisterEntityURLBuilder`, `RegisterStaticSitemapRoute`, the `sitemap.urls`
filter) keep working; explicit settings take precedence over a plugin URL
builder for the same schema.

## Resolution

The Go renderer resolves every public path (there is no JSON resolve API):

1. Checks the redirect table for the full path (exact rules, then wildcard
   rules; see `REDIRECTS.md`).
2. Tries the router's candidates in order; each looks up a published **base**
   row (`variant_key IS NULL`) by schema (schema + aliases) and slug, or, for a
   `{path}` pattern, by the parent chain; a pattern with date tokens also
   requires the date to match.
3. Loads the page and renders it. The canonical URL comes from the match.
   404 when nothing serves the path.

A schema's archive (`archive_path`) lists its rows and those of its aliases
(the archive of `post` includes `blog` rows and vice versa).

## Sitemap rules

* `sitemap.xml` is the index; each `sitemap_group` gets one child
  `sitemap-<group>.xml`: the section index (`archive_path` or section page)
  first, then its rows.
* `misc` collects pages, overrides with group `misc`, the homepage (`/`) and
  plugin static routes.
* The index lists only non-empty children.
* Priority/changefreq come from `settings.sitemap`, else the schema defaults.
* Entities with `no_index` and rows of `sitemap.exclude` schemas are omitted.
* `{path}` and date URLs are listed at their canonical path (the same one the
  page's `canonical` and JSON-LD `url` use).
* The cache is invalidated when schema settings change.

## Admin

* The Go admin asks the registry (`backend/internal/entity/url_registry.go`) for
  "view on site" links, so `{path}` and date patterns resolve like the public site.

## Paged builder loops

A builder `loop` with `paging: "pages"` owns `?page=N` of the page it is on (any path, not only schema archives).
Page 1 has no parameter. The head is self-canonical (`?page=N` for N of 2 or more) with `rel=prev` and `rel=next`,
and there is no extra `noindex`, like archive listings. `?page=N` past the last page is a quiet 404. A request whose
query is exactly `page=N` (canonical decimal, 2 to 1000) is cached under its own key only when the page really has a
paged loop and is in range; every other query string, and every page without a paged loop, stays uncached. Any
entity write purges these entries with the rest of the page cache.

## Site checklist

1. Put the schema `settings` in the site's own migration/seed — never in core.
2. Add a theme template for the schema if the generic one is not enough.
3. Request each URL and compare the sitemap URL set before and after.
