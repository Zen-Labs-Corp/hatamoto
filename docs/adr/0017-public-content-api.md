# ADR 0017: Public read-only content API

Status: accepted (2026-11-28)

Reverses: the [`docs/ROADMAP.md`](../ROADMAP.md) section 8 line "A headless or
public content JSON API (one renderer only)" and decision 2 "No headless mode in
1.0". This is the "future ADR" that [ADR 0004](0004-plugin-changesets.md)
section 11 anticipated.

## The rules in plain words

1. There are three routes under `/api/v1/public/`: `GET /content/{schema}`
   (a page of entries), `GET /content/{schema}/{slug}` (one entry) and
   `GET /terms/{kind}` (a page of terms).
2. The API is off by default. While it is off, every route answers 404 exactly
   like a path that does not exist. The owner turns it on in Settings >
   System > Integrations > Public content API.
3. Only published entries are served. Drafts, scheduled and archived entries,
   non-public people and other locales never appear. An entry that is hidden
   from the knowledge graph (`include_in_llms_txt` off, or noindex) is hidden
   here too, and so is every schema whose type is set to noindex.
4. Entries carry no `attributes` key in v1. No schema has a "public" flag yet,
   so nothing from the free-form attributes is exposed, except the excerpt.
   Email addresses, user ids, editorial data, SEO data and the body are never
   sent.
5. A list answers `{"data": [...], "meta": {...}}`; a single entry answers
   `{"data": {...}}`. Paging uses `page` and `per_page`, 20 by default and 100
   at most. A bad value is a 400.
6. Responses are cached for 60 seconds (`Cache-Control`) and carry a weak ETag.
   `If-None-Match` gets a 304.
7. Each client IP may send 10 requests per second, with a burst of 30. Over the
   limit the answer is 429 with `Retry-After`.
8. There is no CORS header, no API key, no write route, no GraphQL and no
   `/wp-json` compatibility layer.
9. The API is a second output of the same data, not a second renderer. The Go
   renderer stays the only public front end.

## Consequences

- Other tools can read published content with a plain HTTP call from a server.
  Browsers on other origins cannot, because there is no CORS.
- The one-renderer rule from ADR 0002 still holds: themes, caching and SEO
  output are unchanged, and nothing else renders pages.
- The opt-out follows the knowledge graph, so one setting controls both. The
  API filters in SQL, so `meta.total` is exact.
- A term used only by opted-out entries is still listed. This is accepted.
- Excerpts are returned as stored, which can be HTML. Clients must escape them.

## Not decided

- A per-schema allowlist that would let some attributes into the output.
- A `/wp-json` compatible surface.
