---
title: Data model
nav_title: Data model
description: Every Postgres table Hatamoto uses for content, people, menus, redirects, users, plugins, updates and analytics, with the rules each one follows.
---

This page lists the tables in the Hatamoto database and what each column and rule means. To look at your own database step by step, follow [Find where your data lives](where-your-data-lives.md).

## The idea: types and entities

Hatamoto does not have a separate table for pages, posts or products. Instead there are two generic tables:

- `hatamoto_entity_schemas` holds **content types** (the code and the admin call them schemas, the admin UI shows them as types). Each row has a `name`, a `slug`, a JSON Schema in the `schema` column and a `settings` JSON object for routing.
- `hatamoto_entities` holds **entities**, the actual pieces of content. Each row points at a type with `schema_id` and keeps its fields in a `jsonb` column named `attributes`.

A fresh install ships five built-in types (seeded by migrations, `is_builtin` true):

| Slug | Used for |
|---|---|
| `page` | pages |
| `post` | blog posts |
| `person` | people and bylines, see [People and authors](#people-and-authors) |
| `block` | reusable blocks of the visual editor, embedded in pages by reference; never public |
| `theme_part` | parts that fill a layout slot of a theme that opts in; never public |

You add your own types in the admin, for example a `service` or a `doc` type, and the JSON Schema decides which fields exist and how the editor shows them. The schema table also holds an optional `ui_schema` and a `description`. Built-in types cannot be deleted (`cannot delete built-in schema`). Only `person` may be changed, and only by adding fields and editing its routing settings; its name, its description and its core fields (`job_title`, `short_bio`, `bio`, `photo`, `public`, `public_email`, `website`, `linkedin`, `x`, `github`, `expertise`, `on_team`) cannot be removed or retyped (`cannot modify built-in schema`).

## Entities

The main columns of `hatamoto_entities` (from the baseline and later migrations):

| Column | Meaning |
|---|---|
| `id` | UUID primary key |
| `schema_id` | the content type |
| `name`, `slug` | display name and URL slug |
| `status` | `draft`, `scheduled`, `published` or `archived` |
| `attributes` | the content, validated against the type's JSON Schema |
| `metadata`, `editorial` | free-form SEO and review data |
| `version` | incremented on every save |
| `parent_id` | parent entity, for hierarchies such as docs trees |
| `published_at` | when the entity goes live; required for `scheduled` |
| `author_id`, `byline_person_id`, `byline_org` | the byline (see People below) |
| `variant_key` | marks a plugin-owned variant row sharing a slug |
| `slug_nested` | internal copy of "this type uses nested URLs", kept by triggers |
| `search_vector` | generated full-text index of the name, `excerpt` and `content`, used by the site search |
| `created_by`, `updated_by`, `created_at`, `updated_at` | audit columns |

The database check on `status` also tolerates a historical `redirected` value from the baseline; the application code only reads and writes the four statuses above.

**Slug uniqueness follows the URL pattern.** For most types a slug is unique within its type and locale (`uq_entities_schema_slug`, which includes the `locale` column). When a type's URL pattern contains `{path}` or `{parent}`, the parent chain is part of the URL, so a slug only has to be unique among siblings of the same locale (`uq_entities_schema_parent_slug`, same name, `locale` added). Both indexes ignore variant rows. This is why `/docs/seo/local` and `/docs/ads/local` can coexist.

**Status rules.** Only `published` entities are visible to readers. A `scheduled` entity needs a `published_at` date; the worker promotes it when the date passes (see [Worker and background jobs](worker-and-background.md)). The built-in public repository filters on `status = 'published'`, so unfinished work is invisible without any extra guard.

### Page content

Pages built with the visual editor store a document tree in `attributes.page_doc`. The older flat `page_sections` list is a frozen legacy key: the server never rewrites it, converts a page that only has it into a `page_doc` on its first write, and derives section-shaped data from `page_doc` for the public JSON and plugins. Every save passes through the same pipeline, in `entity.Service.CreateEntity` and `UpdateEntity`:

1. validate `attributes` against the type's JSON Schema,
2. sanitise rich text and HTML sections with `bluemonday` (see [Auth and security](auth-and-security.md)),
3. apply the page write rules to `page_doc` (validate it, convert a page that only has `page_sections`, and refuse a changed `page_sections` on a page that has a document with 422 `page_sections_removed`),
4. generate or normalise the slug,
5. run `entity.before_save` plugin filters (a plugin can only reject with an explicit rejection; other plugin errors are logged and ignored),
6. check parent rules (no cycles, bounded nesting depth),
7. write the row, then emit events such as `entity.created` and invalidate cached pages.

## Versions

`hatamoto_entity_versions` keeps a history of `attributes` per entity with the editor, a change summary and a timestamp. Restoring a version validates it against the type's current schema first, and fails if it no longer fits. A background job coalesces identical consecutive versions and keeps the newest 20 per entity. Background autosaves from the blog editor share one version within a five minute window and are pruned before explicit versions.

## Terms

`hatamoto_terms` stores tags, categories and custom kinds. `kind` starts with a lower-case letter, continues with lower-case letters, digits, `_` or `-`, and is at most 16 characters long, so `tag` and `category` are only the defaults and a site can add a `genre` kind. Kinds that would clash with a built-in address are refused: `author`, `search`, `api`, `admin`, `p`, `feed`, `health`, `ready`, `static`, `media`, `mcp`, `sitemap`, `robots`, `llms`, `preview`, `page`, `theme`, `forms` and anything starting with `auth`. Terms can be hierarchical through `parent_id`, at most 8 levels deep, and carry a `name` (up to 128 characters) and a `description`. `hatamoto_entity_terms` joins entities and terms. A public archive for each term is served at `/<kind>/<term>` (for example `/tag/news`), where a nested term's address is its ancestors' slugs and its own joined by `/`.

## People and authors

Bylines point at **people**, not at user accounts. A person is an entity of the built-in `person` type, which carries job title, short bio, photo, social links and a `public` flag. `hatamoto_user_people` links an admin user to at most one person, and a person belongs to at most one user. Posts reference a person through `byline_person_id`, or credit the organisation with `byline_org`. The older `hatamoto_authors` table still exists: `author_id` is dual-written from the person so that rolling back to the previous release still renders bylines.

## Navigation, media and forms

| Table | Contents |
|---|---|
| `hatamoto_menus`, `hatamoto_menu_items` | named menus with an optional `location`; items link to an entity, an external URL or a content type, can be nested and ordered |
| `hatamoto_media` | uploaded file metadata: filename, MIME type, size, dimensions, `alt_text`, `caption`, folder and generated `variants` (the files live on local disk under `MEDIA_UPLOAD_DIR`, default `./uploads`) |
| `hatamoto_webhooks`, `hatamoto_webhook_deliveries` | outgoing webhooks (signing secrets stored unencrypted) and their delivery queue and log, see [Webhooks](../admin/webhooks.md) |
| `hatamoto_forms`, `hatamoto_form_submissions` | form definitions and submissions, with CSV export in the admin |
| `hatamoto_spam_settings` | one row of site-wide spam protection settings; the captcha secret is stored sealed with AES-256-GCM; no row means the built-in defaults |
| `hatamoto_section_templates` | reusable page sections saved by editors |
| `hatamoto_entity_link_index`, `hatamoto_entity_link_audit`, `hatamoto_internal_link_rules` | the internal linking index and its audit trail |

## Site configuration and redirects

`hatamoto_site_config` is a single-row table (the `id` column is a boolean constrained to true). It holds company name, logo, tagline, timezone, contact details (postal address, phone, business hours, notification email), the homepage entity, the site-wide `language`, `public_base_url`, the time the setup wizard finished, the `legacy_wp_redirects` switch, and the page cache and preload settings.

`hatamoto_smtp_settings` is a single-row table of the email settings saved in Settings > Email: provider, SES region, host, port, security, username, sender and the password, sealed with the server secret (AES-256-GCM). No row means the `SMTP_*` environment applies; see [Set up email](../operate/email.md).

`hatamoto_redirects` holds `source_path`, `target_url`, `status_code` (301, 302, 307 or 308, enforced by a check), a hit counter, an active flag and a `source_tag` that records where a rule came from. Wildcard sources are supported.

`hatamoto_404_log` has one row per missing address: path, hit count, last referrer and user agent, first and last seen, an `ignored` flag and the redirect that fixed it, if any. A worker job deletes rows 90 days after they were last seen.

`hatamoto_migration_notes` records, for the owner, what a migration moved, archived or switched off (for example when the site moved to one language).

## Users, sessions and keys

| Table | Contents |
|---|---|
| `hatamoto_users` | accounts with an argon2id password hash, a role (`owner` or `editor`) and login-lockout state |
| `hatamoto_sessions` | admin sessions, stored as a SHA-256 hash of the cookie value, never the cookie itself |
| `hatamoto_user_tokens` | one-time password reset and invitation tokens, stored as hashes |
| `hatamoto_api_keys` | API keys (hash and prefix only), scopes, per-key rate limit (default 1000 requests per hour), expiry, last use and revocation |
| `hatamoto_admin_audit` | an audit log of admin actions, pruned after one year |
| `hatamoto_mcp_audit` | an audit log of MCP tool calls, pruned after 90 days |

## Plugins, themes and imports

| Table | Contents |
|---|---|
| `hatamoto_packages`, `hatamoto_package_config` | installed `.hmp` packages (plugins and themes) stored as bytes with their digest and signature, and the owner's install policy and active theme |
| `hatamoto_theme_settings` | values of a theme's settings, kept across theme updates |
| `hatamoto_plugin_settings`, `hatamoto_plugin_grants`, `hatamoto_plugin_kv` | plugin settings, granted permissions and the key-value store |
| `hatamoto_plugin_credentials`, `hatamoto_instance` | per-plugin database role passwords and this database's identity |
| `hatamoto_import_runs`, `hatamoto_import_map` | import runs and the source-id to Hatamoto-id map that makes re-runs idempotent |
| `hatamoto_import_secrets` | the source credential of an import run that can still be resumed, sealed with the server secret (AES-256-GCM) and deleted when the run completes |
| `hatamoto_plugin_handover` | webhook subscriptions parked for a plugin during an earlier release; read only by the host and kept until a later release drops it |
| `hatamoto_updates`, `hatamoto_update_settings` | the self-update state, see [Updates](../updates/index.md) |

A plugin's own tables are not here: each plugin gets a separate Postgres schema named `plugin_<name>` (see [Plugin runtime](plugin-runtime.md)).

## Analytics

Hatamoto keeps no visitor profiles. AI-crawler data lives in these tables:

| Table | Contents |
|---|---|
| `hatamoto_agent_hits` | one row per crawler fetch, partitioned by month (`hatamoto_agent_hits_YYYY_MM`, plus `hatamoto_agent_hits_default`) |
| `hatamoto_agent_daily` | daily rollups of crawler hits |
| `hatamoto_agent_first_crawl` | the first fetch of each entity by any AI crawler: which crawler, when, and the entity's publication time |
| `hatamoto_agent_tracking` | one row: when first-crawl tracking began, so latency is reported only for content published after it |
| `hatamoto_agent_robots_policy` | the owner's `robots.txt` choices, one row per crawler purpose that is denied |
| `hatamoto_agent_ip_ranges` | the published IP ranges used to verify crawler hits |

Pageviews and AI referrals are rolled up per day in `hatamoto_analytics_daily`. Client IPs in raw agent hits are truncated after 30 days by a worker job (to a /24 for IPv4, a /48 for IPv6).

## Looking at the schema

To see exactly what your database contains, connect with `psql` and run `\dt`, or list applied migrations with:

```bash
hatamoto migrate status
```

The authoritative definition of every table is the sequence of files in `backend/migrations/`; see [Database migrations](database-migrations.md) for how they are applied.
