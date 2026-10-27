# ADR 0002: Single binary

Status: accepted (2026-10-14)

Supersedes: [ADR 0001](0001-plugin-sdk.md) (native compiled-in plugin runtime),
roadmap items 07 (go-rendered-theme), 10 (wasm-plugin-tier) and 12 (sdk-followups)
of the earlier internal roadmap, and [`docs/ADMIN_STATIC_EXPORT.md`](../ADMIN_STATIC_EXPORT.md).

Source: [`docs/ROADMAP.md`](../ROADMAP.md) sections 2 to 4. The roadmap is the plan;
this ADR records the architecture decision it rests on.

## Context

Hatamoto today is two processes and two toolchains: a Go API (chi, JWT, Redis,
native Go plugins compiled in with `hatamoto build`) and a Next.js 14 app that
renders the admin and, in hybrid mode, part of the public site. Installing it
needs Node, a Go toolchain to add a plugin, and several services. Plugins are
trusted code with full database access. Public rendering is split between
Next.js and Go templates, and the admin cannot be exported statically without
restructuring (`docs/ADMIN_STATIC_EXPORT.md`).

The 1.0 goal (ROADMAP section 1) is a stranger with one file and Postgres
having a working site in under ten minutes, with sandboxed plugins that need
no compiling. Two runtimes, compiled-in plugins and a Node build cannot meet it.

## Decision

Hatamoto 1.0 is **one Go binary plus Postgres 16**. The public site and the
admin are both server-rendered by Go (`html/template`, htmx, vendored JS);
Next.js, Node and npm are removed. Plugins run in an Extism (wazero) WASM
sandbox with explicit permissions and their own Postgres schema and role;
themes and plugins are packages stored in Postgres. One install is one site
in one language.

The architecture, the stack and the invariants below are copied from
ROADMAP sections 2 to 4 and are binding for every 1.0 slug. Accepting this
ADR also confirms the decisions listed in ROADMAP section 9 (Apache-2.0, no
headless mode, the `legacy-hybrid` security-fix branch, owner and editor roles,
`/blog/{slug}` post URLs, the autosave rule, public site before admin).

### Architecture

```
hatamoto (one Go binary)                               Postgres 16
 ├─ public site      html/template themes               ├─ core tables
 ├─ admin            html/template + htmx + Tiptap      ├─ plugin_<name> schema + role per plugin
 ├─ API              API keys (admin fallbacks, CLI)    ├─ plugin and theme packages
 ├─ MCP server       /mcp                               └─ sessions, analytics, content
 ├─ worker           in-process scheduler
 ├─ migrations       embedded, run on boot              media: local disk
 └─ plugins          Extism (wazero) WASM sandbox
```

One install = one site = one language.

### Stack at a glance

| Layer | Hatamoto 1.0 | Status | Replaces |
|---|---|---|---|
| Runtime | one `hatamoto` binary + Postgres 16 | keep | Next server, Node, Redis |
| Language | Go 1.27 (`go 1.27`, `toolchain go1.27.2`) | upgrade | Go 1.24, TypeScript, React |
| Build | `go build` only (vendored JS committed; `make vendor` only to update it) | new | npm, Next build, Tailwind |
| HTTP routing | `net/http.ServeMux` + `internal/httpx` groups (~40 lines) | switch | chi, chi/cors |
| Changing routes | fixed catch-alls: content `/{path...}` via the URL-pattern registry; plugins `/p/{plugin}/...` and `/api/v1/p/{plugin}/...` via per-plugin routers swapped atomically | new | Next middleware, chi plugin mounting |
| Database driver | `pgx/v5` | keep | — |
| Migrations | `goose/v3` as a library, embedded, Postgres lock, core + plugins | keep | goose CLI, `entrypoint.sh` |
| HTML sanitising | `bluemonday` (two policies: rich text, html section), save + render + preview | keep | DOMPurify |
| JSON Schema | `santhosh-tekuri/jsonschema` | keep | zod, react-hook-form |
| IDs | `google/uuid` | keep | — |
| Go extended packages | `golang.org/x/{crypto,image,net,text,time}` | keep | — |
| Public rendering | `html/template` themes with a template hierarchy | keep + extend | Next public site |
| Public JS | `hm.js` (~70 lines) | keep | React hydration |
| Admin rendering | `html/template`, `go:embed` | new | Next admin |
| Admin interactions | **htmx 2** | add | React state and fetch code |
| Admin widgets | native `<dialog>`, `popover`, `<details>` | new | component libraries |
| Rich text | **Tiptap 3** (vendored ESM bundle) | keep | its React wrapper |
| Code / HTML / CSS fields | monospace `<textarea>` | keep | — |
| Drag and drop | **SortableJS** | add | `@dnd-kit` |
| Charts | **Chart.js 4** (retired 2026-10: own SVG modules) | add | React charts |
| Admin CSS | plain CSS: existing tokens + one stylesheet per admin partial, bundled by Go | switch | Tailwind |
| Theme CSS | whatever the theme ships (plain CSS bundled by Go; authors may compile Tailwind/Sass locally) | keep | — |
| JS delivery | `web/vendor/` pinned + sha256, import map, served from the binary | new | npm, bundler |
| Canvas editor | embedded `canvas_bridge.js` + vanilla JS host | port | React canvas host |
| Plugins | **Extism** (wazero) WASM; permissions; own Postgres schema + role per plugin | new | compiled Go plugins, `hatamoto build`, Next plugin layer |
| Plugin packages | `.hmp` zip, sha256 + ed25519, stored in Postgres, registry | new | blank imports |
| Themes | packages in Postgres; install, preview, activate, settings in the admin | new | theme-directory env vars |
| Admin auth | database sessions + CSRF | keep | JWT, Google sign-in |
| API / MCP / CLI auth | API keys with scopes | keep | JWT bearer |
| Roles | owner, editor | simplify | viewer, client, author |
| Language | one site setting `language` (for `<html lang>`, JSON-LD `inLanguage`, feeds) | **simplify** | locales table, locale overrides, locale URL tokens, hreflang, region picker, GeoIP, per-locale settings |
| Gated content | none | **remove** | gate partial, teasers, lead JWT, remember tokens |
| AI agent access | official MCP Go SDK at `/mcp` + `hatamoto` CLI | add | — |
| AEO features | AI-crawler hits (in-process), first crawls, AI referrals, AI robots policy, `llms.txt`, JSON-LD, knowledge graph, internal linking v2 | keep | Next crawler pings, linking v1, facts |
| Analytics | pageviews and AI referrals, rolled up daily; no visitor profiles | simplify | visitors, events firehose, intent signals |
| Caching | in-process HTML cache | keep | Redis |
| Observability | `slog`, `/health`, `/ready` | keep | custom OTLP tracer |
| Email | SMTP (optional; CLI fallback for password reset) | keep | SES webhook |
| Forms | forms, submissions, CSV export | keep | lead CRM |
| Integrations | Search Console as a first-party registry plugin; webhooks built in (reversed 2026-10, see `docs/WEBHOOKS.md`) | move | in-core packages |
| WordPress import | REST + WXR importer with a URL audit | add | — |
| Backup / portability | `hatamoto export` / `import` full-site archive (JSON + media) | add | — |
| Tests | stdlib `testing`, `httptest`, golden files, Postgres integration tests; never compiled into the binary | keep | testify, vitest, chromedp |
| CI | one GitHub Actions workflow: vet, test, govulncheck, vendor checksums, release on tag | add | — |
| Packaging | release binaries (linux/darwin, amd64/arm64), one Docker image, compose (db + hatamoto) | new | two images, `hatamoto-web` |
| Licence | Apache-2.0 | add | none |

