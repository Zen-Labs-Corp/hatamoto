# ADR 0003: Public locales

Status: accepted (2026-10-29)

Reverses: [`docs/ROADMAP.md`](../ROADMAP.md) section 4 invariant 8 ("No locales")
and the section 8 line "Locales, translations, multi-language or multi-region
sites" (GeoIP stays out, see below).

Amends: [ADR 0002](0002-single-binary.md) ("One install is one site in one
language").

## Context

ADR 0002 and the roadmap settled that one install is one site in one language.
The content-locale work then added an `entities.locale` column (a BCP 47 tag,
slug unique per schema and locale) but kept every non-site-language row out of
the public site.

Sites that serve regional variants (`en-AU`, `en-US`) or real translations
(`en`, `fr`) from one install have had to build this outside core: a plugin that
links copies, a theme that prints hreflang from a stored attribute, and a proxy
that redirects the bare root. That is the same feature written twice, once per
site, which the "one way to do each thing" invariant argues against. Core
should own it, as long as an install with one language does not notice.

## Decision

A Hatamoto install can serve several public locales. The change ships additive
in a 1.x minor, in phases; this ADR covers the design all phases share.

- **Locale list in site settings.** An ordered list of `(BCP 47 tag, URL segment)`
  pairs, a default locale (the existing site `language`), a "prefix every locale"
  toggle and an x-default locale. The segment is free per locale (a tag
  `fr-CA` may use `fr`). The list order is the hreflang order. An empty list is a
  single-locale install, and every new code path short-circuits on it.
- **The prefix is not a URL pattern token.** Patterns stay as they are. A
  validated locale set strips the segment after the redirect check and before
  routing, and `Locate` and `URLFor` add it last. The default locale is
  unprefixed unless "prefix every locale" is on. With that toggle on, the bare
  root answers 302 to the default locale's home.
- **Shared content.** Content that belongs to no locale is stored with the
  reserved locale `und` (the column stays NOT NULL, so a previous binary keeps
  reading and writing rows). It has one unprefixed URL, is visible from every
  locale and has no hreflang. `NormalizeLocale` still rejects `und`, so it cannot
  be set by accident.
- **Translation groups.** A nullable `translation_group` column on entities
  links one copy per locale. Membership is stored in the column, never as an
  attribute, and hreflang alternates are computed, never stored. Each copy keeps
  its own slug.
- **One visibility filter.** The public filter takes the visible locales as a
  query argument (the request's locale plus shared). With no locale in the
  request it behaves as before: exact site language.
- **hreflang and x-default.** The default `head` partial prints one alternate per
  live sibling in settings order plus x-default (the setting, else the first live
  sibling). A locale without a copy is not printed as hreflang; a theme
  language switcher links it to that locale's home. `<html lang>`, JSON-LD
  `inLanguage` and plugin payloads use the page's locale.
- **Sitemaps.** The index lists one set of files per locale, and each URL with
  two or more live copies carries `xhtml:link` alternates.
- **Language-switch trigger.** The old behaviour of moving every entity to the new
  language when the site language changes stays only while the locale list is
  empty. With a list, nothing is retagged.
- **Neutrality stays.** `scripts/check-neutral.sh` keeps banning hard-coded
  country and locale codes in shipped files. A locale list is data, not code.
- **Not in core:** GeoIP, cookie or Accept-Language negotiation, machine
  translation and per-locale identity settings (site name, logo, tagline). GeoIP
  stays in the reverse proxy.

Phase 1 delivers the settings, routing, translation groups, hreflang and
per-locale sitemaps. Field inheritance between copies, the editor's
Translations panel, menus and theme parts, per-locale search, feeds and
llms.txt, and WordPress multilingual import follow in later phases.

Per-locale contact settings shipped afterwards as business details: Settings >
General holds one site-wide set and one override card per locale, and each
locale's pages print their own `LocalBusiness` node linked to the shared
`Organization`. Phone, the hours note and the address kept their columns, so
older releases still read them. Identity (site name, logo, tagline) is still not
per locale.

## Consequences

- ROADMAP invariant 8 is replaced and the section 8 line removed. The roadmap
  and [`docs/URL_PATTERNS.md`](../URL_PATTERNS.md) describe the new behaviour.
- An install with no locale list is unchanged: same URLs, HTML, sitemaps, feeds
  and llms.txt.
- Migrations are forward-only and N-1 safe: new columns have defaults and the
  previous binary ignores them. Rows in other locales or in `und` are hidden from
  a previous binary after a rollback, they do not error.
- Themes: `.Site.Language` is per page. Custom themes must range over the
  alternates in their `head` partial to print hreflang (see
  [`docs/THEMES.md`](../THEMES.md)).
- Plugins: no SDK version change in phase 1. Existing fields carry the page's
  locale (see [`docs/PLUGIN_API.md`](../PLUGIN_API.md)).
- Unprefixed URLs of the default locale and of shared content share one
  namespace. When both have the same slug the default locale's page wins. A
  write-time collision check is left to a later phase.
- A locale segment must not shadow a reserved path, a section prefix, a
  published root slug or an active redirect source. Saving the settings rejects
  it; redirects are never deleted automatically.
