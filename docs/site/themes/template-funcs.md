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

`cta`, `safeurl` and `safeHref` are the same function: they validate an href and allow only `http`, `https`, `mailto`, `tel`, `sms` and relative targets. Anything else becomes `#`. `telHref` builds a `tel:` link, keeping digits and a leading `+`.

`imgsrc` allows http, https, relative and raster `data:` image URLs and returns `""` otherwise. `embedAllowed` reports whether an iframe address is on the allowed list, and additionally accepts Google Maps and OpenStreetMap embeds. `srcset .Entity.Image` returns `"/media/a_medium.jpg 300w, ..."` built from the resized copies of a stored media file (the square thumbnail is skipped). It returns `""` for remote URLs and for files with no copies.

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

## HTML helpers

**`sanitisedHTML v`** runs rich text, such as a TipTap body in `.Attrs.content`, through the rich-text sanitiser with `data-hm-*` stripped, and emits it unescaped. A missing or non-string value gives an empty string.

**`sanitisedSectionHTML v`** applies the policy html sections are sanitised with on save, and emits it unescaped. Use it for markup a plugin function built, because the rich-text policy drops `id`, `style`, `aria-*` and `data-*`, which such fragments need. It keeps `class`, `id`, `role`, a vetted `style`, a fixed set of `aria-*` and `data-*`, and links with allowed schemes. It removes `script`, `style` blocks, forms, event handlers, `javascript:` URLs, `data-hm-*` and iframes off the allowlist.

**`hl lang source`** highlights source text for `code` fields. It HTML-escapes the source first, then wraps strings, comments and a small keyword set in `<span class="hm-tok hm-tok--k|s|c">`, so the output is built only from escaped input. `#` comments are highlighted for every language except `go`, `js`, `javascript` and `html`.

## Navigation helpers

**`menu "header"`** returns the items of the menu at a location, like `.Site.Nav`. Each `NavItem` has `Label`, `Href`, `Target` and `Children`.

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

## Content queries

All of these are fail-open: an error or timeout gives no items and the page still renders. Limits for `recent`, `related` and `terms` are clamped to 1 to 50.

- `recent "post" 5` returns the newest published entities of a schema.
- `related . 3` is the same as `call .Related 3`; pass `$` inside a `range`.
- `terms "category" 10` returns terms of a kind with published entities, most used first (`.Name`, `.URL`, `.Count`, `.Description`, `.Parent`).

### pageNum and queryParam

`pageNum .` is the request's `?page=` as an `int`: 1 by default, clamped to 1 to 1000, on every view. Inside a section pass its own `.`, which carries the page number too.

`queryParam . "name"` is the first value of the query parameter `name` as a string, cut to 200 bytes, or `""`. A request with a query string is never cached, so a theme can branch on it (for example `?days=7`) without a cache key.

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
