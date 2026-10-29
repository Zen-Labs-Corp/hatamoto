---
title: First-party plugins and other languages
nav_title: First-party plugins
description: The two plugins Hatamoto ships, the patterns worth copying from them, and how to write a plugin in a language other than Go.
---

This page describes the real first-party plugins and the rules for writing a plugin in a language other than Go. Read it after [Build the hello plugin](hello-plugin.md), when you want to see how a production plugin is put together.

The best documentation for a real plugin is a real plugin. Two integrations ship as signed first-party plugins instead of being built into the server. (Outgoing webhooks used to be a third; they are now part of core, see [Webhooks](../admin/webhooks.md).) Each lives in the `Zen-Labs-Corp/hatamoto-first-party` monorepo, its source of record (first-party plugins are not mirrored), and is the reference for what a production plugin looks like.

## The two first-party plugins

| Plugin | Source | Permissions | What it does |
|---|---|---|---|
| `gtm` | [Zen-Labs-Corp/hatamoto-first-party/plugins/gtm](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/gtm) | `settings` | Loads your Google Tag Manager container on every public page from a container ID in the plugin settings. |
| `searchconsole` | [Zen-Labs-Corp/hatamoto-first-party/plugins/searchconsole](https://github.com/Zen-Labs-Corp/hatamoto-first-party/tree/main/plugins/searchconsole) | `db`, `kv`, `http.fetch`, `settings`, `log` | Google Search Console: OAuth, site selection, sitemap submission, URL inspection and a daily performance import. |

Both are listed in the public plugins repository by a manifest with `"tier": "first-party"`, built from their pinned commit and signed with the first-party key into the default registry (`make registry`), which every server trusts. The private key is never in a repository. They build against the public SDK like any other plugin. The `hello` plugin is the reference for the basics, and the two above show real-world patterns. Two more examples cover the page builder: `examples/plugins/datasource-demo` answers the `builder.data` filter, and `examples/plugins/widgetpack-demo` is a widget pack with no code. `make plugins` builds `hello` and `datasource-demo`.

### gtm: the smallest real plugin

The gtm manifest asks for one permission, a settings schema and nothing else. A `render.head` filter reads the container ID with `settings.get`, validates it and adds a meta tag. It never fails a render. This is the real filter:

```go
func addContainerMeta(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		ContainerID string `json:"container_id"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	id := strings.ToUpper(strings.TrimSpace(s.ContainerID))
	if !idRe.MatchString(id) {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "gtm-container", "content": id},
	})
	return c, nil
}
```

`assets/plugin.js`, which the host links on every public page, reads the meta tag, validates the ID again and injects the Tag Manager script. The theme's base layout must include the plugin slots (the default theme does). The plugin has no `<noscript>` fallback and no consent mode, and under an enforced Content-Security-Policy (`CSP_ENFORCE=true`) the browser blocks the Tag Manager host until the policy allows it.

### searchconsole: OAuth, routes, cron and a UI

The Search Console plugin uses nearly every capability. It declares ten routes (one of them, `/oauth/callback`, public), an hourly `import` cron job, an admin page and a settings schema with write-only secrets. It keeps OAuth state and a PKCE verifier in `kv` with a TTL, encrypts the Google tokens in its own table and imports daily performance data in pages of 1,000 rows so each call stays under the host's response cap. Its allowed hosts are the four Google endpoints it needs.

## Patterns worth copying

- **Logic in plain Go, exports in one file.** Everything except `exports_wasip1.go` (the `//go:wasmexport` lines) builds and tests on the host machine. The host-function call is a package variable (`callHost`) that `host_wasip1.go` sets to `guest.Call` and tests replace.
- **One document per query.** `SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (...) t` returns a single cell the guest decodes into a struct.
- **A queue in a table, drained by cron.** Core's outgoing webhooks work this way too. Claim due rows one at a time atomically (`FOR UPDATE SKIP LOCKED` in a `WITH ... UPDATE ... RETURNING`), lease them for a minute, send within a time budget and count a claim whose call died as an attempt when its lease expires.
- **Secrets are write-only settings.** `"writeOnly": true` in `settings_schema` keeps a value out of the API and the form. The form shows that a value is stored and never fills it in. The plugin reads it with `settings.get`. An empty value keeps the stored one, and the form's clear box (or `null` in the JSON API) removes it.
- **OAuth through plugin routes.** An authenticated route starts the flow and a public route is the redirect target that trusts only the one-time state.
- **Fail soft.** A filter that cannot do its job returns the input unchanged, as gtm does, instead of returning an error.

## Writing a plugin in another language

Any language with an [Extism plugin development kit](https://extism.org/docs/concepts/pdk) can write a plugin from the contract alone. You need to do five things:

1. **Compile to a WASI reactor.** No `_start`, with `_initialize` run once per instance.
2. **Export functions with the right names.** Hook exports (`on_entity_published`, `filter_jsonld_enrich`), `http_handle`, `on_cron_<name>` and `func_<name>`. The rule is: replace `.` with `_` in the hook name and prefix `on_` for an action or `filter_` for a filter.
3. **Speak JSON.** Read the input as JSON from the Extism input buffer and write the result as JSON to the output buffer. Return `0` on success. On failure set the Extism error message and return non-zero. To refuse an `entity.before_save`, set the message `hatamoto:reject:<reason>`.
4. **Call the host through `hm_call`.** Import `hm_call` from the module `extism:host/user`. It takes one `i64` memory offset in (a JSON request `{"fn": ..., "args": ...}`) and returns one `i64` offset (a JSON response). Free the response. Use your PDK's host-function support to declare the import.
5. **Mind the limits.** The module must start within `limits.memory_pages` (a standard Go guest needs 64 pages), it must not spin, because the call is killed at the hook's timeout, and the module file may be at most 64 MiB. There is no filesystem, no network of your own and no WASI output unless the host process runs with `EXTISM_ENABLE_WASI_OUTPUT`.

Use the Go types in `backend/pkg/sdk` as the specification for every payload and host function, and read them next to [Hooks and events](hooks-and-events.md) and [The host API](host-api.md). Logs written with the PDK reach the host log tagged with the plugin name, each line cut at 4 KiB, with a burst of 20 lines and then 10 per second.

For the Go guest, the helpers you will use are `guest.OnAction`, `guest.OnFilter`, `guest.OnHTTP`, `guest.OnCron`, `guest.OnFunc`, `guest.Call`, `guest.Config`, `guest.Log`, `guest.Reject`, `guest.CapabilityDenied` and `guest.Handle` for unit tests without the runtime. Start with [Build the hello plugin](hello-plugin.md) and see [Plugin runtime](../architecture/plugin-runtime.md) for how the server loads and isolates modules.
