---
title: Template functions
nav_title: Template functions
description: Every function you can call from a theme template, with what it returns and how it behaves on bad input.
---

This page lists every function you can call from a theme template, grouped by what it does: editor markers, safe links and images, lists and numbers, text, dates, HTML, navigation, content queries and plugin functions. To see several of them at work in one section, follow [Add a section](add-a-section.md).

## Behaviour

Every function here is generic (nothing in it knows a site, a language or a schema) and fail-open: bad input gives a zero value, never a failed render. The one exception is `dict`, which returns an error for an odd number of arguments or a non-string key. None returns trusted markup except `sanitisedHTML`, `sanitisedSectionHTML` and `hl`, so `html/template` escapes every other result. This page was checked against the function maps in `backend/internal/render/funcs.go`, `funcs_extra.go` and `datafuncs.go`.

## Markers

Never write `data-hm-*` attributes by hand. The `section` partial already adds `<section {{hmsec .}}>`, so section templates do not repeat it.

- `hmsec .` prints `data-hm-section` and `data-hm-type`.
- `hmf "heading"` prints `data-hm-field="heading"`. Path segments are joined with dots, so `{{hmf "faqs" $i "answer"}}` gives `faqs.2.answer`.
- `hment id` prints `data-hm-entity` and is used on `<main>` in `base.html`.

See [Builder contract](builder-contract.md) for what the editor does with them.

## URLs and media

