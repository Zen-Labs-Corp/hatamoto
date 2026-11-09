# Hatamoto documentation

Start with the guides: they are published at [hatamoto.dev/docs](https://hatamoto.dev/docs), and their
markdown sources are in [`site/`](site/). The other files in this folder are references for people who
write plugins and themes, run a server, or work on Hatamoto itself. Each one goes deeper than the guides
on a single subject.

## Guides (`site/`)

| Section | For |
| --- | --- |
| [Getting started](site/getting-started/index.md) | installing Hatamoto and the first run |
| [Using the admin](site/admin/index.md) | editors and site owners: pages, the builder, posts, forms, menus, settings |
| [AI and SEO](site/ai-seo/index.md) | AI crawlers, analytics, `llms.txt`, structured data, MCP |
| [Plugins](site/plugins/index.md) | writing, packaging and signing a plugin |
| [Themes](site/themes/index.md) | building and changing a theme |
| [Operating it](site/operate/index.md) | deploying, configuring, backing up and hardening a server |
| [How updates work](site/updates/index.md) | release channels, signing, automatic updates and rollback |
| [Architecture](site/architecture/index.md) | how the one binary is put together |
| [Migrate and contribute](site/migrate-contribute/index.md) | moving from WordPress, and working on Hatamoto |

How the guides are written and published: [`site/README.md`](site/README.md), [`site/STYLE.md`](site/STYLE.md).

## Plugin and theme authors

| Document | What it covers |
| --- | --- |
| [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md) | write a plugin in your own repo and get it into the registry |
| [PLUGIN_API.md](PLUGIN_API.md) | the plugin SDK: manifest, hooks, host functions, permissions |
| [THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md) | build a theme in your own repo and get it listed |
| [THEMES.md](THEMES.md) | the theme contract: layouts, partials, template functions, sections |
| [WIDGET_PACKS.md](WIDGET_PACKS.md) | builder widgets and data sources supplied by a package |
| [PACKAGES.md](PACKAGES.md) | `.hmp` packages, signing and the registry |
| [MCP.md](MCP.md) | the MCP server: tools, scopes, API keys |
| [WEBHOOKS.md](WEBHOOKS.md) | outgoing webhooks and their payloads |
| [CLI.md](CLI.md) | every `hatamoto` command |

Compatibility promises for all of these: [STABILITY.md](../STABILITY.md).

## Running a server

| Document | What it covers |
| --- | --- |
| [UPGRADES.md](UPGRADES.md) | how an upgrade stays reversible |
| [BACKUPS.md](BACKUPS.md) | backups from the admin and the command line |
| [EXPORT.md](EXPORT.md) | site export and import |
| [IMPORT-WORDPRESS.md](IMPORT-WORDPRESS.md) | the WordPress importer in detail |
| [REDIRECTS.md](REDIRECTS.md) | redirect rules and legacy addresses |
| [URL_PATTERNS.md](URL_PATTERNS.md) | where content lives on the public site (schema data, not code) |
| [LOCALE_CONVERSION.md](LOCALE_CONVERSION.md) | turning region page trees into locales |
| [SPAM_PROTECTION.md](SPAM_PROTECTION.md) | the spam pipeline for public forms |
| [SECURITY_MODEL.md](SECURITY_MODEL.md) | what Hatamoto protects, from whom, and the tests that prove it |
| [DEPENDENCIES.md](DEPENDENCIES.md) | every third-party module in the binary |
| [deploy/](deploy/) | deployment notes: Caddy, demo mode, plugin database roles |

## Working on Hatamoto

| Document | What it covers |
| --- | --- |
| [ROADMAP.md](ROADMAP.md) | where the project is going |
| [ADMIN.md](ADMIN.md) | how the server-rendered admin is built |
| [ADMIN_UI_RULES.md](ADMIN_UI_RULES.md) | the admin design rules, enforced by `make design-check` |
| [BUILDER.md](BUILDER.md) | the page builder's `page_doc` model |
| [CANVAS.md](CANVAS.md) | the builder canvas protocols and node rendering |
| [ENTITY_IMPORT.md](ENTITY_IMPORT.md) | the one bulk path into content |
| [agent-analytics.md](agent-analytics.md) | AI crawler capture, verification and referrals |
| [CHANGELOG_SITE.md](CHANGELOG_SITE.md), [CHANGELOG_RELEASE_HOOKS.md](CHANGELOG_RELEASE_HOOKS.md) | the `/changelog` page and how releases feed it |
| [RELEASE_NOTES.md](RELEASE_NOTES.md) | notes for changes that need more than a changelog line |
| [QA.md](QA.md), [qa/](qa/) | manual QA checklists for what the automated tests cannot see |
| [adr/](adr/) | architecture decision records |
| [ADMIN_STATIC_EXPORT.md](ADMIN_STATIC_EXPORT.md) | superseded; kept for the record |

Contributing, the local gates and the commit rules: [CONTRIBUTING.md](../CONTRIBUTING.md). The scripts behind
the gates, releases and screenshots: [`scripts/`](../scripts/README.md).
