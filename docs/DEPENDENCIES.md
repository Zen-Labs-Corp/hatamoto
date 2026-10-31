# Dependencies

Every third-party module that ends up in the `hatamoto` binary or in a plugin built
against the SDK, why it is there, and how to update it. Versions are those in
`backend/go.mod` (and `backend/pkg/sdk/go.mod`); `go list -m all` is authoritative if
this table drifts. Licences were read from each module's `LICENSE` file.

Rules: the standard library first; one module per job; no cgo; every addition is
reviewed for licence (permissive only: MIT, BSD, Apache-2.0, ISC) and for what it pulls in.
`govulncheck`, `make vendor-verify` and the licence scan run in `make check` and in CI on every pull request.

## Updating

```sh
cd backend
go list -m -u all                      # what has a newer version
go get example.com/module@v1.2.3       # one module (add -t to include test deps)
go mod tidy
go build ./... && go vet ./... && go test ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The SDK module is separate: `cd backend/pkg/sdk && go get github.com/extism/go-pdk@... && go mod tidy`
and then `go mod tidy` in `backend` and in each `examples/plugins/*`. After a wazero or
Extism bump re-run `go test -race ./internal/wasmhost ./internal/pluginhost`: the sandbox
tests are the acceptance test for the runtime.

## Go modules: direct

| Module | Version | Licence | Purpose | Update |
|---|---|---|---|---|
| `github.com/extism/go-sdk` | v1.7.1 | BSD-3-Clause | WASM plugin runtime (host side, on wazero); see [Extism](#extism-and-the-binary-size) | `go get github.com/extism/go-sdk@latest` |
| `github.com/tetratelabs/wazero` | v1.9.0 | Apache-2.0 | WebAssembly runtime under Extism; compile cache. We use its compilation cache. **Held at v1.9.0 on purpose:** v1.12.0 passes the sandbox tests but its warm compile-cache load measured 124-205 ms on CI runners against the 100 ms plan criterion (`TestCompileCacheWarmLoad`); v1.9.0 passes. Revisit with the next Extism release | `go get github.com/tetratelabs/wazero@latest` |
| `github.com/jackc/pgx/v5` | v5.11.0 | MIT | PostgreSQL driver and pool | `go get github.com/jackc/pgx/v5@latest` |
| `github.com/pressly/goose/v3` | v3.29.0 | MIT | Database migrations (`hatamoto migrate`) | `go get github.com/pressly/goose/v3@latest` |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | UUID identifiers | `go get github.com/google/uuid@latest` |
| `github.com/microcosm-cc/bluemonday` | v1.0.27 | BSD-3-Clause | HTML sanitiser for editor content and plugin output | `go get github.com/microcosm-cc/bluemonday@latest` |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | Apache-2.0 | MCP server for AI agents | `go get github.com/modelcontextprotocol/go-sdk@latest` |
| `github.com/santhosh-tekuri/jsonschema/v5` | v5.3.1 | Apache-2.0 | JSON Schema validation (entity schemas, plugin settings) | `go get github.com/santhosh-tekuri/jsonschema/v5@latest` |
| `golang.org/x/crypto` | v0.58.0 | BSD-3-Clause | argon2 password hashing | `go get golang.org/x/crypto@latest` |
| `golang.org/x/image` | v0.47.0 | BSD-3-Clause | WebP decoding and resizing for media variants | `go get golang.org/x/image@latest` |
| `golang.org/x/net` | v0.61.0 | BSD-3-Clause | HTML parsing and tokenising (`x/net/html`) | `go get golang.org/x/net@latest` |
| `golang.org/x/text` | v0.43.0 | BSD-3-Clause | Unicode normalisation, language tags and names, slugs | `go get golang.org/x/text@latest` |
| `golang.org/x/time` | v0.16.0 | BSD-3-Clause | Rate limiters | `go get golang.org/x/time@latest` |
| `github.com/oschwald/maxminddb-golang` | v1.13.1 | ISC | GeoIP country lookup from an owner-supplied .mmdb (read into memory, no mmap, no cgo) | `go get github.com/oschwald/maxminddb-golang@latest` |
| `hatamoto.dev/sdk` | local | project | The plugin contract; `replace`d to `backend/pkg/sdk` | in-tree |
| `github.com/extism/go-pdk` | v1.1.3 | BSD-3-Clause | Plugin side of the Extism ABI; in the SDK module and in every plugin, never in the server binary | `cd backend/pkg/sdk && go get github.com/extism/go-pdk@latest` |

## Go modules: indirect

| Module | Version | Licence | Pulled in by |
|---|---|---|---|
| `github.com/aymerick/douceur` | v0.2.0 | MIT | bluemonday (CSS parsing) |
| `github.com/gorilla/css` | v1.0.1 | BSD-3-Clause | bluemonday, douceur |
| `github.com/jackc/pgpassfile` | v1.0.0 | MIT | pgx |
| `github.com/jackc/pgservicefile` | v0.0.0-20240606120523-5a60cdf6a761 | MIT | pgx |
| `github.com/jackc/puddle/v2` | v2.2.3 | MIT | pgx (connection pool) |
| `github.com/mfridman/interpolate` | v0.0.2 | MIT | goose |
| `github.com/sethvargo/go-retry` | v0.5.0 | Apache-2.0 | goose |
| `go.uber.org/multierr` | v1.11.0 | MIT | goose |
| `github.com/google/jsonschema-go` | v0.4.3 | MIT | MCP go-sdk |
| `github.com/segmentio/asm` | v1.2.1 | MIT | MCP go-sdk (via segmentio/encoding) |
| `github.com/segmentio/encoding` | v0.5.4 | MIT | MCP go-sdk |
| `github.com/yosida95/uritemplate/v3` | v3.0.2 | BSD-3-Clause | MCP go-sdk |
| `golang.org/x/oauth2` | v0.37.0 | BSD-3-Clause | MCP go-sdk |
| `golang.org/x/sync` | v0.24.0 | BSD-3-Clause | goose, MCP go-sdk |
| `golang.org/x/sys` | v0.49.0 | BSD-3-Clause | wazero, pgx, x/crypto |
| `github.com/dylibso/observe-sdk/go` | v0.0.0-20240828172851-9145d8ad07e1 | Apache-2.0 | Extism go-sdk (tracing adapter, unused) |
| `github.com/gobwas/glob` | v1.0.0 | MIT | Extism go-sdk (allowed-host patterns) |
| `github.com/ianlancetaylor/demangle` | v0.0.0-20261012173340-cbb8aaea4290 | BSD-3-Clause | Extism go-sdk / observe-sdk |
| `github.com/tetratelabs/wabin` | v0.0.0-20230304001439-f6f874872834 | Apache-2.0 | Extism go-sdk / observe-sdk (wasm binary parsing) |
| `go.opentelemetry.io/proto/otlp` | v1.11.1 | Apache-2.0 | observe-sdk (OTLP protobufs) |
| `google.golang.org/protobuf` | v1.36.12 | BSD-3-Clause | observe-sdk, OTLP |

## The final set against the roadmap

ROADMAP §3 "Totals" lists: pgx, goose, bluemonday, jsonschema, uuid, Extism (wazero), the MCP
SDK and `golang.org/x/*`, plus the vendored htmx, Tiptap and SortableJS. The audit
(v1-23) checked every `require` of every module in the repository:

* **Direct (backend):** exactly those, nothing else. `go mod tidy` leaves no unused require and
  `go list -deps -test ./...` reaches every one. `wazero` is listed apart from Extism because
  the host uses its compile cache directly. There is no test-only module: tests use the
  standard library (`testing`, `httptest`).
* **Indirect, each pulled by one of the above** (the "Pulled in by" column): bluemonday
  (douceur, gorilla/css), pgx (pgpassfile, pgservicefile, puddle), goose (interpolate,
  go-retry, multierr), the MCP SDK (jsonschema-go, segmentio, uritemplate, oauth2) and
  Extism (observe-sdk, glob, demangle, wabin, OTLP, protobuf; see the size note below).
  Nothing was removable: the Extism tree is the only part we do not want, and it cannot be
  dropped without forking Extism (decision below).
* **`go list -m all` is larger than this file** (about 130 entries, e.g. grpc, otel, cloud and
  Azure SDKs, `testify`, `x/tools`): those appear only in the *module graphs* (go.mod
  requirements) of goose, Extism and the MCP SDK for features we do not import. They are not
  compiled or linked and no code from them is in the repository; `go list -deps -test ./...`
  shows 34 third-party modules, which is every module in the tables here.
* **SDK, example plugins, test guests:** `backend/pkg/sdk`, `examples/plugins/*`,
  `backend/internal/wasmhost/testdata/hostile` and `backend/internal/pluginhost/testdata/capa`
  depend only on `extism/go-pdk` and (the examples) on the in-tree SDK.
* **Small single-use dependencies** were reviewed for replacement by the standard library:
  none removes more code than it adds (`uuid` has about 870 call sites; `x/time`,
  `jsonschema` and `bluemonday` are real implementations).

## Licence scan

Every module above is permissive: **MIT, BSD-3-Clause, Apache-2.0** (Go) and **MIT, 0BSD**
(JavaScript). No GPL, AGPL, LGPL, SSPL, MPL or unknown licence is present, so the set is
compatible with the project's Apache-2.0.

Method: `backend/tools/licenses` (stdlib only, offline once modules are downloaded) lists the
modules that provide packages to the server, its tests and the SDK (native and `wasip1`), reads
each module's `LICENSE`/`COPYING` file from the module cache and classifies it by its
distinctive text (a small SPDX matcher, not a full database). Anything it cannot classify
prints `UNKNOWN` and fails, as does any copyleft licence; a human then reads the file. It runs
in `make check` and CI.

```sh
make licenses     # table, plus build/licenses/ (gitignored): every module's licence texts and THIRD_PARTY_LICENSES.md
```

`build/licenses/` is the Go modules licence bundle for a release. Shipping it with
`backend/web/vendor/licenses/` and `NOTICE` (the JavaScript) is part of the release slug
(v1-24), which the owner runs separately.

## Extism and the binary size

The plugin runtime (v1-16) was chosen after a spike (`extism-hatamoto`: Extism go-sdk on
wazero with standard-Go `wasip1` guests). Measured with Go 1.27, `-ldflags "-s -w"`:

| Binary | Size | Added |
|---|---|---|
| net/http, JSON and slog only | 6.71 MB | |
| plus raw wazero + WASI | 9.00 MB | +2.29 MB |
| plus Extism go-sdk and the runtime | 13.90 MB | +7.19 MB |

Extism adds about 4.9 MB on top of wazero, and about **5 of the 7.2 MB** comes from
modules the server never uses: `dylibso/observe-sdk` (a tracing adapter that Extism links
although nothing sets an `ObserveAdapter`), the OTLP protobufs and `google.golang.org/protobuf`
it drags in, `ianlancetaylor/demangle`, `tetratelabs/wabin` and `gobwas/glob`.

Decision: **accepted for 1.0.** The alternatives are implementing the roughly 20 Extism
kernel imports on top of wazero ourselves (saves about 5 MB, costs a maintained fork of
the ABI) or waiting for an upstream build tag. Neither is worth it for a 14 MB server
binary. An upstream issue asking Extism for an optional observe adapter (a build tag or a
separate module) is **to be filed by a human**; revisit this decision when it lands. wazero
is held at v1.9.0 (see the table): the spike's optional mmap allocator used its
`experimental` API, which can change between minors, so re-run the sandbox tests on a bump.

## Vendored JavaScript and CSS (admin)

The admin ships its browser dependencies as committed files, not from a CDN and not
through Node. They are listed, with version, licence, source URL, SHA-256 and size of
every file, in [`backend/web/vendor/vendor.json`](../backend/web/vendor/vendor.json),
which is the source of truth (htmx, SortableJS and the Tiptap/ProseMirror module
graph, among others). The tool that fetches and checks them is `backend/tools/vendor`:

```sh
make vendor          # cd backend && go run ./tools/vendor fetch  (network; a pinned file that changed upstream fails, add --update to accept it and review the diff)
make vendor-verify   # offline: committed bytes match vendor.json; part of `make check` and CI
```

`make vendor-verify` fails (exit 1) on a changed, missing or extra file. Licences of the
vendored files are in `backend/web/vendor/licenses/`, with a `NOTICE`.

| Group | Files | Version | Licence | Purpose | Update |
|---|---|---|---|---|---|
| htmx | `htmx-2.0.11.min.js` | 2.0.11 | 0BSD | Admin interactions (partial swaps) | change the `@version` in `vendor.json`, `cd backend && go run ./tools/vendor fetch --update`, review the diff |
| SortableJS | `sortable-1.15.7.min.js` | 1.15.7 | MIT | Drag and drop | same |
| Tiptap module graph | `tiptap-3.31.3/` (62 files: Tiptap, ProseMirror, floating-ui, linkifyjs, orderedmap, rope-sequence, w3c-keyname) | 3.31.3 | MIT | Rich-text editor | same, then the editor checklist in `docs/QA.md` |

Licence texts: `backend/web/vendor/licenses/` (22 files, one per package, hashed in
`vendor.json`) and `backend/web/vendor/NOTICE`, both written by the vendor tool.

## Admin icons (Lucide)

The admin icon set is generated Go, not a runtime dependency. `backend/internal/admin/icons_lucide.go`
holds SVG path data for 84 icons taken from Lucide (`lucide-react` 0.331.0, ISC licence). Nothing
from Lucide is imported, bundled or fetched at run time, and Node is not part of the build. To add
an icon, add one line to the name map in `backend/tools/gen-icons/gen-icons.mjs` and run, from
`backend/`:

```sh
node tools/gen-icons/gen-icons.mjs /path/to/node_modules/lucide-react && gofmt -w internal/admin/icons_lucide.go
```

Never hand-edit `icons_lucide.go`. The brand mark is the one hand-drawn glyph (in `icons.go`).
`TestIconsAllNamesPresent` fails if a template or nav entry names an icon that is not in the map.

Licence notice (Lucide, ISC):

> Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as part of Feather (MIT).
> All other copyright (c) for Lucide are held by Lucide Contributors 2022.
>
> Permission to use, copy, modify, and/or distribute this software for any purpose with or without
> fee is hereby granted, provided that the above copyright notice and this permission notice appear
> in all copies.
>
> THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS
> SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
> AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
> WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT,
> NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
> OF THIS SOFTWARE.

Audit (v1-23): npm `latest` of htmx (2.0.11) and SortableJS (1.15.7) is what
is pinned (Chart.js 4.5.1 was retired later: the analytics charts are drawn by the admin's own SVG modules). **Tiptap 3.32.0 is available** (pinned 3.31.3). It was not taken: re-crawling the
module graph changes about 60 files and the editor can only be verified in a browser, which is
the manual QA step before a release.