**Totals.** Go modules: pgx, goose, bluemonday, jsonschema, uuid, extism (wazero), MCP SDK, `golang.org/x/*`.
Vendored JS: htmx, Tiptap, SortableJS (Chart.js retired 2026-10: the analytics charts are drawn by the admin's own SVG modules). Build tool: `go`. Runtime: one binary + Postgres.

**On tests.** `_test.go` files and test-only modules are never compiled into the production binary,
so tests cost nothing at runtime. They stay in the repo because contributors need them and CI uses
them to vet pull requests. Browser tests (chromedp) are dropped: the admin is server-rendered, so
`httptest` + golden HTML covers it; Tiptap and the canvas host get a manual QA checklist in
`docs/QA.md` run before each release.

### Invariants every slug must keep

1. **One way to do each thing.** A slug that replaces something deletes the old version in the same PR. No feature flags keeping old paths alive.
2. **Import before writing.** Reviewers reject hand-written code that a maintained library already provides; hand-written code is for Hatamoto-specific logic only. Every dependency is pinned and listed in `docs/DEPENDENCIES.md`.
3. **Lowest dependencies.** No new dependency unless it removes more code than it adds and nothing in the stack table already covers it.
4. **No Node** anywhere in the repo after `v1-15`.
5. **Content is untrusted.** `bluemonday` policies are the single source of truth for stored HTML; the browser editor never is. Sanitiser fixtures (`rich-html-samples.json`, `style-cases.json`, `html-style-corpus.json`) stay as Go test data when `frontend/` is deleted.
6. **Admin read paths work without JS**; JS only enhances editing.
7. **No site-specific or fork-specific code.** `scripts/check-neutral.sh` (added in `v1-03`, run by `make check`) fails on the names of the agency site core grew out of (and its private repos and hosts), or on any hard-coded locale or country code used as a path or locale literal; the banned list lives in the script.
8. **No locales.** No code may branch on a locale, prefix a URL with one, or store per-locale data after `v1-09`.
9. **hatamoto.dev ships every slug** through `scripts/ship.sh`; it is the first production user.

## Consequences

- One artefact to build, ship and run: `go build` only, no Node anywhere in
  the repo after `v1-15`; hatamoto.dev runs one process.
- Plugins no longer need a custom binary and cannot reach core tables or the
  host; the native plugin runtime from ADR 0001 (`hatamoto build`,
  blank imports, `backend/plugins/`) is deleted in `v1-16`. `pkg/sdk` survives
  as the plugin contract (DTOs, hook names, manifest) without pgx or router types.
- The admin is rebuilt in Go (`v1-10` to `v1-14`); the static-export route for
  the Next admin is abandoned.
- Locales, gated content, lead CRM, Redis, OpenTelemetry, JWT, CORS and Google
  sign-in leave core. Sites that need them (for example regions or gating)
  build them as plugins on generic hooks (`render.head`, `render.page`,
  `form.submitted`) or as content structure.
- Each slug that replaces something deletes the old version in the same PR
  (invariant 1), so `main` never carries a dual path; the pre-1.0 hybrid stack
  lives only on the `legacy-hybrid` security-fix branch (ROADMAP section 5).
