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
4. **The site graph.** An `Organization` and a `WebSite` node tied together by `@id`, built from the default locale's business details and the site settings: name, logo, tagline, phone, opening hours, address and social links. The Organization also carries legal name, tax ID, fax, email, extra profile addresses and an aggregate rating, each only when you fill it in on the default locale's card under Settings > Locales > Business details. The same section holds the extended details: identifier codes (`naics`, `isicV4`, `duns`, `leiCode`, `globalLocationNumber`), `founder`, an external `parentOrganization`, `brand`, `knowsAbout`, `award`, `memberOf`, a `hasOfferCatalog` of your services, and `potentialAction` for booking and ordering links (`ReserveAction`, `OrderAction`). When one of these is filled in, Hatamoto prints the key itself, so a `render.site_graph` plugin that sets the same key on the Organization (`brand`, `founder`, `knowsAbout`, `award`, `memberOf`, `hasOfferCatalog`, `potentialAction`, `parentOrganization`, `additionalProperty` and the identifier codes) is overridden, as for any key the core node already prints. On a site with several public locales, a page of a locale with an office adds a third node, described below. The default locale can print one too, when its **This locale has an office** switch is on and its business has a street or a city. The `WebSite` node carries a `SearchAction` pointing at `/search?q={search_term_string}`, and the article nodes refer back with `publisher` and `isPartOf` references, such as `https://example.com#organization`.

### Services and extra properties

These details come from Settings > Locales > Business details, or from `business_profile` and `locale_profiles` over the API. A site that leaves them empty prints the same graph as before.

- **Catalogue name.** `catalog_name` becomes the `name` of the `hasOfferCatalog`. Empty prints `Services`.
- **Price range.** A service with `price_min` or `price_max` gets a `priceSpecification` of type `UnitPriceSpecification` with `minPrice`, `maxPrice`, the currency and, when set, `unitText` from `unit_text`. The single `price` still prints as before and may sit next to it.
- **Provider.** With `service_provider` on, every catalogue `Service` gets `provider` pointing at the Organization by `@id`. It is off by default, so existing sites print nothing new. A locale card can set it either way, with or without an office, and it applies to that locale's Organization and LocalBusiness catalogues. The same goes for the card's `catalog_name` and `additional_properties`.
- **Additional properties.** Each `additional_properties` row becomes one `PropertyValue` in `additionalProperty` with `name`, `value`, `minValue`, `maxValue` and `unitText`. A value that is a number prints as a number, anything else as text. They print on the Organization only.
- **Service addresses.** An address that starts with a single `/`, such as `/services/seo`, is relative to the site. On a page of a locale it opens under that locale's segment. On a page that belongs to no locale it opens under the default locale. A full `https://` address is printed as it is.

A `render.site_graph` plugin filter still runs after the core nodes are built, as before.

### The local business of a locale

The default locale's card is the Organization: its values are what the site graph prints, and the other locales' cards sit over them. On a site with several public locales, Settings > Locales > Business details lists one card per locale, the default locale first. A non-default locale's pages print a `LocalBusiness` node (or the subtype you choose) with `@id` `https://example.com#localbusiness-<segment>`, a `parentOrganization` reference to the Organization, and the card's address, phone, opening hours, area served and the other fields. A field left empty on the card uses the default locale's value, unless the card marks it as not inherited, in which case it prints nothing. The default locale prints a LocalBusiness of its own, with the `@id` `https://example.com#localbusiness-<segment>` for its segment, only when its office switch is on and it has a street or a city. A site with fewer than two public locales prints no LocalBusiness at all, so the details that belong to the place (service radius, menu address, reservations, public access, free entry, amenities and payment methods) print nothing there.

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

### Structured data settings

The **Structured data** card of an entity type (Settings > Entity types) holds the settings below. They are keys of the schema's settings (see [URL patterns](url-patterns.md#schema-settings)). A type that sets none of them prints the same JSON-LD as before.

