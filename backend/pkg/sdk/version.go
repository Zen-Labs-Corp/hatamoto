package sdk

// Version is the SDK semantic version. 0.5.0 replaced native Go plugins with
// sandboxed WASM modules; 0.6.0 adds capabilities (permissions, host
// functions, routes, cron, template funcs) and the form.submitted and
// render.page hooks; 0.7.0 renames the HeadContext and SitemapURLs
// per-site field to `language`; 0.8.0 makes sitemap.urls additions-only,
// turns the denied-capability error into
// {"code":"capability_denied","capability":"<name>"} and replaces
// limits.timeout_ms with per-hook limits.hook_timeouts_ms (see
// docs/PLUGIN_API.md); 0.9.0 slims the render.page payload (blocks by id,
// the host re-attaches content; `render_page.full_blocks` opts back in);
// 0.10.0 grows the entities host functions: `parent_id` on create and update,
// `parent_id`/`path`/`url` on the Entity, and entities.get by `path` or by
// `parent_id` + `slug`.
//
// 0.11.0 adds the builder.data filter, the builder.data permission, the
// builder_sources manifest field (plugin data sources for the page builder)
// and the builder.island permission (a widget pack that ships island.js).
//
// 0.12.0 adds the site.scripts permission: a plugin's browser JavaScript
// (assets/plugin.js) is only served and injected once the permission is granted.
//
// 0.13.0 adds the content `locale` field to the entity payloads (EntityRef,
// EntityEvent, EntitySummary, EntityDraft). Purely additive.
//
// 0.14.0 adds `attribution` (first and last touch) to FormSubmittedEvent.
// Purely additive.
//
// 0.15.0 adds locale support for plugins that manage a multi-locale site:
// `locale` and `segment` on HeadContext, `segment` and `file` on SitemapURLs,
// `translation_group` on the entity payloads, `translation_of` on
// entities.create, the entities.translations host function and locale-prefixed
// paths in entities.get. Additive for plugins built for 0.14 or older; a plugin
// declaring 0.15 or later must also pass `locale` to entities.create on a
// multi-locale site (older plugins keep the site language as the default).
const Version = "0.15.0"

// CompatibleWith lists earlier versions whose plugins this SDK still serves.
// Caret ranges before 1.0 stop at the next minor, so a purely additive minor
// lists its predecessors here. 0.4 plugins were native Go.
var CompatibleWith = []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0", "0.11.0", "0.12.0", "0.13.0", "0.14.0"}
