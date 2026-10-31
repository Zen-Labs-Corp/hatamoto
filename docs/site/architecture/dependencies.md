---
title: Dependencies
nav_title: Dependencies
description: Every third-party Go module and browser file Hatamoto ships, with versions and licences, how they are checked and how to update them.
---

This page lists every third-party module and browser file built into the `hatamoto` program, and the commands that check and update them. A summary of the stack is on [Stack and package map](package-map.md).

## Policy

Hatamoto keeps its dependency set small on purpose. The rules the project follows:

- The standard library first. One module per job.
- No cgo, so the binary builds with the Go toolchain alone.
- Permissive licences only: MIT, BSD, Apache-2.0, ISC (and 0BSD for one JavaScript file). Copyleft or unknown licences fail the build.
- A new dependency must remove more code than it adds, and nothing already in the stack may cover the job.
- Every dependency is pinned, and the browser files are pinned by SHA-256.
- No Node, no npm and no bundler in the build. The Go toolchain is the only build tool. The one Node file in the repository is the development-only icon generator described under [Admin icons](#admin-icons).

The Go version is `go 1.27` with `toolchain go1.27.3`, as declared in `backend/go.mod`. The authoritative list is always `go list -m all` and `backend/go.mod`; the tables below were checked against them.

## Go modules in the binary

These are the direct requirements of `backend/go.mod`:

| Module | Version | Licence | Purpose |
|---|---|---|---|
| `github.com/extism/go-sdk` | v1.7.1 | BSD-3-Clause | host side of the WebAssembly plugin runtime |
| `github.com/tetratelabs/wazero` | v1.9.0 | Apache-2.0 | the WebAssembly engine under Extism, and its compilation cache |
| `github.com/jackc/pgx/v5` | v5.11.0 | MIT | Postgres driver and connection pool |
| `github.com/pressly/goose/v3` | v3.29.0 | MIT | database migrations, used as a library |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | UUID identifiers |
| `github.com/microcosm-cc/bluemonday` | v1.0.27 | BSD-3-Clause | HTML sanitiser for editor content and plugin output |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | Apache-2.0 | the MCP server for AI agents |
| `github.com/santhosh-tekuri/jsonschema/v5` | v5.3.1 | Apache-2.0 | JSON Schema validation for content types and plugin settings |
| `golang.org/x/crypto` | v0.58.0 | BSD-3-Clause | argon2id password hashing |
| `golang.org/x/image` | v0.47.0 | BSD-3-Clause | WebP decoding and resizing for media variants |
| `golang.org/x/net` | v0.61.0 | BSD-3-Clause | HTML parsing and tokenising |
| `golang.org/x/text` | v0.43.0 | BSD-3-Clause | Unicode normalisation, language tags, slugs |
| `golang.org/x/time` | v0.16.0 | BSD-3-Clause | rate limiters |
| `hatamoto.dev/sdk` | in-tree | project | the plugin contract, replaced to `backend/pkg/sdk` |

`wazero` is pinned at v1.9.0 on purpose. A newer release passed the sandbox tests, but loading compiled code from the warm cache was measured slower than the project's 100 ms target on CI runners, so the pin stays until the next Extism release is evaluated.

The plugin side of the Extism ABI, `github.com/extism/go-pdk` (v1.1.3, BSD-3-Clause), lives in the SDK module and in plugins. It is never linked into the server binary.

### Indirect modules

Each indirect module is pulled in by exactly one of the above:

| Pulled in by | Indirect modules |
|---|---|
| bluemonday | `aymerick/douceur`, `gorilla/css` (CSS parsing) |
| pgx | `jackc/pgpassfile`, `jackc/pgservicefile`, `jackc/puddle/v2` (pooling) |
| goose | `mfridman/interpolate`, `sethvargo/go-retry`, `go.uber.org/multierr` |
| the MCP SDK | `google/jsonschema-go`, `segmentio/asm`, `segmentio/encoding`, `yosida95/uritemplate/v3`, `golang.org/x/oauth2` |
| Extism | `dylibso/observe-sdk/go`, `gobwas/glob`, `ianlancetaylor/demangle`, `tetratelabs/wabin`, `go.opentelemetry.io/proto/otlp`, `google.golang.org/protobuf` |
| several | `golang.org/x/sync`, `golang.org/x/sys` |

`go list -m all` prints a longer list (about 130 entries) because it includes the module graphs of goose, Extism and the MCP SDK for features Hatamoto does not import. Those modules are not compiled or linked into the binary.

## Extism and the binary size

The plugin runtime was chosen after a spike that measured binary size with `-ldflags "-s -w"`:

| Binary | Size |
|---|---|
| net/http, JSON and slog only | 6.71 MB |
| plus raw wazero and WASI | 9.00 MB |
| plus Extism and the runtime | 13.90 MB |

Extism adds roughly 4.9 MB on top of wazero, and about 5 MB of the 7.2 MB increase comes from modules the server never uses (a tracing adapter, OTLP protobufs and a WebAssembly parser). The project accepted this: avoiding it would mean maintaining its own implementation of the Extism kernel on wazero, which is worse than a 14 MB server binary. The decision is to be revisited if Extism makes the tracing adapter optional.

## Vendored JavaScript

The admin ships its browser dependencies as committed files, served from the binary, with no CDN and no Node. `backend/web/vendor/vendor.json` lists each file with version, licence, source URL, SHA-256 and size, and is the source of truth.

| Group | Version | Licence | Used for |
|---|---|---|---|
| htmx | 2.0.11 | 0BSD | partial page updates in the admin |
| SortableJS | 1.15.7 | MIT | drag and drop ordering |
| Tiptap and ProseMirror module graph | 3.31.3 (62 files) | MIT | the rich text editor |

The public site's own script, `hm.js`, is part of the default theme (`backend/internal/render/themes/default/assets/js/hm.js`), not a vendored dependency. Licence texts for the vendored files, including each package of the Tiptap module graph, are in `backend/web/vendor/licenses/`, with a `NOTICE` file in `backend/web/vendor/`.

Two commands keep the vendored files honest:

```bash
make vendor          # network: fetch pinned files; a pinned file that changed upstream fails
make vendor-verify   # offline: committed bytes must match vendor.json; runs in make check and CI
```

To update a file, change the version in `vendor.json`, run `cd backend && go run ./tools/vendor fetch --update`, and review the diff. For Tiptap, run the editor checklist in the QA notes in a browser before releasing, because the editor can only be verified there.

## Admin icons

The admin icon set is generated Go code, not a runtime dependency. `backend/internal/admin/icons_lucide.go` holds SVG path data for about 85 icons taken from Lucide (ISC licence). Nothing from Lucide is imported, bundled or fetched at run time, and Node is not part of the build. Contributors add an icon with the generator script `backend/tools/gen-icons/gen-icons.mjs`, which is never shipped: add one line to its name map, run it with Node from `backend/` against a local `lucide-react` package (`node tools/gen-icons/gen-icons.mjs /path/to/node_modules/lucide-react`), then format the output with `gofmt`. The brand glyph `mark` is drawn by hand in `icons.go`; the brand seal `seal` is the outline of one glyph from Noto Sans CJK JP (SIL Open Font License 1.1, recorded in NOTICE). The file is never edited by hand, and a test fails if a template names an icon that is missing from the map.

## Licence scan

Everything above is permissive: MIT, BSD-3-Clause and Apache-2.0 for Go, MIT and 0BSD for JavaScript. A stdlib-only tool in `backend/tools/licenses` lists the modules that provide packages to the server, its tests and the SDK, reads each module's `LICENSE` file from the module cache and classifies it. Anything it cannot classify, and any copyleft licence, fails. It runs in `make check` and in CI, and can write the licence bundle for a release:

```bash
make licenses
```

The output goes to `build/licenses/` (not committed) as `THIRD_PARTY_LICENSES.md` plus each module's licence text.

## Updating Go modules

```bash
cd backend
go list -m -u all                      # what has a newer version
go get example.com/module@v1.2.3       # one module
go mod tidy
go build ./... && go vet ./... && go test ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The SDK is a separate module: update `extism/go-pdk` in `backend/pkg/sdk`, then run `go mod tidy` in `backend` and in each directory under `examples/plugins`. After a wazero or Extism bump, run the sandbox tests with the race detector:

```bash
cd backend && go test -race ./internal/wasmhost ./internal/pluginhost
```

Those tests are the acceptance test for the runtime; `TestCompileCacheWarmLoad` is the one that holds the 100 ms warm-cache target. `make check` also runs `govulncheck` on the server and the SDK (`make vuln`), `make vendor-verify` and the licence scan on every pull request.