| Setting | Effect |
|---|---|
| `modified_from` | The attribute that supplies `dateModified`, as an RFC 3339 time or a `YYYY-MM-DD` date. A missing, empty or unreadable value falls back to the entry's last update, and the result is never earlier than `datePublished`. |
| `author_from` | The attribute that holds a person's id or slug. When that person has a public page, `author` becomes `{"@type": "Person", "@id": "https://example.com/team/maria#person", "name": "Maria"}`. Without a public person the plain author name stays, and a person credited in the editor's byline always wins. |
| `term_set_url` | For `DefinedTerm`: the page of the set the terms belong to, such as `/glossary`. The node gets `inDefinedTermSet` with a `DefinedTermSet` of that address. A root-relative value is made absolute with the public base URL. |
| `emit_webpage` | `on`, `off` or empty. `off` leaves out the default `WebPage` node; `on` adds one next to the typed node. Empty keeps today's output. |
| `emit_breadcrumb` | `on`, `off` or empty. `off` leaves out the `BreadcrumbList` block. The breadcrumb trail on the page itself does not change. |

With `modified_from`, `author_from` or `term_set_url` set, a typed node of a creative-work type (`Article`, `BlogPosting`, `NewsArticle`, `DefinedTerm`, `WebPage`, `CreativeWork`, `HowTo` or `Course`) also refers to the site graph by `@id`: `publisher` points at `#organization` and `isPartOf` at `#website`, as article nodes already do. Other types, such as `Person` or `Organization`, never get these two properties, and the `emit_webpage` and `emit_breadcrumb` switches never add them.

An entry can override both switches on the **Schema** tab of its SEO panel. **WebPage** and **BreadcrumbList** each offer **Use schema default**, **On** and **Off**, and an entry value wins over the schema. They are stored as the entry attributes `jsonld_webpage` and `jsonld_breadcrumb` (`on` or `off`; the key is removed for **Use schema default**). The `Accept: application/ld+json` answer applies the same switches as the HTML page.

### FAQ from a list

An entry can hold a list attribute named `faq`, where each item has a `question` and an `answer`:

```json
{
  "faq": [
    { "question": "Do you ship to France?", "answer": "<p>Yes, within three days.</p>" },
    { "question": "Versenden Sie nach Deutschland?", "answer": "Ja." }
  ]
}
```

The list produces a `FAQPage` next to the entity node, after any items found through annotations or in the body. Markup in an answer is reduced to plain text, and an item without both parts is skipped. A page-builder component or a legacy section can carry the same `faq` list in its props or content; its items follow in document order. The schema's `faq_from_content` setting, when `false`, turns all FAQ extraction off.

## Social images

`og:image` and `twitter:image` addresses are percent-encoded like JavaScript's `encodeURI`: spaces and non-ASCII characters become `%XX`, reserved characters such as `?`, `&`, `=` and `#` stay, and an existing `%XX` is not encoded again. The `og:url` is not changed.

## Editorial provenance

If an entity has editorial metadata, the document is enriched: `reviewed_by` becomes an `editor`, each entry in `sources` becomes a `citation` item, and `last_verified_at` (or `reviewed_at`) becomes `lastReviewed`.

## URLs and the base URL

The entity's own `url` and `@id` are always taken from the page's canonical URL, which comes from the URL pattern, the parent chain and any canonical address set in the editor. Organization and company content types keep their `website` attribute as `url`. See [URL patterns](url-patterns.md).

The base is the public base URL, so set `PUBLIC_BASE_URL` before you rely on any of this. Without it, and with no address saved in the admin, Hatamoto takes the base from the request host, which on a proxied install can be an internal address. See [URL patterns](url-patterns.md#the-public-base-url).

## Extending it with plugins

Two plugin hooks change structured data. See [Hooks and events](../plugins/hooks-and-events.md) for how filters are declared.

- **`jsonld.enrich`** is a filter that receives the entity reference, the base URL and the finished document. A plugin changes the document, for example to add `aggregateRating`. It runs once per generation, on the success path only, after the settings above are applied (the FAQ list, the canonical address, `author_from` and the author credits, and the WebPage switch), so a plugin sees the final document, a `@graph` when the page has a FAQPage or a WebPage node, and its changes are the last word. It is not called when generation fails or the document was dropped by the WebPage switch.
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
