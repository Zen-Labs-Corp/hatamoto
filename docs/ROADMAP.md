# Hatamoto roadmap

Status: **pre-1.0.** Releases are published on three channels (`stable`, `edge`, `canary`; see
[How updates work](site/updates/index.md)) and the plugin SDK is at v0: it can still change between
minor versions until it is frozen at 1.0 (see [Not in 1.0](#8-not-in-10-decided) for what is out of scope).
This page says what Hatamoto 1.0 is, how it is built and what is next. Sections 1 to 4 describe the product and its invariants; the rest
is the design record of the 1.0 build: it stays so contributors can see why things are the way they are.

**Next, toward 1.0:** public source and release bundles on GitHub, a signed multi-arch Docker image
with each release, the public plugin registry at <https://hatamoto.dev/plugins>, a public demo, and
freezing the SDK as v1.0.0. Larger ideas that are not on the list are in section 8.

## 1. The promise: open source, ready on day 1

A stranger downloads one file, points it at Postgres and, in under ten minutes, has:

- a fast, server-rendered website with a good default theme, a blog and pages;
- an admin with a proper **blog editor** and a visual **page editor**;
- their WordPress site imported, with every old URL still working;
- AI-crawler analytics (which AI read which page, and did it send visitors), `llms.txt`, JSON-LD and a knowledge graph out of the box;
- one-click, sandboxed plugins and themes, with no compiling;
- an MCP server so AI agents can write and publish through the CMS;
- an Apache-2.0 licence, docs, signed release binaries and a Docker image (from the first public release).

## 2. Architecture

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

One install = one site. It has one default language and can serve more public locales (see [ADR 0003](adr/0003-public-locales.md)).

## 3. Stack at a glance

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
| Rich text | **Tiptap 3**: core, pm, starter-kit (includes Link), image, table, extensions (Placeholder), suggestion (slash menu), bubble-menu; vendored as an esm.sh module tree with shared core/pm externals (~90 files) | keep | its React wrapper |
| Code / HTML / CSS fields | monospace `<textarea>` | keep | — |
| Drag and drop | **SortableJS** | add | `@dnd-kit` |
| Charts | **Chart.js 4** (retired 2026-10: own SVG modules) | add | React charts |
| Admin CSS | plain CSS: existing tokens + one stylesheet per admin partial, bundled by Go | switch | Tailwind |
| Theme CSS | whatever the theme ships (plain CSS bundled by Go; authors may compile Tailwind/Sass locally) | keep | — |
| JS delivery | `backend/tools/vendor` (Go tool, `make vendor`) downloads the esm.sh module tree pinned to `?target=es2022`, rewrites imports to local paths, writes `backend/web/vendor/vendor.json` (sha256 per file); import map with `integrity` hashes and per-page `modulepreload`; served fingerprinted from the binary (~0.97 MB embedded, 298 KiB gzip total; list screens 18 KiB, editor 211 KiB) | new | npm, bundler |
| Canvas editor | embedded `canvas_bridge.js` + vanilla JS host | port | React canvas host |
| Plugins | **Extism** (wazero) WASM; permissions; own Postgres schema + role per plugin | new | compiled Go plugins, `hatamoto build`, Next plugin layer |
| Plugin packages | `.hmp` zip, sha256 + ed25519, stored in Postgres, registry | new | blank imports |
| Themes | packages in Postgres; install, preview, activate, settings in the admin | new | theme-directory env vars |
| Admin auth | database sessions + CSRF | keep | JWT, Google sign-in |
| API / MCP / CLI auth | API keys with scopes | keep | JWT bearer |
| Roles | owner, editor | simplify | viewer, client, author |
| Language | one site setting `language` (for `<html lang>`, JSON-LD `inLanguage`, feeds) plus an optional ordered locale list with translation groups and core-computed hreflang ([ADR 0003](adr/0003-public-locales.md)) | **simplify** | locales table, locale overrides, locale URL tokens, region picker, GeoIP |
| Gated content | none | **remove** | gate partial, teasers, lead JWT, remember tokens |
| AI agent access | official MCP Go SDK at `/mcp` + `hatamoto` CLI | add | — |
| AEO features | AI-crawler hits (in-process), first crawls, AI referrals, AI robots policy, `llms.txt`, JSON-LD, knowledge graph, internal linking v2 | keep | Next crawler pings, linking v1, facts |
| Analytics | pageviews and AI referrals, rolled up daily; no visitor profiles | simplify | visitors, events firehose, intent signals |
| Caching | in-process HTML cache | keep | Redis |
| Observability | `slog`, `/health`, `/ready` | keep | custom OTLP tracer |
| Email | SMTP (optional; CLI fallback for password reset) | keep | SES webhook |
| Forms | forms, submissions, CSV export | keep | lead CRM |
| Integrations | Search Console as a first-party registry plugin (in the first-party monorepo, `Zen-Labs-Corp/hatamoto-first-party/plugins/searchconsole`); webhooks built in (see `docs/WEBHOOKS.md`) | move | in-core packages |
| WordPress import | REST + WXR importer with a URL audit | add | — |
| Backup / portability | `hatamoto export` / `import` full-site archive (JSON + media) | add | — |
| Tests | stdlib `testing`, `httptest`, golden files, Postgres integration tests; never compiled into the binary | keep | testify, vitest, chromedp |
| CI | one GitHub Actions workflow: vet, test, govulncheck, vendor checksums, release on tag | add | — |
| Packaging | release binaries (linux/darwin, amd64/arm64), one Docker image, compose (db + hatamoto) | new | two images, `hatamoto-web` |
| Licence | Apache-2.0 | add | none |

**Totals.** Go modules: pgx, goose, bluemonday, jsonschema, uuid, extism (wazero), MCP SDK, `golang.org/x/*`.
Vendored JS: htmx, Tiptap (module tree), SortableJS, all locked by sha256. Build tool: `go`. Runtime: one binary + Postgres.

**On tests.** `_test.go` files and test-only modules are never compiled into the production binary,
so tests cost nothing at runtime. They stay in the repo because contributors need them and CI uses
them to vet pull requests. Browser tests (chromedp) are dropped: the admin is server-rendered, so
`httptest` + golden HTML covers it; Tiptap and the canvas host get a manual QA checklist in
`docs/QA.md` run before each release. The spike's Playwright harness stays outside the repo as an optional local tool (no Node in the repo).

## 4. Invariants every change must keep

1. **One way to do each thing.** A change that replaces something deletes the old version in the same PR. No feature flags keeping old paths alive.
2. **Import before writing.** Reviewers reject hand-written code that a maintained library already provides; hand-written code is for Hatamoto-specific logic only. Every dependency is pinned and listed in `docs/DEPENDENCIES.md`.
3. **Lowest dependencies.** No new dependency unless it removes more code than it adds and nothing in the stack table already covers it.
4. **No Node** anywhere in the repo after `v1-15`.
5. **Content is untrusted.** `bluemonday` policies are the single source of truth for stored HTML; the browser editor never is. Sanitiser fixtures (`rich-html-samples.json`, `style-cases.json`, `html-style-corpus.json`) stay as Go test data when `frontend/` is deleted.
6. **Admin read paths work without JS**; JS only enhances editing.
7. **No site-specific or fork-specific code.** `scripts/check-neutral.sh` (added in `v1-03`, run by `make check`) fails on the names of the agency site core grew out of (and its private repos and hosts), or on any hard-coded locale or country code used as a path or locale literal; the banned list lives in the script.
8. **Locales are data, not code.** Public locales come only from the site's locale list ([ADR 0003](adr/0003-public-locales.md), which reverses the earlier "no locales" rule). No code may hard-code a locale or country code, add a locale token to URL patterns, or change what a single-locale install serves.
9. **hatamoto.dev runs every change first**; it is the first production user.

## 5. Where it stands

- The core (public site, admin, API, MCP, importer, packages, updater) is built and shipping on the
  `canary`, `edge` and `stable` channels.
- Plugins are sandboxed WASM; the SDK (`hatamoto.dev/sdk`) is at v0 and is published from
  `backend/pkg/sdk` to its own repository.
- hatamoto.dev runs the current release and is the first production site.

## 6. Order of work

Work items below are numbered in dependency order. Each shipped as one pull request, one at a time
on `main`, after the previous one merged.

## 7. Work items

Sizes: S (days), M (about a week), L (two to three weeks), XL (a month or more).

### Phase A: Foundations

#### v1-01-architecture-adr (docs, S)
- **Do:** `docs/adr/0002-single-binary.md` (sections 2 to 4 of this document); mark ADR 0001, roadmap 07/10/12 and `docs/ADMIN_STATIC_EXPORT.md` superseded; README intro rewritten to the section 1 promise; this file committed as `docs/ROADMAP.md`.
- **Done when:** docs merged; no code changes.

#### v1-02-go-1.27 (backend, S)
- **Do:** `go 1.27` + `toolchain go1.27.2` in `backend/go.mod` and `backend/pkg/sdk/go.mod`; Dockerfile builder `golang:1.27-alpine`; `go fix ./...`; adopt any new vet findings; update every dependency to its latest compatible release (`go get -u ./... && go mod tidy`); add the CI workflow (vet, test with a Postgres service, govulncheck) so every later PR is checked.
- **Done when:** build, vet and tests pass on 1.27; hatamoto.dev deployed on a 1.27 binary.

#### v1-03-subtract (both, L)
- **Intent:** delete everything 1.0 does not need before porting anything.
- **Do (backend):**
  - `go mod tidy`: drops unused `chromedp`, `gosimple/slug`, `shopspring/decimal`, `x/sync`, `emersion/*`.
  - Redis (`internal/cache/redis*.go`, `REDIS_*`): in-process HTML cache only.
  - Custom telemetry (`internal/telemetry`): `slog`, `/health`, `/ready`.
  - Google sign-in (`auth/google.go`, `GOOGLE_*`).
  - **All of `internal/lead`** and gated content: `entities.gated`, gate partial and view, teasers, lead JWT, remember tokens, magic tokens, nurture queue, scoring, intent signals, merges, trusted sources, SES webhook, email events, `lead_*` tables. Forms and submissions stay (`internal/form`).
  - Analytics cut to pageviews + AI referrals rolled up daily: drop `hatamoto_visitors`, `hatamoto_intent_signals`, the raw events firehose and its partitions, client-reported event types.
  - Search Console (`internal/searchconsole`, handlers, table); returns as a plugin in `v1-19`.
  - Linking v1 (`internal/linking/*.go`, `linking_handlers.go`); v2 is the only engine.
  - Facts (fact tokens, facts editor, facts data).
  - Agency-site leftovers: city hooks and handlers, series handlers, `tenant_shim`, `redirect_shadow`, `render_mode`; SDK extension points `CaseStudyGeoTags`, `IntentSignal`, `TrustedSource*`, `CaptureIntentHandler`, `AllowedLeadEvent`; columns `fact_confidence`, `successor_entity_id`, `content_role`; table `hatamoto_bot_visits`; roles `viewer`, `client`; every reference to the agency site, its private repo or its pinned core ref in code, comments, Makefile, scripts, env names and package names (60 files today).
  - Scripts for retired paths: `adopt-legacy-db.sh`, `test-legacy-adoption.sh`, `db-catalog*`, `gen-baseline-catalog.sh`, `check-tenant-grep.sh`, `verify-tenant-json.sh`, `check-layer-b-grep.sh`, `gen-redirect-fallback.sh`, `test-plugin-build.sh`, `check-canvas-neutral.sh` (replaced by `check-neutral.sh`).
  - testify: tests move to stdlib `testing`.
- **Do (frontend, interim):** delete the Next screens, components and API clients for everything removed above (leads, gate UI, Search Console, Google button, linking v1, facts), so the interim app keeps building.
- **Not here:** locales (`v1-09`, after the public cutover), JWT and CORS (`v1-15`, when the Next admin is deleted).
- **Done when:** build, tests and hatamoto.dev healthy; `check-neutral.sh` passes except locale literals (enforced from `v1-09`).

#### v1-04-stdlib-router (backend, M)
- **Do:**
  - `chi.URLParam` to `r.PathValue` (98 sites); `/*` wildcards to `{rest...}` (45); root-exact routes use `{$}`.
  - `internal/httpx`: `NewGroup(mux, prefix, mw...)`, nested `Group(prefix, mw...)`, `Handle(pattern, h)`, `Chain(mw...)`; records patterns (`Routes()`) for a route-table test and `hatamoto routes`.
  - Replace `chimw.NewWrapResponseWriter` (2 sites) with a recorder that implements `Unwrap()`.
  - `pkg/sdk` route mounters take an `httpx.Router` interface until native plugins are deleted in `v1-16`.
  - **Routing rule:** the root mux is built once at boot and never changes; anything dynamic is resolved inside fixed catch-alls.
- **Done when:** chi is gone from go.mod; the route-table golden shows every old method and path reaching the same handler; an ambiguous-pattern test proves boot fails loudly.

#### v1-05-single-binary-boot (backend, M)
- **Do:** `cmd/hatamoto` is the only command: `serve` (auto-migrate with embedded goose, generate and persist secrets, start the worker in-process, render always on), `migrate`, `user create`, `user reset-password`, `routes`, `version`. Delete `cmd/api`, `cmd/seed`, `cmd/worker`, `entrypoint.sh`, `HATAMOTO_RENDER`. Drop the unused pgvector extension. New migrations use timestamp versions so parallel PRs never collide. One Go Docker image; compose = db + hatamoto (+ web until `v1-15`).
- **Done when:** `docker compose up` on an empty volume gives a working site.

### Phase B: Public site entirely in Go

#### v1-06-theme-hierarchy (backend, L)
- **Do:**
  - Template lookup: `single-<schema>` → `single` → `page`; `archive-<schema>` → `archive`; `taxonomy-<kind>` → `taxonomy`; `author`; `search`; `home` → `page`.
  - View data: `.Entity` (attributes, terms, author, dates, image), `.Body` (sanitised `content`), `.Blocks`, `.Items` + `.Pagination`, `.Site` (name, language, logo, settings).
  - Template funcs: `recent`, `related`, `terms`, `menu "<location>"`, `paginate`, `srcset`.
  - Routes: archives with pagination, `/category/{slug}`, `/tag/{slug}`, `/author/{slug}`, `/search?q=` (Postgres full-text search).
  - Default theme: every template above, a polished blog layout (post, archive, author, tag), `wp-compat.css` for core Gutenberg classes, dark mode, accessible navigation.
  - No locale features are added here; existing locale handling is left untouched until `v1-09`.
- **Done when:** a post with only `attributes.content` renders; archives paginate; search returns results; goldens for every template.

#### v1-07-public-parity (backend, L)
- **Do:** internal-link injection at render; agent-hit capture fully in-process; `robots.txt`, `llms.txt`, sitemaps, RSS/Atom feeds; Organization/WebSite JSON-LD parity; plugin public-asset slot (filled in `v1-18`).
- **Done when:** `scripts/parity-crawl.sh` (crawl the sitemap on Next-served and Go-served instances; diff status, redirect, title, description, canonical, robots, JSON-LD types, visible-text hash) reports zero diffs on hatamoto.dev.

#### v1-08-public-cutover (both, M)
- **Do:** Caddy sends everything except `/admin*`, `/auth*`, `/_next/*`, `/admin.css` to Go. Delete Next public routes and components, the public parts of `middleware.ts`, the Next plugin layer (its loader script, `extensions.json`, `examples/plugins/hello-frontend`, `PLUGIN_FRONTEND_EXTENSIONS.md`), the public JSON page endpoints (`/public/page/*`, `/public/resolve`, `/public/archive/*`, `/public/site`, `cached_handlers.go`, keeping their loaders as internal functions), `POST /public/agent-hits`.
- **Done when:** hatamoto.dev's public site is served by Go and the parity crawl is clean against production.

#### v1-09-kill-locales (both, L)
- **Intent:** one site, one language. Remove every locale concept.
- **Do (backend):**
  - Drop the locales table, the entity locale overrides table, the entity locale code and scope columns, the enabled-locales attribute and their indexes; per-locale settings (phone, address) move into site config once.
  - Remove the locale URL token, locale-neutral/localized split, hreflang alternates, `/choose-region`, locale negotiation, locale cookie, locale redirect targets, `redirect.SupportedLocales`, the `/localized` loaders, locale override handlers and sanitiser paths, locale sitemaps (one `sitemap.xml` + groups), `internal/locale`, tenant-locale handlers.
  - Add site setting `language` (BCP 47, default `en`) used for `<html lang>`, JSON-LD `inLanguage` and feeds.
  - Defaults: page `/{slug}`, post `/blog/{slug}`; WordPress legacy redirect patterns become the site setting `legacy_wp_redirects` (default off).
  - One-off migration for existing installs: for each previously active locale code, insert redirect `/<code>/**` → `/$1` (301), then drop the tables.
- **Do (frontend, interim):** delete locale screens and tabs from the Next admin.
- **Done when:** `check-neutral.sh` passes in full (no locale literals); `grep -rni 'locale' backend/internal` returns only the `language` setting and stdlib/`x/text` use; hatamoto.dev old `/xx/...` URLs 301 to the new paths.

### Phase C: Admin entirely in Go

Shared rules: pages under `/admin`, rendered by `html/template` from `web/admin/`; database sessions + CSRF;
strict CSP with nonces (`script-src 'self' 'nonce-...'`); htmx for every server interaction; native
`<dialog>`/`popover`/`<details>` for UI; plain CSS on the existing tokens, one stylesheet per partial,
bundled and fingerprinted by `render/css.go`; vendored JS in `web/vendor/` behind an import map;
everything `go:embed`ed. English-only admin. Every screen is keyboard-usable and meets WCAG 2.2 AA.

#### v1-10-admin-shell (backend, L)
- **Do:**
  - **Starting point:** the admin-JS spike (an out-of-repo prototype, verdict **go**: 16/16 scripted and 12/12 interactive checks in Chrome, Firefox and WebKit, zero CSP violations). Port its vendor tool, import-map helper and CSP setup; read `FINDINGS.md`.
  - `backend/tools/vendor` (`make vendor`): downloads each pinned package's full esm.sh module tree with `?target=es2022` (never user-agent dependent), Tiptap as `?bundle&external=@tiptap/core,@tiptap/pm,...` with import-map entries for the shared pieces (never one standalone `?bundle` per package: it loads three copies of core and the editor fails), rewrites imports to local paths, writes `backend/web/vendor/vendor.json` (sha256 per file). `make vendor-verify` (in `make check`) exits 1 on any changed, missing or extra file.
  - Packages: htmx 2, Tiptap 3 (`core`, `pm`, `starter-kit` (includes Link; no separate link package), `extension-image`, `extension-table`, `extensions` (Placeholder), `suggestion` (slash menu), `extension-bubble-menu`), SortableJS, Chart.js 4.
  - Import map rendered by a dedicated template helper (html/template otherwise entity-escapes it and percent-encodes `^`), with `integrity` for every file (tampered files are refused by browsers, only the affected feature fails) and admin JS fingerprinted through the map. Each page template declares its entry modules; the server emits `modulepreload` links for their full dependency closure (642 ms vs 2141 ms first load at 150 ms RTT).
  - CSP: `script-src 'self' 'nonce-...'`, no `unsafe-eval`, no inline style injection. htmx config `allowEval:false`, `includeIndicatorStyles:false`, `responseHandling` swapping 422 (validation) responses; Tiptap `injectCSS:false`; their CSS ships in our stylesheets. `scripts/check-neutral.sh` also fails on `hx-on`, `js:` values and htmx trigger filters in templates (they need eval and silently do nothing).
  - Layout, sidebar (plugin-extensible), page header, flash and toast, tables (sort, filter, bulk actions, pagination), empty and error states.
  - Partials and CSS: button, field, table, card, badge, tabs, dialog, toast, pagination, empty state, media tile; `/admin/assets/tokens.css` published for plugin UIs.
  - JSON-Schema form renderer in Go (port of `FieldRenderer`): string, number, bool, enum, array, object and every `x-hm-field` kind, with validation messages.
  - Auth: login, logout, password reset by email (and `hatamoto user reset-password` without SMTP), invite, session expiry, login lockout (exists).
  - **First-run onboarding wizard:** create owner, site name, URL, language, pick a theme, then "start empty", "install starter content" (home, about, contact, blog with two posts, menus, a contact form) or "import from WordPress" (links to `v1-22b`).
  - Users (owner, editor); site settings (identity, language, contact details, SMTP test, API keys).
- **Done when:** a fresh install goes from first visit to logged-in owner with starter content, without Next.

#### v1-11-admin-content (backend, L)
- **Do:** entity lists per schema (filters, search, bulk status); generic entity edit (name, slug, status, schedule, parent, terms, SEO panel, versions with compare and restore); schema editor (fields, URL pattern settings, sitemap and JSON-LD type); taxonomy screens (hierarchy-ready); media library (upload with drag and drop, grid and list, alt text and caption, usage, picker dialog used by every `image` field).
- **Done when:** every action of the current React entity screens (minus removed features) is possible.

#### v1-12-page-editor (backend, L)
- **Do:** section list (add from theme schemas, SortableJS reorder plus up/down buttons, hide, duplicate, delete, save as section template); schema-driven section forms; Tiptap on `richtext` fields; `html` section with Code (textarea), Visual (Tiptap with the existing html-block nodes, lossless rule unchanged) and Preview tabs; vanilla JS canvas host speaking the unchanged postMessage protocol v1 to the embedded `canvas_bridge.js`; live preview of unsaved changes beside the form.
- **Done when:** handler goldens pass and the `docs/QA.md` canvas checklist (from `docs/CANVAS.md`) passes by hand.

#### v1-13-blog-editor (backend, L)
- **Intent:** a writing experience that WordPress and Ghost users recognise on day one.
- **Do:**
  - Distraction-free writing view: inline title, Tiptap body, word count and reading time, focus mode.
  - Formatting: bubble menu (bold, italic, link, inline code), **slash menu** (built on `@tiptap/suggestion`, listbox with `aria-activedescendant`) (headings, lists, quote, code block, table, image, YouTube/Vimeo embed, divider, callout, FAQ block, html block), Markdown shortcuts (Tiptap input rules), keyboard shortcuts.
  - Images: paste or drag in, uploaded to the media library with progress; alt-text prompt; captions.
  - Paste from Google Docs and Word cleaned to the sanitiser's tag set.
  - Tables: column resizing off, so Tiptap writes no `style="min-width..."`; the sanitiser policy stays unchanged.
  - Custom block nodes (callout, FAQ, YouTube/Vimeo embed, html block) are the untested part of the spike: build them first in this item.
  - Sidebar: slug (from title), status and schedule, featured image, excerpt, categories and tags (create inline), author name, canonical, noindex.
  - **Autosave:** drafts autosave every few seconds; published posts keep a server-side recovery copy and change only on "Update"; every save is a revision with compare and restore.
  - **SEO and AEO panel** (port of `seo-analysis-panel` and `seo-schema-tab`): search-result preview with length checks, focus keyword checks, JSON-LD type and preview, answerability checks (question headings, short direct answers, FAQ block to `FAQPage`), `llms.txt` inclusion, and, after publishing, AI-crawler activity for this post.
  - Preview in the real theme (desktop and mobile widths) in a sandboxed iframe.
- **Done when:** handler goldens pass and the `docs/QA.md` writing checklist passes by hand in Chrome, Firefox and real Safari (macOS and iOS): write, illustrate, schedule, publish, restore a revision, paste from Google Docs and Word, IME input, touch drag, and the slash menu with a screen reader (VoiceOver, NVDA).

#### v1-14-admin-site-and-growth (backend, L)
- **Do:** menus (SortableJS tree with keyboard move buttons, touch drag checked on iOS/Android, locations), forms builder, submissions and CSV export, redirects (with explain and the 404 log), section templates, AI-crawler and AI-referral dashboards plus pageviews (Chart.js on JSON endpoints), internal-linking v2 screens, theme settings placeholder (filled in `v1-18`).
- **Done when:** every remaining React admin screen has a Go equivalent.

#### v1-15-remove-next (both, M)
- **Do:** delete `frontend/`; remove Node from the Dockerfile, Makefile (`deployn`), `ship.sh`, Caddy and the box (`hatamoto-web` service); remove `NEXT_PUBLIC_*`, JWT (`golang-jwt`), CORS (`go-chi/cors`). Admin served by the binary; Caddy is a plain reverse proxy to one port.
- **Known loss:** the ~2,000 lines of `node:test`/jsdom unit tests for `backend/web/admin/js/*` were deleted with Node (the modules still ship). Nothing runs a DOM or a JS engine under `go test`: goja was not added (it is not in the module cache and needs a DOM anyway), so the cases are manual items in `docs/QA.md` "Browser modules" (canvas navigation, malformed and oversized messages, lookalike hosts, text-walk parity, blog editor local copy and in-flight save). The Go half of the text-walk parity stays in `render/canvas_text_golden_test.go` with its fixtures in `testdata/canvas-text`. An optional, out-of-repo Playwright harness (a local tool, not CI) can drive the same checks.
- **Done when:** `git ls-files | grep -E 'package.json|\.tsx?$'` is empty; `go build` works on a machine without Node; hatamoto.dev runs one process and ship.sh deploys one artefact.

### Phase D: Sandboxed plugins and runtime themes

#### v1-16-wasm-runtime (backend, L)
- **Starting point:** the Extism spike (an out-of-repo prototype, verdict **go**, 14 tests passing with `-race` on Go 1.27). Read `FINDINGS.md`; port its host code and tests into `internal/wasmhost` rather than starting fresh.
- **Do:**
  - Host: `extism/go-sdk` v1.7+ (wazero). Guests build with the standard Go toolchain (`GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`) and the Extism Go PDK; TinyGo is optional for authors, not required.
  - **Spike conditions (all mandatory):**
    1. Never expose Extism's built-in HTTP call (it follows redirects to non-allowlisted hosts). Core's own `http.fetch` host function re-checks the allowlist on every redirect and refuses private, loopback and link-local addresses.
    2. Discard a plugin instance after any error, trap or timeout; never reuse it.
    3. Every call has a timeout > 0 (default 250 ms for filters, 10 s for actions, configurable per hook through `limits.hook_timeouts_ms` in `plugin.json`, replacing the single `limits.timeout_ms`); a zero timeout is rejected at load with a clear error.
    4. Per-plugin memory cap and max live instances. Document and test that the effective ceiling is 2× the configured page cap (Extism's kernel module gets the same cap).
    5. wazero compilation cache on disk under the data directory (`$HATAMOTO_DATA/cache/wasm`); it is a rebuildable cache, the package of record stays in Postgres. Target: plugin load ≤ 100 ms warm.
    6. Small hook payloads: hooks carry structured data (blocks, request fields, single records, deltas), never rendered HTML or whole sitemaps. `sitemap.urls` becomes "add entries" (plugin returns additions in `add`, at most 5,000; the host merges and skips locs the file already has).
  - Hooks map to exports (`on_entity_published`, `filter_jsonld_enrich`, ...), JSON in and out, fail-open (the original value is kept on any error).
  - `pkg/sdk` becomes the contract: DTOs, hook names, manifest, no pgx or router types. `pkg/sdk/guest`: a thin layer over the Extism Go PDK with the familiar `OnAction`/`OnFilter` API. Other languages use their Extism PDK and the JSON schemas in `docs/PLUGIN_API.md`.
  - Dependency note: Extism links `dylibso/observe-sdk`, OTLP protobufs, `demangle`, `wabin` and `gobwas/glob` (~5 MB of the 7.2 MB the binary grows by) even though tracing is unused. Accepted for 1.0 because the PDK ecosystem is the reason to use Extism; record it in `docs/DEPENDENCIES.md` and open an upstream issue/PR to make the observe adapter optional.
  - Delete native plugins: `cmd/hatamoto build`, `cmd/api/plugins.go`, blank-import wiring, `backend/plugins/`, `internal/pluginhost` native adapters. Port `examples/plugins/hello` to WASM with a `make` target.
- **Done when:** the spike's 14 tests pass inside the repo under `-race`, plus: hello.wasm loads at runtime and handles `entity.published` and `jsonld.enrich`; hostile modules (infinite loop, memory bomb, runaway recursion, trap, disallowed host, redirect to a disallowed host, private address) cannot affect requests; a v1→v2 swap under load has zero failed calls; 50 load/unload cycles leak no memory or goroutines.

#### v1-17-plugin-capabilities (backend, L)
- **Do:**
  - Permissions in `plugin.json`, shown and granted at install.
  - Host functions: `entities.read|write` (with re-entrancy guard), `terms`, `menus`, `media.read|write`, `redirects`, `forms.read`, `settings`, `log`, `mail.send`, `http.fetch` (manifest allowlist, SSRF guard), `kv`.
  - Own data: Postgres schema `plugin_<name>` + role `hm_p_<id8>_<name>` (`<id8>` is the first 8 hex digits of this database's id; rights on its own schema only; roles are cluster-wide in Postgres, so a bare `hm_plugin_<name>` would let a second Hatamoto database in the same cluster take over another install's role, which review found, and the 63-byte identifier limit still leaves room for the name); queries run with `SET LOCAL ROLE`, `search_path`, `statement_timeout`; plugin migrations run the same way through goose.
  - Routes declared in the manifest, served under `/api/v1/p/<plugin>/...` (auth, CSRF, roles) and `/p/<plugin>/...` (rate limited) by per-plugin routers held in an `atomic.Pointer` map; added and removed without restart; never shadow core routes.
  - Cron registrations on the in-process scheduler; template funcs, partials and section packs into the theme chain.
  - Generic hooks needed by real sites: `form.submitted` (action), `render.head` (filter, structured tags incl. `link rel=alternate hreflang`), `render.page` (request-aware filter: read cookies, replace or hide blocks, set a cookie; fail-open), `entity.before_save`.
- Denied capability errors use the spike's typed shape: `{"code":"capability_denied","capability":"entities.write"}` (SDK 0.8; `guest.CapabilityDenied(err)` reads it); the denied operation never reaches the store.
- **Done when:** a test plugin cannot read `hatamoto_users` even with crafted SQL (the part the spike did not cover); denied capabilities return typed errors; `render.page` round-trips in under 1 ms p50 with a typical page's blocks.

#### v1-18-packages-and-themes (backend, L)
- **Do:**
  - `.hmp` format (`plugin.json` or `theme.json`, `plugin.wasm`, `sections/`, `schemas/`, `layouts/`, `partials/`, `assets/`, `migrations/`, `ui/`), stored in `hatamoto_packages` (bytea, sha256, ed25519 signature, signer).
  - Registry index JSON (default `https://hatamoto.dev/registry.json`, configurable); upload as alternative.
  - Admin Plugins page: browse, install, permission prompt, enable/disable, update, uninstall (with data-drop confirmation).
  - Admin Themes page: browse, install, preview with any theme, activate after a missing-section-type check, theme settings form from `settings_schema`, values exposed to templates as `.Settings`.
  - Plugin admin UI in `<iframe sandbox="allow-scripts">` with a postMessage bridge carrying only the plugin's capabilities.
  - The render chain loads the active theme from Postgres; env theme dirs removed; `hatamoto theme dev <dir>` watches a directory and pushes it for local theme development; `hatamoto theme pack` and `hatamoto plugin pack` build signed packages.
- **Done when:** a plugin and a theme install from a local signed registry without restart; uninstall changes no core data.

#### v1-19-first-party-plugins (backend, M)
- **Do:** Search Console plugin (OAuth through plugin routes, data in its own schema, allowlist `*.googleapis.com`); outgoing webhooks plugin (replaces `internal/webhook`, deleted from core). Both signed and in the default registry, with source in `examples/` as reference plugins.
- **Done when:** both install with one click and core has no Search Console or webhook code.
- **Reversed:** the webhooks half is superseded. Outgoing webhooks are built into core again (`internal/webhook`, Settings > Webhooks) and the webhooks plugin is removed on upgrade; Search Console stays a plugin. See [`docs/WEBHOOKS.md`](WEBHOOKS.md).

### Phase E: Agents and migration

#### v1-20-mcp-cli-export (backend, M)
- **Do:** MCP server with the official Go SDK (streamable HTTP) at `/mcp`, scoped API keys; tools for entities (draft, update, publish), schemas, media upload, terms, menus, redirects, search, AI-crawler analytics (crawler hits, first crawls, AI referrals per page), sitemap and `llms.txt` preview, JSON-LD validation. CLI subcommands over the same services. `hatamoto export` / `hatamoto import`: full-site archive (JSON + media + packages) for backup, moving hosts and no lock-in.
- **Done when:** an MCP client drafts, publishes and reports AI-crawler activity for a page; export then import into an empty database reproduces the site.

#### v1-21-import-foundations (backend, M)
- **Do:** URL tokens `{year}`, `{month}`, `{day}` and multi-level `{path}` for nested parents; hierarchical terms (`parent_id`, `description`) and custom term kinds; `authors` (name, slug, bio, avatar) as content, not user accounts; `entity.Service.Import` (sets dates, status, parent, author; validates and sanitises; one `import.completed` event).
- **Done when:** `/2024/05/hello-world` and `/services/seo/local` route correctly.

#### v1-22-wordpress-importer (backend, XL)
- **Do:**
  - Sources: WordPress REST API (application password for drafts and menus) and WXR upload.
  - Scan report: counts, permalink structure, SEO plugin, page builders, forms, multilingual plugins (WPML, Polylang: default language only), unsupported plugins, expected fidelity.
  - Runner: settings, terms, authors, media (bounded, SSRF-guarded, deduped), pages, posts and custom post types, menus, redirects, internal-link rewrite, cache and sitemap rebuild, URL audit against the old sitemap.
  - `hatamoto_import_runs` + `hatamoto_import_map` make re-runs idempotent (re-sync before cutover).
  - Mapping: post body to `attributes.content` (edited in the blog editor); Yoast, Rank Math and SEOPress fields to SEO fields; Gutenberg comments stripped; YouTube/Vimeo embeds kept; shortcodes, forms and page-builder pages listed in the report; members-only content imported as normal drafts.
  - `hatamoto import wordpress` CLI (the admin wizard is v1-22b).
- **Done when:** against a dockerised fixture WordPress (nested pages, dated permalinks, Yoast, gallery, embed, Contact Form 7): every old URL returns 200 or one 301, no `wp-content` URLs remain, SEO titles match 100%, a re-run creates zero duplicates.

#### v1-22b-import-wizard (backend, M)
- **Do:** admin wizard over the v1-22 engine: Source, Scan report, Options, Run (live progress), Report (per-item errors, redirects created, URL audit, links to low-fidelity pages); linked from the onboarding wizard.
- **Done when:** the fixture import runs end to end from the admin.

### Phase F: Ship

#### v1-23-dependency-audit (backend, S)
- **Do:** confirm the final set in section 3; update everything to latest; remove anything unused; `docs/DEPENDENCIES.md` (purpose, version, licence, update command for every Go module and vendored file); `govulncheck` and `make vendor-verify` in `make check`; licence scan (all Apache-2.0 compatible).

#### v1-23a-admin-ia-design-and-feel (both, L)
- **Do:** implement the approved admin mockups: four primary items plus a Settings secondary sidebar, one icon set, AA tokens, ⋯ row menus, three page widths, real page loads with cross-document View Transitions and Speculation Rules instead of `hx-boost`, 301s from every old admin URL, and a local browser harness (`scripts/admin-qa/`) for axe, keyboard, performance and screenshots.
- **Done when:** every screen matches its mockup at 1440 and 390 in light and dark, axe reports 0 violations, CLS < 0.05, INP < 200 ms and navigation < 300 ms on every primary screen, and `docs/QA.md` records the results.

#### v1-24-oss-release (both, L)
- **Do:**
  - `LICENSE` (Apache-2.0), `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, issue and PR templates.
  - Docs (served by Hatamoto itself on hatamoto.dev): install, upgrade policy (semver, forward-only automatic migrations, release notes), themes guide, plugin guide (Go guest SDK + other PDKs), MCP, WordPress import, AEO features, CLI reference, `docs/QA.md` release checklist.
  - Extend the CI workflow from v1-02: vendor checksums, release on tag (binaries for linux/darwin amd64/arm64, checksums, signatures, Docker image).
  - Publish the SDK as `v1.0.0` (plain tag in the public SDK repository, see `scripts/publish-sdk.sh`) and freeze it.
  - Registry live with the default theme, a second theme, hello and Search Console plugins, all signed (the webhooks plugin was later moved into core).
  - No telemetry or phone-home, stated in the README.
  - Keep internal deploy notes and working plans out of the public tree (`export-ignore` in `.gitattributes`, `scripts/public-snapshot.sh`); publish the public repo as one fresh commit, without the private history.
- **Done when:** a stranger goes from download to a published post, an imported WordPress site and an installed plugin using only the README; recorded on a clean machine.

### Phase G: Public locales (1.x minors, additive; [ADR 0003](adr/0003-public-locales.md))

Every phase is additive and N-1 safe, and an install with no locale list stays byte-identical.

#### locales-1-foundation (backend, XL)
- **Do:** the ordered locale list, default, prefix-all and x-default settings with a no-JS form; shared content (`und`); translation groups; locale-aware public visibility filter; locale prefix routing and the bare-root 302; core-computed hreflang and x-default; per-locale sitemaps with `xhtml:link` alternates.
- **Done when:** a seeded multi-locale install serves every locale, prints hreflang in settings order and lists per-locale sitemaps, and a one-locale install's HTML, URLs and sitemaps are unchanged.

#### locales-2-inheritance (backend, L)
- **Do:** a copy inherits top-level fields from the base copy until edited; propagation runs as a worker job with conditional writes; links in inherited fields are rewritten to the copy's locale.

#### locales-3-admin (admin-ui, L)
- **Do:** a Translations panel in the page editor (copies, status, create copy, inherit or own value per field); a Locale column and filter on the entity list.

#### locales-4-per-locale-data (both, L)
- **Do:** per-locale contact and office settings (identity stays global); per-locale menus, theme parts and blocks with fallback to the default locale; per-locale search, feeds and llms.txt.

#### locales-5-import (backend, M)
- **Do:** import WordPress multilingual sites (WPML, Polylang) as linked copies; a conversion dry-run for sites that stored regions in attributes.

---

## 8. Not in 1.0 (decided)

- React, Next.js, npm/Node tooling, Tailwind, CodeMirror, Alpine.
- GeoIP and IP-based or cookie-based locale routing (the reverse proxy does that), machine translation. Multi-locale sites are covered by [ADR 0003](adr/0003-public-locales.md).
- Gated content, lead capture beyond forms, CRM features.
- A headless or public content JSON API (one renderer only).
- SQLite or any second database.
- Compiled-in Go plugins.
- Redis, OpenTelemetry, JWT, CORS, Google sign-in.
- Facts, author role, visitor profiles.
- Search Console in core (it is a registry plugin).
- Browser test automation (manual QA checklist instead).
- Multi-site in one database (one install = one site).
- Comments, e-commerce, page-builder layout conversion, newsletters, public demo instance.
- Hand-rolled replacements for libraries that already work.

## 9. Decisions taken for 1.0

1. Apache-2.0 licence.
2. No headless mode in 1.0.
3. Roles: owner and editor only.
4. Post URLs default to `/blog/{slug}` (WordPress imports keep their own structure).
5. Autosave: drafts autosave; published posts change only on "Update".
6. Order: public site before admin; locales killed right after the public cutover.

## 10. Implementation notes (deliberate deviations from this plan)

What the items that already merged did differently, or measured, and why. The plan text above has been brought in line where a name changed; these are the things that are not simply renames.

- **Names.** The vendoring tool is `backend/tools/vendor` with `backend/web/vendor/vendor.json` (not `cmd/hm-vendor` and `vendor.lock`): same behaviour, no churn after v1-10 shipped it. Plugin database roles are `hm_p_<id8>_<name>`, not `hm_plugin_<name>`: Postgres roles are cluster-wide, so two Hatamoto databases in one cluster must not collide and a plugin name alone would let one install take over another's role (found in review, tested by `TestTwoDatabasesOneCluster`).
- **v1-15 dropped the JS unit tests.** Deleting Node deleted the `node:test`/jsdom suites; the cases became manual items in `docs/QA.md` ("Browser modules") and the optional out-of-repo Playwright harness. See the known loss under v1-15.
- **Plugin timeouts** are per hook (`limits.hook_timeouts_ms`; filters and template funcs 250 ms, everything else 10 s; at most 10 s; 0 is refused). `limits.timeout_ms` and its 2 s default are gone (SDK 0.8).
- **`capability_denied`** replaces `permission_denied` for plugin host calls (SDK 0.8). The admin bridge that serves a plugin's sandboxed admin page to its iframe still answers `permission_denied` to the page's script; that is the admin's own JSON, not a host call.
- **`sitemap.urls`** is additions-only (SDK 0.8): plugins see the group and language and add up to 5,000 entries; they can no longer rewrite the file's own entries.
- **Compile cache** defaults to `$HATAMOTO_DATA/cache/wasm` (`HATAMOTO_DATA`, default `./data`), mode 0700, rebuildable, falling back to memory when unusable. `HATAMOTO_PLUGIN_CACHE_DIR` overrides it, `off` disables it. Warm load measured at about 50 ms (target 100 ms).
- **Memory ceiling** is documented and tested: guest and Extism kernel each get `memory_pages`, so up to 2x per instance (`TestMemoryCeilingIsTwiceMaxPages`, `TestConcurrentMemoryBombsBounded`).
- **`render.page` meets 1 ms p50 on the slim payload (SDK 0.9).** Blocks go to the plugin as `id`, `type`, `title`, `hidden` and a few scalar fields, and the host re-attaches content by id (a plugin can still replace a block by returning new content). Measured on a Ryzen 5 5600 with the capa plugin: p50 480 µs for 4 blocks, 660 µs for 8, 810 µs for 12. The full payload (`render_page.full_blocks`, and automatically for plugins built for SDK < 0.9) keeps the old cost: 1.2 ms, 2.2 ms, 4.1 ms, about 0.5 ms plus 0.5 ms per KB of JSON. `BenchmarkRenderPage` reports both; `TestRenderPageP50` asserts under 1 ms for the slim path and a 10 ms regression bound for the full one.
- **Extism binary growth** (about 5 of 7.2 MB from modules the server does not use) is accepted for 1.0 and recorded in `docs/DEPENDENCIES.md`. The upstream issue is still to be filed by a human.
- **Browser matrix** (real Safari macOS and iOS, paste from Google Docs and Word, IME, touch drag, VoiceOver and NVDA) is manual and listed in `docs/QA.md` and `docs/qa/admin-js.md`; no CI covers it.
