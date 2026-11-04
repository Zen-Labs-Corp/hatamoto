# Release notes

## Unreleased: Cookie consent banner and Consent Mode v2

Settings > Privacy adds an optional cookie banner. Visitors can accept all, reject all or choose Analytics, Marketing and Functional, and the choice is announced to your scripts through `window.hm.consent`, the `hm:consent` event and Google Consent Mode v2. A choice is remembered for 12 months; the page shows daily counts of the choices without storing any visitor data.

- **Off by default.** Until you turn it on, public pages are byte-identical to before.
- **EU and UK mode.** Shows the banner by the country header of your CDN (`CF-IPCountry` or `CloudFront-Viewer-Country`); the page HTML and its cache are the same for every country.
- **Public API.** `GET /api/v1/public/consent/region` (never cached) and `POST /api/v1/public/consent` (daily counter, limited per IP).
- **Migration.** Two new tables (expand-only). No SDK, hook or plugin manifest change.

**Rollback floor: unchanged.** The previous release runs against the migrated data and ignores the new tables.

## Unreleased: Plugin SDK 0.18.0 adds eight things first-party plugins need

SDK 0.18.0 is an additive minor over 0.17: plugins built for earlier ranges keep loading.

- **Request headers.** `request_headers` in `plugin.json` lists extra request headers (a webhook signature, say) passed to the plugin's routes. `Authorization`, `Cookie` and the visitor's address are never passed. The plugin page lists them.
