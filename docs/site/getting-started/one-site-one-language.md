---
title: One site, one language
nav_title: One site, one language
description: Why each Hatamoto install is one site in one language, what the language setting changes, and how page addresses work without a language in them.
---

An install of Hatamoto is one site in one language. This page explains what that means for your settings, your page addresses and plugins, and what to do if you need a second language. You choose the language in the setup wizard; see [First run](first-run.md).

## The design in short

Many CMSs grow a layer of translations: a table of languages, per-language copies of every setting, language prefixes in URLs, alternate-language tags in the page head and a region picker. Hatamoto 1.0 has none of that. The architecture decision behind the single binary (ADR 0002) states the rule plainly: one install is one site is one language. No code branches on a language, prefixes a URL with one, or stores per-language data.

What you get in return:

- One set of settings, one set of menus, one set of content.
- URLs that never carry a language or region segment.
- A smaller, faster server with fewer ways for pages to disagree.
- Backup and restore cover the whole site: one database is one site.

## The language setting

The site has one `language` setting. It holds a BCP 47 language tag such as `en`, `fr` or `pt-BR`. The default is `en`. You choose it in the setup wizard and can change it later under Settings.

The value is validated when you save it. Anything that is not a recognisable tag is rejected with "Choose a language". It is stored in its canonical form, so `pt-br` becomes `pt-BR`.

The setting feeds three things:

| Where | Effect |
|---|---|
| `<html lang>` | The language attribute on every public page. |
| JSON-LD | The `inLanguage` property in structured data. |
| Feeds | The language declared by the site's feeds. |

Because it only sets metadata, changing it does not translate or move any content. It tells browsers, search engines and screen readers what language your pages are written in.

### Related settings

Public contact details sit beside the language in the same site settings and appear in the Organization JSON-LD:

| Setting | Meaning |
|---|---|
| `phone` | A contact phone number. |
| `business_hours` | Opening hours as text. |
| `address` | A postal address. |
| `legacy_wp_redirects` | Switch on the built-in WordPress redirects. Default off. API only, not a switch in the admin. |

Owners can read and change these over the API with `GET` and `PATCH` on `/api/v1/settings/site`, as well as in the admin, except `legacy_wp_redirects`, which is API only.

## URLs have no language or region

Public URLs are built from content, never from a language:

| Content | URL |
|---|---|
| A page with slug `about` | `/about` |
| A post with slug `hello` | `/blog/hello` |
| The home page | `/` |

How an entity type maps to a URL is data held in its schema, not code. The default for pages is `/{slug}` and for posts `/blog/{slug}`. You can change a schema's pattern, for example to nest pages by their parent. See [URL patterns](../ai-seo/url-patterns.md) for the token syntax.

## Sites that once had region prefixes

If your database came from an earlier Hatamoto that used language or region path segments, the upgrade did the clean-up for you. Every old address of the form `/<prefix>/page` now answers with a permanent 301 redirect to its new path, so bookmarks and search results keep working. Those redirects are ordinary rows, so you can review them in the redirects screen; see [Media, menus and redirects](../admin/media-menus-redirects.md).

For a site imported from WordPress, turn on `legacy_wp_redirects` to enable the built-in rules for old WordPress URL shapes. It is off by default so a fresh install carries no surprise redirects.

## What if I need a second language?

You have three honest options:

1. **Run a second install.** Two sites, each with its own database and domain or subdomain, each in its own language. This is the clearest approach, and it keeps every URL free of prefixes.
2. **Model it as content.** Create a separate section or content type for the other language and link between the two. You manage the relationship yourself.
3. **Build it as a plugin.** Plugins attach to generic hooks such as `render.head` and `render.page`, so a plugin can add alternate-language tags or switching UI without changing the core. See [Plugins](../plugins/index.md).

What Hatamoto will not do is translate your content or serve one page under several language URLs by itself.

## Writing in other languages

The single-language rule is about site structure, not about what characters you can use. Titles, slugs and body text can be in any script. Search uses a language-neutral Postgres configuration without stemming or stop words, so it needs no per-language tuning. Set `language` to match what you write and everything that reads it will be correct.
