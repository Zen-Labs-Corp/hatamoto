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
// paths in entities.get (read only for plugins whose range requires 0.15 or
// later). Additive for plugins built for 0.14 or older; a plugin
// declaring 0.15 or later must also pass `locale` to entities.create on a
// multi-locale site (older plugins keep the site language as the default).
//
// 0.16.0 adds the render.site_graph filter (SiteGraph): a plugin adds keys to
// the Organization, LocalBusiness and WebSite nodes of the generic site graph
// and appends nodes. Purely additive.
//
// 0.17.0 moves content writes into runs and is a break for plugins that write
// content. The 12 direct write functions (entities.create, entities.update,
// entities.set_status, entities.delete, terms.save, terms.delete, menus.save,
// menus.add_item, menus.delete_item, media.upload, redirects.create,
// redirects.delete) and the five *.write permissions are removed: a manifest
// that requests one is refused at load, and a call to a removed function
// returns `unavailable`. A plugin now declares a `runs` block, exports
// on_run_step, collects data in a fetch phase (run.*, upload.read, buffer.put,
// media.import_url, http.fetch) and stages content in a transform phase
// (buffer.get, buffer.list, site.info, entities.slug_taken, changeset.*) into
// a changeset the owner reviews and publishes. Calls outside their phase fail
// with `phase_denied`; changing an object the owner or another source owns
// fails with `not_owned`. Plugins that never wrote content are unaffected and
// keep loading.
//
// 0.18.0 is additive over 0.17 and adds eight things first-party plugins need:
// `request_headers` and `csp` in plugin.json, declared cookies on public routes
// (HTTPRequest.Cookies, HTTPResponse.SetCookies), `translation_of` on
// changeset.entity.upsert, `caption` and `credit` on changeset.media.attach,
// the changeset.media.alt proposal, public content reads in the run fetch phase
// (entities.get, entities.list, entities.translations, media.get, media.list),
// and content types shipped as schemas/<name>.json in a plugin package.
//
// 0.19.0 moves the SDK to the JSON v2 packages and raises its Go floor to 1.27.
// `json.RawMessage` is an alias of `jsontext.Value` in Go 1.27, so the public
// types that now name `jsontext.Value` stay source compatible. The bytes
// exchanged with the host are unchanged, including those of plugin-author types
// the guest marshals (call arguments, results, run summaries): the guest pins
// the v1 behaviours (omitempty, Duration, byte arrays, method calls, repeated
// member names in raw JSON) and reads them back with the v1 behaviours
// (Duration, byte arrays, `,string`, case-only name folding, merging). The one difference left is that a struct with two
// fields sharing a JSON name fails to marshal.
const Version = "0.19.0"

// CompatibleWith lists earlier versions whose plugins this SDK still serves.
// Caret ranges before 1.0 stop at the next minor, so a purely additive minor
// lists its predecessors here. 0.4 plugins were native Go.
var CompatibleWith = []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0", "0.11.0", "0.12.0", "0.13.0", "0.14.0", "0.15.0", "0.16.0", "0.17.0", "0.18.0"}
