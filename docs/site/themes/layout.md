---
title: Layout and template hierarchy
nav_title: Layout
description: Every file a theme can hold, which template draws which kind of page, the data each template receives, and worked examples.
---

This page lists the files a theme can hold, how the engine picks a **layout** (the template for a whole page) for each kind of page, and the data those layouts receive. To make your first layout, follow [Build your first theme](your-first-theme.md).

## Directory layout

```text
my-theme/
  theme.json            name, version, settings_schema, builder options
  layouts/
    base.html           the document shell; calls the "content" block
    page.html           defines "content"; the last fallback of every kind
    single.html         optional: a single entity; single-<schema>.html for one schema
    home.html           optional: the homepage
    archive.html        optional: a schema archive; archive-<schema>.html for one schema
    taxonomy.html       optional: a category or tag page; taxonomy-<kind>.html for one kind
    author.html         optional: an author page
    search.html         optional: /search
    404.html            defines "content" for not-found
    error.html          defines "content" for 5xx
  partials/
    head.html header.html nav.html footer.html section.html scripts.html doc.html
    plugin-assets.html  the plugin-head and plugin-body slots
  sections/
    hero.html           defines "section-hero"
    faq.html            defines "section-faq"
  schemas/
    hero.json           JSON Schema (subset) for the section's content
  assets/
    css/base.css        bundled first, then every other css/*.css, then css/sections/*.css
    css/sections/hero.css
    js/hm.js
    icons/              site icons served at the site root
```

Because of the **lookup chain** (your theme first, then the embedded default theme; the first that has a file wins, see [Understand how themes work](overview.md)), you only ship what you change. The embedded default theme supplies the rest. The default theme is a useful reference: its layouts and partials live in `backend/internal/render/themes/default/` in the repository.

`layouts/base.html` is the shell. It receives the whole view and calls `{{template "content" .}}` inside `<main>`; each other layout only defines `content`. The default `base.html` also prints the `<html lang>` and `data-hm-contract` attributes, the head partial, the stylesheet link, a skip link and the two plugin slots.

## Template hierarchy

The engine picks the first layout that exists, trying the candidates in order:

| View | Candidates |
| --- | --- |
| entity page | `single-<schema>`, then `single`, then `page` |
| homepage | `home`, then `page` |
| schema archive (`/blog`) | `archive-<schema>`, then `archive`, then `page` |
| term archive (`/category/x`, `/tag/x`, `/genre/x/y`) | `taxonomy-<kind>` (`category`, `tag` or a custom kind), then `taxonomy`, then `page` |
| author (`/author/x`) | `author`, then `page` |
| search (`/search?q=`, optional `&type=<schema slug>`) | `search`, then `page` |
| not found or error | `404` or `error` |

A theme may omit any of them. A schema-specific layout is `layouts/single-<schema-slug>.html`; the old `layouts/<schema-slug>.html` is no longer read. The slug must match `[a-z][a-z0-9_-]*`. A slug that is not a valid name falls back down the chain, and the slug is only a lookup key, never used to build a path. A broken schema-specific layout rejects the theme version like any other layout.

For each view the engine tries every candidate in the highest layer of the lookup chain before it moves to the next layer. So a theme that ships only `page.html` renders posts through its own `page.html`, not through the default `single.html`.

