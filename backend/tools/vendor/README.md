# tools/vendor

`make vendor` / `make vendor-verify` (see the root Makefile). Stdlib only: no
Node, no npm. The manifest is `backend/web/vendor/vendor.json`; the committed
bytes are the source of truth, URLs are provenance.

* `fetch`: downloads over HTTPS only, 30 s timeout, 5 MiB cap, no redirects to
  another host, atomic writes. A pinned entry whose upstream bytes changed fails
  unless `--update` is given.
* `verify` (offline, run by `make check`): every listed file exists with the
  listed sha256, every URL carries an exact `@x.y.z`, nothing else lives in
  `web/vendor/` (except `NOTICE` and `src/`).

## Tiptap spike (v1-10 C1): outcome

The design's preferred option (esm.sh `POST /build`, one self-contained bundle)
is **not available**: esm.sh answers "The `/build` API has been deprecated".
`?bundle` / `?bundle=all` per package would ship one copy of `@tiptap/core` and
ProseMirror per extension (the keyed-plugin duplicate-instance failure), so it
was rejected too.

Landed: the design's fallback 2, a **crawled module graph** (kind
`module-graph`). The tool starts from the pinned roots (core, starter-kit,
image, link, placeholder, table*, extensions, `@tiptap/pm/*`), follows every
static import, collapses esm.sh's `?target=es2022` re-export shims, stores each
module once under `web/vendor/tiptap-<version>/` (55 files, ~0.5 MB) and
rewrites origin-absolute imports to relative ones. Result: exactly one
`prosemirror-state` and one `@tiptap/core`. The import map exposes the roots as
bare specifiers (`@tiptap/starter-kit`, `@tiptap/pm/state`, ...). Verified by
importing starter-kit and extension-table in Node 22 (single module graph, no
unresolved specifiers). In-browser behaviour is exercised in v1-12/13.

## Tiptap graph contents

Roots: core, starter-kit, image, link (StarterKit's own module, aliased so
`EntityLink` can extend `Link`; no second copy), placeholder, table*,
extensions, suggestion, extension-bubble-menu and the `@tiptap/pm/*`
entry points. `suggestion` and the bubble menu bring floating-ui
(`@floating-ui/core|dom|utils`, licences in `licenses/`). 62 files,
560,546 bytes raw (193 KiB gzipped file by file). `internal/admin` tests assert
a single `@tiptap/core` and a single `prosemirror-*` copy in the tree.