`cta`, `safeurl` and `safeHref` are the same function: they validate an href and allow only `http`, `https`, `mailto`, `tel`, `sms` and relative targets. Anything else becomes `#`. `telHref` builds a `tel:` link, keeping digits and a leading `+`. A `mailto:` link from these functions is encoded after the page is rendered (see [Email protection](engine-details.md#email-protection)), so a theme must not compare the rendered HTML against the literal address.

`imgsrc` allows http, https, relative and raster `data:` image URLs and returns `""` otherwise. `embedAllowed` reports whether an iframe address is on the allowed list, and additionally accepts Google Maps and OpenStreetMap embeds. `srcset .Entity.Image` returns `"/media/a_medium.jpg 300w, ..."` built from the resized copies of a stored media file (the square thumbnail is skipped). It returns `""` for remote URLs and for files with no copies. It lists only copies in the original format. `picturesources .Entity.Image "100vw"` (the second argument is the `sizes` attribute) returns the `<source>` elements (AVIF, then WebP) for the same file, or `""` when there are none; put it inside `<picture>` before the `<img>`: `<picture>{{picturesources .Entity.Image "100vw"}}<img src="..." srcset="{{srcset .Entity.Image}}" sizes="100vw"></picture>`.

### Iframe embed hosts

Both sanitiser policies (rich text and html sections) and `embedAllowed` keep an `<iframe src>` only for https URLs on an allowed host. `HATAMOTO_EMBED_HOSTS` is a comma-separated list of host names, with no scheme, port or wildcard. Unset, the default is YouTube and YouTube-nocookie (path `/embed/`, with and without `www.`) and `player.vimeo.com` (path `/video/`). A set list replaces the default. The editor's embed tool and the WordPress importer only produce YouTube-nocookie and Vimeo addresses, so list those hosts again if editors embed videos, or existing videos disappear at the next save and at render. Invalid entries are ignored, and changing the variable needs a restart.

## Collections and arithmetic

`dict k1 v1 k2 v2 ...` builds a map, `list a b c` a slice, `seq n` the integers 0 to n minus 1 (at most 1000), `join slice sep` joins values, and `default fallback value` returns the fallback for an empty value. `year` returns the current year.

Arithmetic works on integers. Floats are truncated, numeric strings are parsed, anything else counts as 0, and operands are clamped to about plus or minus 2 billion so a result never wraps.

| Func | Result |
| --- | --- |
| `add a b`, `sub a b`, `mul a b` | integer result |
| `div a b` | truncates toward zero; division by zero gives 0 |
| `mod a b` | remainder; modulo by zero gives 0 |

## Strings

The subject comes first, like `hasPrefix`. Numbers are accepted where a string is expected, and other values (`nil`, maps) read as `""`.

| Func | Result |
| --- | --- |
| `lower s`, `upper s` | case-mapped string |
| `trim s` | surrounding whitespace removed |
| `trimPrefix s p`, `trimSuffix s p` | `s` without the prefix or suffix when present |
| `contains s sub`, `hasPrefix s p`, `hasSuffix s p` | `bool` |
| `replace s old new` | every `old` replaced by `new` |
| `split s sep` | `[]string`, at most 1000 parts (an empty `sep` splits into characters) |
| `atoi v` | `int` from a string, number or `nil`; anything unparseable is `0` |

## Dates, reading time and JSON

`formatDate t layout [zone]` takes a `time.Time`, `*time.Time` or an RFC 3339 or `2006-01-02` string, and a Go time layout (`"2 January 2006"` gives `13 October 2026`). Zero or unparseable values give `""`, and month names are English. Dates print in the value's own zone (UTC for stored timestamps). Pass an IANA zone as a third argument to convert first: `formatDate .PublishedAt "2 January 2006" "Australia/Sydney"`. An unknown zone is ignored.

`readingTime v` returns whole minutes (200 words per minute) of an HTML string's text: at least 1 for any text, 0 for none.

`fromJSON s` decodes a JSON string (plugin functions always return strings) into `map[string]any`, `[]any`, `string`, `float64`, `bool` or `nil`. Input over 1 MiB, empty input and invalid JSON give `nil`:

```gotemplate
{{with fromJSON (plugin_acme_rows .Path)}}{{range .}}<li>{{.title}}</li>{{end}}{{end}}
```

To show a value that changes often, such as a price, have the plugin keep it in its own store and print it with `{{ plugin_<name>_<func> ... }}`. The result is escaped as text and needs no approval. A plugin that must change content itself can ask the owner for an approval on its **Automatic updates** tab; see [Data, routes and cron](../plugins/data-routes-cron.md#update-content-on-a-schedule-standing-approvals).

## HTML helpers

**`sanitisedHTML v`** runs rich text, such as a TipTap body in `.Attrs.content`, through the rich-text sanitiser with `data-hm-*` stripped, and emits it unescaped. A missing or non-string value gives an empty string.

**`sanitisedSectionHTML v`** applies the policy html sections are sanitised with on save, and emits it unescaped. Use it for markup a plugin function built, because the rich-text policy drops `id`, `style`, `aria-*` and `data-*`, which such fragments need. It keeps `class`, `id`, `role`, a vetted `style`, a fixed set of `aria-*` and `data-*`, and links with allowed schemes. It removes `script`, `style` blocks, forms, event handlers, `javascript:` URLs, `data-hm-*` and iframes off the allowlist.

**`hl lang source`** highlights source text for `code` fields. It HTML-escapes the source first, then wraps strings, comments and a small keyword set in `<span class="hm-tok hm-tok--k|s|c">`, so the output is built only from escaped input. `#` comments are highlighted for every language except `go`, `js`, `javascript` and `html`.

## Navigation helpers

**`menu "header"`** returns the items of the menu at a location, like `.Site.Nav`, for example `menu "footer"` or a location the theme declares such as `menu "follow"`. A location with no menu, or a menu with no items, returns an empty list. Each `NavItem` has `Label`, `Href`, `Target` and `Children`.

**`navctx` and `navlink`** normalise the two call styles of the default navigation partials (a plain `[]NavItem`, or a `dict` with `Items` and `Path`).

**`paginate .Pagination`** returns page links `{N, URL, Current, Gap}` for the first page, the last page and the current page plus or minus 2, with gaps.

**`navPos nav path`** locates a path in a menu tree and returns a `NavPosition`:

| Field | Meaning |
| --- | --- |
| `.Trail` | ancestors of the current item, root first, excluding the current item (parents without an `Href` may appear) |
| `.Current` | the matching `NavItem`, or nil |
| `.Prev`, `.Next` | the depth-first neighbours that have an `Href`, or nil. A menu item without a link has the `Href` `#`, which counts as no link, so group labels are never a neighbour |

The walk is depth-first, parents before children, and the path is matched after removing the query string, fragment and trailing slash. A missing path, an empty menu or nil input returns the zero value.

```gotemplate
{{$pos := navPos (menu "sidebar") .Path}}
{{with $pos.Prev}}<a rel="prev" href="{{.Href}}">{{.Label}}</a>{{end}}
{{with $pos.Next}}<a rel="next" href="{{.Href}}">{{.Label}}</a>{{end}}
```

**`docBody .Body`** takes the already-sanitised body and returns a `DocBody` with two fields: `.HTML`, the body with an `id` on every `h2` and `h3`, and `.TOC`, a list of `{ID, Text, Level}` items where `Level` is 2 or 3. Ids are a lower-case ASCII slug of the heading text, an empty slug becomes `section`, collisions are suffixed `-2`, `-3`, the ids `main-content`, `toc` and `top` are never used, and a heading that already has an id keeps it. An empty body returns the zero value, and TOC text is escaped by `html/template`. Nothing runs unless a layout calls it.

```gotemplate
{{$b := docBody .Body}}
<div class="hm-prose">{{$b.HTML}}</div>
{{range $b.TOC}}<a href="#{{.ID}}">{{.Text}}</a>{{end}}
```

## Text, list and pager helpers

All of these are pure and fail-open: bad input gives the zero value, never an error. None returns `template.HTML` except the `html` key of `toc`, which only accepts `template.HTML` (like `docBody`), so `html/template` still escapes every other result.

**`excerpt s n`** returns the plain text of `s` (script and style blocks dropped, tags replaced by a space, entities decoded, whitespace collapsed) cut to at most `n` characters at a word boundary, with `...` appended when it was cut. `n <= 0` gives `""`.

```gotemplate
<p>{{excerpt .Entity.Content 160}}</p>
```

**`initial s`** returns the first letter of `s`, upper-cased, or `""` when it has none.

```gotemplate
<span class="avatar">{{initial .Author.Name}}</span>
```

**`isBlankHTML s`** is true when `s` shows nothing: no text (non-breaking spaces count as blank, script and style are ignored) and no `img`, `iframe`, `video`, `audio`, `embed`, `object`, `svg`, `picture` or `canvas`. Empty input is blank.

```gotemplate
{{if not (isBlankHTML .Body)}}<div class="hm-prose">{{.Body}}</div>{{end}}
```

**`toc body`** takes a `template.HTML` value such as `.Body` (a plain string fails the render, so untrusted text is never promoted to markup), gives its `h2` headings an id and returns a map with two keys: `html` (the body with the ids) and `items` (a list of `{ID, Text}`). Ids follow the `docBody` rules (slug, `-2`, `-3`, reserved ids skipped, an existing id kept). Only `h2` counts, and headings inside `pre` or `code` are ignored. Empty input gives `html` `""` and no items.

```gotemplate
{{$t := toc .Body}}
<ol>{{range $t.items}}<li><a href="#{{.ID}}">{{.Text}}</a></li>{{end}}</ol>
<div class="hm-prose">{{$t.html}}</div>
```

**`groupBy items attr`** groups a slice in first-seen order and returns a list of `{Key, Items}`. `Items` is a slice of the input's own type, so `.Title` still works. `attr` is looked up per element as a map key, then an exported struct field, then an entry of the struct's `Attrs`. The key is the value as text (`""` when missing). Only the first 1000 elements are used; anything that is not a slice gives no groups.

```gotemplate
{{range groupBy .Items "Category"}}<h3>{{.Key}}</h3>{{range .Items}}<a href="{{.URL}}">{{.Title}}</a>{{end}}{{end}}
```

**`sortBy items attr ...`** returns a stably sorted copy of a slice (the input is untouched), with the same lookup as `groupBy`. Extra arguments are further sort keys. Two numbers compare as numbers, anything else as text, and elements missing the attribute come last. Only the first 1000 elements are used; anything that is not a slice gives nil.

```gotemplate
{{range sortBy .Items "order" "Title"}}{{.Title}}{{end}}
```

**`pager .Pagination`** returns rows `{Kind, Page, Href, Current}` for a pagination control. `Kind` is `prev`, `page`, `gap` or `next`: the first page, the last page and the current page plus or minus 1 are `page` rows, a `gap` row (no `Page` or `Href`) marks skipped pages, and `prev` and `next` are left out on the first and last page. Like `paginate`, it takes a `*Pagination`; one page, nil or another type give no rows.

```gotemplate
<nav>{{range pager .Pagination}}{{if eq .Kind "gap"}}<span>...</span>{{else}}<a href="{{.Href}}"{{if .Current}} aria-current="page"{{end}}>{{.Kind}} {{.Page}}</a>{{end}}{{end}}</nav>
```

**`videoID url`** returns the id of a YouTube (`watch?v=`, `youtu.be/`, `/embed/`, `/shorts/`, `/live/`, also the `youtube-nocookie.com` host) or Vimeo (`vimeo.com/<digits>`, `player.vimeo.com/video/<digits>`) http or https URL, and `""` for anything else. It does not check the embed host allowlist.

```gotemplate
{{with videoID .Attrs.video}}<iframe src="https://www.youtube-nocookie.com/embed/{{.}}"></iframe>{{end}}
```

**`percent a b`** returns `a / b` as a whole percent, rounded half up (`percent 2 3` is 67). A zero `b` gives 0.

```gotemplate
<progress value="{{percent .Done .Total}}" max="100"></progress>
```

## Content queries

All of these are fail-open: an error or timeout gives no items and the page still renders. Limits for `recent`, `popular`, `related` and `terms` are clamped to 1 to 50.

- `recent "post" 5` returns the newest published entities of a schema.
- `popular "post" 5 30` returns the published entities of a schema with the most pageviews over the last 7, 30 or 90 days (default and fallback 30), the newest filling any gap. Views are the pageview beacons of the daily traffic rollup (visitors without script and bots are not counted), summed per entity over UTC days including today; only the 100 busiest pages of each day are kept, so a small page on a busy site can have no views. A landing path counts for the entity it serves: a trailing slash, a locale prefix and upper case add up (stored paths are lower case, so a slug with capitals or non-ASCII letters is never ranked). Entities without views follow, newest first, so the list is always full. The ranking refreshes about once a minute, and no count is ever shown.
- `related . 3` is the same as `call .Related 3`; pass `$` inside a `range`.
- `terms "category" 10` returns terms of a kind with published entities, most used first (`.Name`, `.URL`, `.Count`, `.Description`, `.Parent`).

### pageNum and queryParam

`pageNum .` is the request's `?page=` as an `int`: 1 by default, clamped to 1 to 1000, on every view. Inside a section pass its own `.`, which carries the page number too.

`queryParam . "name"` is the first value of the query parameter `name` as a string, cut to 200 bytes, or `""`. A request with a query string is never cached, so a theme can branch on it (for example `?days=7`) without a cache key.

### t

`t . "key"` returns the theme's text for `key`, in the language of the page. `t . "n_results" .Total` also replaces every `{n}` in the text with its first extra argument. Pass the page data as the first argument: `.` in a layout, partial or section, or `$` inside a `range`. A partial called with `dict` has no page data of its own, so hand it the strings: `{{template "pager" dict "Strings" .Strings}}`, then `{{t . "next"}}` inside it. Without `Strings` the partial prints the theme's default text, not the page language's.

A key the theme did not declare returns the key itself, so a typo shows up on the page. The result is plain text and `html/template` escapes it where it prints, in an attribute as well as in the body. Keys are declared in `theme.json` and translated in the admin, see [Translate theme strings](translate-theme-strings.md).

```gotemplate
<a href="{{.Pagination.NextURL}}">{{t . "next"}}</a>
<p>{{t . "n_results" .Pagination.Total}}</p>
```

### query

`query dict` lists published entities on any view and returns `{Items []RelatedItem, Total, Page, Pages, Limit int}`. Only `schema` is required.

| Key | Meaning |
| --- | --- |
| `schema` | schema slug (its aliases are included) |
| `attr` and `eq` | `attributes[attr]` equals `eq`: a string, or a number or boolean spelled the same. `attr` without `eq` requires the attribute to exist. A nil `eq` matches nothing, so guard optional values with `{{with}}`. Keys are `[A-Za-z0-9_-]`, at most 64 |
| `author` | person slug; a person who is not public matches nothing |
| `term` | `"<kind>:<slug>"`, such as `"category:news"` |
| `order` | `-published_at` (default), `published_at`, `name`, `-name`, `attr:<key>`, `-attr:<key>` |
| `limit` | 1 to 100, default 12 |
| `status` | `published` (default) or `published+scheduled`, which adds scheduled items with only name, title, schema, date and the filter and order attributes, so embargoed content never leaks |
| `page` | 1 to 1000, and a page past the end returns the last page |

An unknown author, a missing or malformed term, an invalid `attr` key or an unknown schema gives an empty result, never an unfiltered one. It costs one `COUNT`, one `SELECT` and one batched author lookup, whatever the limit. Call it once and keep the result in a variable: a section that calls it runs it on each of its two executions.

```gotemplate
{{$r := query (dict "schema" "post" "attr" "series_slug" "eq" .Attrs.series_slug "order" "attr:series_part" "limit" 20 "page" (pageNum $))}}
{{range $r.Items}}<a href="{{.URL}}">{{.Attrs.series_part}}. {{.Title}}</a>{{end}}
{{if lt $r.Page $r.Pages}}<a href="?page={{add $r.Page 1}}">Next</a>{{end}}
```

**`attrValues schema attr limit`** returns the distinct non-empty string values of an attribute across a schema's published entities as `[]{Value, Count}`, sorted by value, with `limit` 1 to 500. It is one aggregate query: `{{range attrValues "post" "category" 50}}{{.Value}} ({{.Count}}){{end}}`.

## Plugin functions

A plugin registers `plugin_<plugin>_<name>`, with `-` in the plugin name becoming `_`: plugin `acme-seo`, function `badge`, template `plugin_acme_seo_badge`. Results are strings, bools, numbers or slices and maps of those, and are always escaped. Plugin partials are included with `{{template "plugin/acme_seo/badge" .}}`.