The default theme also ships `single-doc.html`, the layout behind the documentation pages of this site, described [below](#the-documentation-layout), and `archive-release.html`, an archive layout for a schema with the slug `release`.

## View data

Every layout receives these fields. Which ones are filled depends on the view kind.

| Field | Meaning |
| --- | --- |
| `.Kind` | `single`, `home`, `archive`, `taxonomy`, `author`, `search` or `page` |
| `.Path` | the request path |
| `.Entity` | entity pages: `.Title`, `.Slug`, `.URL`, `.Schema`, `.Excerpt`, `.Image`, `.Attributes` (same as `.Attrs`), `.Terms` (plus `.Categories`, `.Tags`), `.Author`, `.PublishedAt`, `.UpdatedAt`. Nil on listings |
| `.Body` | `attributes.content` sanitised with the rich-text policy at render time (stored content is never trusted); empty when there is none |
| `.Blocks` | the prepared sections of the page, derived from its `page_doc` (a page without a document: its legacy `page_sections`) |
| `.Items` | listings: cards with `.Title`, `.URL`, `.Excerpt`, `.Image`, `.Category`, `.PublishedAt`, `.Author`, `.AuthorURL` |
| `.Pagination` | listings: `.Page`, `.TotalPages`, `.Total`, `.HasPrev`, `.HasNext`, `.PrevURL`, `.NextURL`, `.URL n` |
| `.Term`, `.Author`, `.Query`, `.SearchType` | taxonomy, author and search views. `.SearchType` is the schema slug a search is limited to, empty for the whole site |
| `.Site` | `.Name`, `.Tagline`, `.LogoURL`, `.Language`, `.Phone`, `.Hours`, `.Address`, `.Settings` (extras of the site's design tokens object, not the theme settings), `.Nav`, `.FooterNav`, `.SocialLinks` |
| `.Settings` | the active theme's settings: the values of its `settings_schema`, with schema defaults merged in, so a key with a `default` is always present. A read-only `map[string]any`; strings are escaped like any other value. Section templates read the same map |
| `.Tokens` | the owner's colours, fonts and corner radii from **Theme > Colours and fonts**, only the values changed from the stock defaults (see [Builder contract](builder-contract.md)) |
| `.CSSHref` | the address of the theme's bundled stylesheet (see [Assets, icons and feeds](assets-icons-feeds.md)) |
| `.Attrs` | entity pages: a deep copy of the entity's attributes without `page_sections` and `page_doc`. Numbers are `float64`, strings are plain text and the template escapes them, missing keys read as empty |
| `.SchemaSlug` | the entity's schema slug |
| `.PublishedAt`, `.UpdatedAt` | `time.Time`, zero when unknown |
| `.Related` | `func(limit int) []RelatedItem` |

`.Site.Language` is the site setting `language` (default `en`); it also feeds `<html lang>` and the JSON-LD `inLanguage`.

An entity's byline, `.Entity.Author`, is the person it credits, the site organisation, or a plain `author` or `author_name` attribute string. It is never the account that created the entity. Its `.URL` is set only when the person has made their profile public and the entity's schema has an archive; otherwise the name shows unlinked. Drafts never reach `.Attrs` on the public site; the admin preview fills it from the posted attributes.

### Related items

`{{range call .Related 3}}...{{end}}` lists other published entities of the same schema, newest first, excluding the current page. The limit is clamped to 1 to 12. The query runs lazily, at most once per render, so pages that never call it run no query. `.Related` is never nil, and returns nothing on archive, 404, error and unsaved-preview views, so `call` is always safe, even in a partial shared with those layouts. If the query fails the page is served without the block but not cached.

Each item is a `RelatedItem`, the card shape shared by `recent`, `related`, `query` and the listings: `Title`, `Name`, `URL`, `Excerpt`, `Image`, `Category`, `PublishedAt`, `Author`, `AuthorURL`, `WordCount`, `ReadingTime` and `Attrs` (a read-only copy of the attributes without `content` and `page_sections`, so cards stay light).

## A single-schema layout

This layout renders every entity of the `post` schema. It uses entity data instead of builder sections.

```gotemplate
{{define "content"}}
<article class="hm-container hm-article">
  <header>
    <h1>{{.Attrs.title}}</h1>
    <p class="hm-meta">
      {{with .Entity.Author}}<span>{{if .URL}}<a href="{{.URL}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</span>{{end}}
      <time datetime="{{formatDate .PublishedAt "2006-01-02"}}">{{formatDate .PublishedAt "2 January 2006"}}</time>
      <span>{{readingTime .Attrs.content}} min read</span>
    </p>
  </header>
  <div class="hm-prose">{{sanitisedHTML .Attrs.content}}</div>
  {{range .Blocks}}{{template "section" .}}{{end}}
  {{with call .Related 3}}
  <aside aria-label="Keep reading">
    {{range .}}<a href="{{.URL}}">{{.Title}}</a>{{end}}
  </aside>
  {{end}}
</article>
{{end}}
```

Save it as `layouts/single-post.html`. Notes on the choices: `sanitisedHTML` is the only way attribute HTML is emitted unescaped, and a missing value yields an empty string. Ranging over `.Blocks` keeps any builder sections an editor adds to the post. `formatDate` prints in the value's zone (UTC for stored timestamps) and takes an optional IANA zone as a third argument.

## The documentation layout

Documentation pages use the `doc` schema, so the engine tries `single-doc`, then `single`, then `page`. The default theme's `layouts/single-doc.html` builds the documentation pages of this site from the `sidebar` menu and the page body. It is a good example of a schema-specific layout. Read the real file, and the partials it calls in `partials/doc.html`, in `backend/internal/render/themes/default/` in the repository.

It uses two functions described in [Template functions](template-funcs.md): `navPos` for the breadcrumb trail and the previous and next links, and `docBody` for heading ids and the on-page table of contents. `docBody` adds the heading ids at render time, for this layout only.

### Page shape

The layout uses the whole width of the window instead of the site's centred `hm-container`. From `60rem` wide it has three columns: the sidebar in a 16rem column at the left edge, the article in the middle, and the "On this page" list in a 14rem column at the right edge. The article's breadcrumb, title and text are centred at about 46rem, and the cards start on the same left edge and may extend to the right, in a grid up to 60rem wide. A page without headings gives the article the third column, except from `90rem` wide, where the empty column stays so the article stays centred. Builder blocks of a page are still rendered after the body.

Below `60rem`, the sidebar moves into a closed `<details>` element labelled "Documentation menu", and the aside and the table of contents are hidden. From `60rem` the aside and the table of contents are sticky columns (each scrolls inside itself when it is taller than the window) and the `<details>` copy is hidden. The stylesheet hides one copy with `display:none`, so the page needs no JavaScript and the hidden copy is removed from the accessibility tree. The article comes before the aside in the source and the stylesheet places the columns, so the skip link lands on the article instead of the menu.

### Sidebar

The sidebar is the site's `sidebar` menu as a tree, with a search box on top. It is rendered twice (the `<details>` copy and the aside), so nothing in it has an `id`.

- **Sections.** Each top-level menu item with children is a `<details>` element. Only the one that holds the current page starts open; the match is by `Href`. Its summary is the section name, and the section's own link is the first entry inside, labelled "About" plus the section name, so it is never a second "Overview".
- **Group labels.** A menu item without a link (a `custom` item with an empty URL, which the renderer gives the `Href` `#`) is a label, not a link: it renders as `<span class="hm-doc-nav__group">`, followed by its children. The docs publish tool uses this for "Guides" and "Reference". Labels are skipped in the breadcrumb and are never the previous or next page.
- **Current page.** The link to the current page carries `aria-current="page"`.
- **Search box.** A plain GET form to `/search`, with the page's `.SchemaSlug` sent as a hidden `type` field, so results are limited to the same kind of page (see [Listings, authors and terms](#listings-authors-and-terms)). The hidden field is left out when the slug is empty.

The partials in `partials/doc.html`:

| Partial | Arguments (a `dict`) | Renders |
| --- | --- | --- |
| `doc-search` | `Type`: the schema slug to limit results to | the search form |
| `doc-nav` | `Nav`: a `[]NavItem`; `Cur`: the `Href` of the current page as it appears in the menu (empty when the page is not in the menu); `Open`: the `Href` of the top-level item that starts open | the top level, one `<details>` per section |
| `doc-tree` | `Nav`, `Cur` | the levels below, as nested lists |
| `doc-item` | `Item`: one `NavItem`; `Cur` | one entry: a link, or a group label when it has no link, followed by its children |
| `doc-cards` | `Items`: a `[]NavItem`; `A`, `B`: the items of two `query` pages | the card grids |
| `doc-card` | `Item`, `A`, `B` | one card |

If you copy `single-doc.html` into your own theme, copy `partials/doc.html` too.

### Breadcrumb

The root crumb comes from the schema, not from the template. It is the section crumb of the page's `.Breadcrumb` (the item between Home and the page), so it follows the schema's `archive_path` and no address is written into the layout. A schema without an archive path has no root crumb. Its label is the `archive_crumb_label` setting, else `archive_title`, else the type name. The `doc` schema in `docs/site/doc-schema.json` sets it to "Docs". After the root crumb come the linked items of the menu trail, then the current page.

A page that is not in the menu still renders, without trail, previous or next links.

### Cards

On a section index (a page whose menu item has children) and on the landing page (a page whose path is the parent of the top-level menu items), the layout adds card grids after the body. Direct children share one grid (headed "Sections" on the landing page). Each group label gets an `<h2>` heading and a grid of its own. Each card is the item's link and, when the page has one, its description: the excerpt of the matching published entity, read from two `query` calls (100 items each; the second page is read only when the first reports more pages) that run only on those pages. A page without a description shows its title only; the body text is never used instead. Article pages show no cards.

### The Next steps block

A guide ends with a list of links to the next pages. The stylesheet shows a list with the class `hm-next-steps`, or the list right after a heading with the id `next-steps`, as a row of cards.

### Styles

The styles are in `assets/css/docs.css`, which the bundle picks up after `base.css` like any other file in `css/`. They use only `--hm-*` tokens. The cards use the classes `hm-doc-cards` and `hm-doc-card`, separate from the blog's `hm-card`. The default `nav`, `nav-items` and `nav-link` partials accept both call styles, a plain value or a `dict` with `Items` or `Item` and `Path`, and your overrides can use the `navctx` and `navlink` functions to normalise either shape.

## Listings, authors and terms

**Routes.** Archives live at the schema's `archive_path`. The built-in `/category/{path}`, `/tag/{path}`, `/{kind}/{path}`, `/author/{slug}` and `/search` are fallbacks, tried only after redirects, schema URL patterns and archives found nothing, so an entity or archive at one of those paths always wins. Pagination is `?page=N`, with page 1 carrying no parameter and `rel="prev"` and `rel="next"` in the head. A page past the end shows the last page, and listings show 12 items per page.

**Authors** are people (the built-in `person` type), not accounts. A person's page renders with the `author` layout and `.Kind` of `author`, and `.Author` carries `.Name`, `.Slug`, `.URL`, `.Avatar`, `.JobTitle`, `.Bio` and `.SameAs`. A person is hidden everywhere public until they switch on Public profile. No account email is ever exposed.

**Terms** are hierarchical. `tag` and `category` are built in; any other lower-case kind (`genre`, `series`: 1 to 16 characters of `a-z0-9_-`, starting with a letter) exists as soon as its first term does. Its archive is `/{kind}/{path}` with the ancestors' slugs, such as `/genre/fiction/noir`. A theme sees `.Term.Kind`, so `taxonomy-genre.html` styles one kind.

Taxonomy, author and search views are never cached. Search uses Postgres full-text search with the `simple` configuration and `websearch_to_tsquery` syntax, rate limited per client; results are `noindex`.

**Search by type.** `/search?q=<words>&type=<schema slug>` limits a search to one schema and its aliases. The type is trimmed and lower-cased and must match `^[a-z][a-z0-9_-]*$`. An unknown or malformed type gives a normal page with no results, never a search of the whole site, and the type is kept in the pagination links. The view carries it as `.SearchType`, and the default `search.html` adds a hidden `type` field to its form only when `.SearchType` is set.
