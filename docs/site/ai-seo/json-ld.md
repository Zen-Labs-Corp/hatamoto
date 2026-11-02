---
title: JSON-LD
nav_title: JSON-LD
description: Which structured data each page carries, which schema.org type each content type gets, how plugins extend it and what the checker tests.
---

This page lists the structured data Hatamoto writes into every public page: the blocks a page holds, how the schema.org type of each content type is chosen, how plugins change it and what the built-in checker tests. To check a page step by step, follow [Check your sitemap and structured data](check-sitemap-and-structured-data.md).

## Overview

JSON-LD is a block of machine-readable data in a page that tells search engines and AI assistants what the page is about, in the shared [schema.org](https://schema.org) vocabulary. Hatamoto builds it on the server from the entity, its content type and your site settings, and writes it into the page head as `<script type="application/ld+json">` blocks. You do not write it by hand for ordinary content.

A trimmed entity document for a post:

```json
{
  "@context": "https://schema.org",
  "@type": "Article",
  "headline": "Hello",
  "url": "https://example.com/blog/hello",
  "@id": "https://example.com/blog/hello#article",
  "mainEntityOfPage": {"@type": "WebPage", "@id": "https://example.com/blog/hello#webpage"},
  "publisher": {"@id": "https://example.com#organization"},
  "isPartOf": {"@id": "https://example.com#website"},
  "datePublished": "2026-01-14T09:00:00Z"
}
```

## What a page contains

A rendered page can hold up to four JSON-LD blocks:

1. **The entity document.** One node for the page itself, such as `WebPage` or `Article`. When the entity has FAQ items, a `FAQPage` with one `Question` and `Answer` per item is added next to it inside an `@graph`, unless the entity is itself a `FAQPage`.
2. **A breadcrumb trail**, a `BreadcrumbList`, whenever the page has breadcrumbs. Its `item` addresses use the canonical paths.
3. **A `CollectionPage`** on archive listings such as `/blog`.
4. **The site graph.** An `Organization` and a `WebSite` node tied together by `@id`, built from site settings: name, logo, tagline, phone, opening hours, address and social links. The Organization also carries legal name, tax ID, fax, email, extra profile addresses and an aggregate rating, each only when you fill it in under Settings > General > Business details. The same section holds the extended details: identifier codes (`naics`, `isicV4`, `duns`, `leiCode`, `globalLocationNumber`), `founder`, an external `parentOrganization`, `brand`, `knowsAbout`, `award`, `memberOf`, a `hasOfferCatalog` of your services, and `potentialAction` for booking and ordering links (`ReserveAction`, `OrderAction`). On a site with several public locales, a page of a locale with an office adds a third node, described below. The `WebSite` node carries a `SearchAction` pointing at `/search?q={search_term_string}`, and the article nodes refer back with `publisher` and `isPartOf` references, such as `https://example.com#organization`.

### The local business of a locale

On a site with several public locales, Settings > General lists one card per locale. A locale's pages print a `LocalBusiness` node (or the subtype you choose) with `@id` `https://example.com#localbusiness-<segment>`, a `parentOrganization` reference to the Organization, and the card's address, phone, opening hours, area served and the other fields. A field left empty on the card uses the site-wide value.

The card also takes the extended details. The ones above print on the LocalBusiness too, with the card's own list replacing the site-wide one. These belong to the place and print on the LocalBusiness only, never on the Organization. Reservations and the menu address print only when the business type is a food business (`Restaurant`, `BarOrPub`, `Bakery` or `CafeOrCoffeeShop`), because schema.org defines them for those types only:

| Setting | Property |
|---|---|
| Payment methods | `paymentAccepted` |
| Accepts reservations | `acceptsReservations` (true or false; unset prints nothing) |
| Menu address | `hasMenu` |
| Service radius (km) | a `GeoCircle` in `areaServed`, centred on the card's coordinates (needs both) |
| Public access, Free entry | `publicAccess`, `isAccessibleForFree` |
| Amenities | `amenityFeature`, one `LocationFeatureSpecification` per amenity, with an explicit true or false |

The address is the exception: it is one block. A card with its own street or city uses its whole address; a card with neither has no office, and the site-wide address is never copied into it. A locale marked "No office in this locale", a card without its own address, a page that belongs to no locale and a site with fewer than two locales all print the Organization and WebSite nodes only.

The output is escaped so that content can never close the script element: angle brackets, ampersands and the line-separator characters are encoded, and `</` becomes `<\/`.

## Which type an entity gets

Hatamoto picks the generator for an entity in this order. The first match wins.

| Order | Source | Notes |
|---|---|---|
| 1 | A plugin builder registered for the content type | A plugin that adds a content type can build its own document. |
| 2 | `x-schema-org-type` annotations in the type's JSON Schema | A root annotation sets `@type`, and `x-schema-org-property` on each property maps an attribute to a schema.org property. Nested objects can carry their own type. |
| 3 | The `schema_org_type` setting on the content type | Writes a small, safe document. See below. |
| 4 | Built-in mapping by type slug | See the next table. |

The person type always uses its built-in mapping, which takes its description from the short biography or the biography and adds contact and `sameAs` details.

### The built-in mapping

| Content type slug | `@type` | Fields |
|---|---|---|
| `page` | `WebPage` | `name`, `url`, `@id` ending `#webpage`, `description` (from `meta_description` when set) |
| `post`, `blog`, `article`, `blog_post` | `Article` | `headline`, `url`, `@id` ending `#article`, `mainEntityOfPage` (a `WebPage` ending `#webpage`), `publisher`, `isPartOf`, `description` (from `excerpt` or `meta_description`), `datePublished`, `dateModified`, `author`, `image` |
| `person`, `team_member` | `Person` | Name, description and profile links. |
| `organization`, `company` | `Organization` | Keeps its own `url` from the `website` attribute. |
| `product` | `Product` | Name and product fields. |
| anything else | `Thing` | `name`, `url` and `description`. |

The `author` is a `Person` with the plain author name by default. When the entity is credited to a person, `author` becomes that person's `Person` node (with a `url` and `@id` when the person has a public page). When it is credited to the site organisation, `author` points at the `Organization` by `@id`.

### Typing a content type with schema_org_type

For a custom content type with no annotations, set `schema_org_type` in its settings (see [URL patterns](url-patterns.md#schema-settings)). In the admin it is the **Structured data type (JSON-LD)** field of the entity type, where **Automatic** means no value. Allowed values are `Article`, `BlogPosting`, `NewsArticle`, `DefinedTerm`, `Person`, `Service`, `Course`, `HowTo`, `WebPage`, `CreativeWork`, `Organization` and `LocalBusiness`.

```json
{
  "url_pattern": "/guides/{slug}",
  "archive_path": "/guides",
  "schema_org_type": "HowTo"
}
```

The generated node has `@type`, `name`, `description` (the first of `description`, `meta_description` and `excerpt` that is set), `url`, `@id`, `image` (the first of `featured_image`, `image` and `og_image`), `datePublished` and `dateModified`, plus `headline` for the three article types. Types that need fields a generic document cannot supply, which are `FAQPage`, `Product` and `Event`, are refused on save: use annotations or a plugin builder for those. A stored value that is no longer valid is ignored for JSON-LD and never affects URL routing.

## Editorial provenance

If an entity has editorial metadata, the document is enriched: `reviewed_by` becomes an `editor`, each entry in `sources` becomes a `citation` item, and `last_verified_at` (or `reviewed_at`) becomes `lastReviewed`.

## URLs and the base URL

The entity's own `url` and `@id` are always taken from the page's canonical URL, which comes from the URL pattern, the parent chain and any canonical address set in the editor. Organization and company content types keep their `website` attribute as `url`. See [URL patterns](url-patterns.md).

The base is the public base URL, so set `PUBLIC_BASE_URL` before you rely on any of this. Without it, and with no address saved in the admin, Hatamoto takes the base from the request host, which on a proxied install can be an internal address. See [URL patterns](url-patterns.md#the-public-base-url).

## Extending it with plugins

Two plugin hooks change structured data. See [Hooks and events](../plugins/hooks-and-events.md) for how filters are declared.

- **`jsonld.enrich`** is a filter that receives the entity reference, the base URL and the finished document. A plugin changes the document, for example to add `aggregateRating`. It runs once per generation, on the success path only, so a plugin sees the final document.
- **`render.head`** lets a plugin add `meta`, `link`, `script-src` and `jsonld` tags to the head. Setting `ReplaceSiteGraph` tells Hatamoto to leave out its generic Organization and WebSite graph, because the plugin supplies its own `jsonld` tags. The host builds and escapes the markup, so a plugin never provides raw HTML.

If a plugin or a custom theme template already writes its own entity JSON-LD, the page has two overlapping nodes. Remove the duplicate from the template, or have the plugin use `jsonld.enrich` instead. There is no per-template switch to turn the built-in output off.

## Content negotiation

Send `Accept: application/ld+json` to an entity page and Hatamoto answers with the bare JSON-LD for that entity instead of HTML.

```bash
curl -s -H 'Accept: application/ld+json' https://example.com/blog/hello
```

The same documents, many at once, are available from the feed `GET /api/v1/public/knowledge-graph`. See [robots.txt and llms.txt](robots-llms.md#the-knowledge-graph-feed).

## The checker

The MCP tool `jsonld_validate` takes an `entity_id` and checks the page's JSON-LD. When the entity is published it reads the blocks from the live page. Otherwise it generates them from the draft. The answer holds the `entity_id`, the `url`, a `source` field that says which of the two it used, and a `report` with `valid`, the number of `blocks`, one entry per typed node with its `errors` and `warnings`, and block-level `errors`.

The check covers valid JSON, a top-level `@context`, an `@type` on every node, and the fields each known type needs. A missing required field is an error (`<Type>: missing required field "<name>"`), and a missing recommended one is a warning (`<Type>: recommended field "<name>" is missing`):

| Type | Required | Recommended |
|---|---|---|
| `Article`, `BlogPosting`, `NewsArticle` | `headline`, `author`, `datePublished` | `image`, `dateModified`, `publisher` |
| `WebPage` | `name` | `description` |
| `AboutPage`, `ContactPage`, `CollectionPage`, `ProfilePage`, `Person` | `name` | none |
| `WebSite` | `name`, `url` | none |
| `Organization` | `name` | `url`, `logo` |
| `LocalBusiness` | `name`, `address` | `telephone`, `openingHours` |
| `Product` | `name` | `image`, `description`, `offers` |
| `Service` | `name` | `description`, `provider` |
| `SoftwareApplication` | `name` | `offers` |
| `Event` | `name`, `startDate`, `location` | `endDate`, `description` |
| `FAQPage` | `mainEntity` | none |
| `Question` | `name`, `acceptedAnswer` | none |
| `Answer` | `text` | none |
| `HowTo` | `name`, `step` | none |
| `BreadcrumbList` | `itemListElement` | none |
| `ListItem` | `position` | none |
| `ImageObject` | `url` | none |
| `VideoObject` | `name`, `uploadDate`, `thumbnailUrl` | `description` |
| `PostalAddress` | none | none |

Other types only get their `@type` checked, because the checker guards against broken output and is not a full schema.org validator. For rich-result eligibility, also run a page through Google's Rich Results Test.

To look at a page's blocks without MCP:

```bash
curl -s https://example.com/blog/hello \
  | grep -o '<script type="application/ld+json">[^<]*' | head -c 1500
```

## Limits and errors

- The JSON-LD `url` values change when the URL pattern, the public base URL or, for dated addresses, the site time zone changes. Compare them before and after such a change, as [Check your sitemap and structured data](check-sitemap-and-structured-data.md) describes.
- A `schema_org_type` outside the allowed list, or one of `FAQPage`, `Product` and `Event`, is refused when the content type is saved.
- An entity whose document cannot be built is left out of the knowledge-graph feed.
