# Homepage and README screenshots

Regenerates the 13 images on the hatamoto.dev homepage, and the images in the repository README
(`docs/images/`), from a fictional, seeded local instance ("Northwind Studio"), so they can be rebuilt
after any admin UI change. Dev-only: nothing here is shipped and nothing under `backend/` is touched.

## One command

```
scripts/homepage-shots/run.sh
```

It builds the backend (stamped `v0.26.0`), starts a throwaway Postgres and server, seeds the demo,
captures every shot, and writes the contact sheet. Result in `scripts/homepage-shots/out/`
(not committed):

- 13 WebPs named by slot (`hero.webp`, `agents.webp`, ...), each exactly 2880x1800, light theme.
- `contact-sheet.png`: all shots with their slot class.
- `tiles-500.png`: the six tile slots at the 500 px width they have on the homepage (readability check).

## README images

```
SHOTS_SET=readme scripts/homepage-shots/run.sh
```

Same instance and seed, plus `seed.d/40-readme.sh`, then `readme-shots.json` instead of `shots.json`.
It writes 1600 px wide WebPs (16:10, about 30 to 100 KB each) straight into `docs/images/`, and the contact
sheet to `out/readme-contact-sheet.png`. A failed capture leaves `docs/images/` untouched. Review the diff
and commit the images with the change that moved the UI. What the README set adds:

- The build is stamped with the newest release tag, so Settings > Updates shows a believable version
  (the rolled-back update is the next patch release).
- `seed.d/40-readme.sh` creates an API key and runs `mcp-demo.mjs`: a real MCP session against the
  instance (initialize, list the tools, create a post, publish it, fetch the page). The transcript is
  written as a page that the `mcp` shot captures, so the numbers in it are the server's own.
- The WordPress scan report reads the importer fixture's export (`test/wpfixture`; run
  `test/wpfixture/run-local.sh up` once, or point `README_WXR` at any WXR file), with its local address
  and name replaced by the demo's.
- `theme-grid.mjs` composes `themes.webp` from four theme screenshots in `registry/screenshots/`
  (filled by `registry/build-community-themes.sh`; override with `THEME_SHOTS`). Without them the old
  `themes.webp` stays.
- Entries can reuse a homepage shot as it is (`{"from": "<slot>", "slot": ..., "file": ...}`); the set
  file's `host` replaces `hatamoto.dev` in the URL pill and `width` sets the output width.

## Prerequisites

Go, Node 20+, Postgres 17 or 18 binaries (`initdb`, `pg_ctl`, `psql`), Google Chrome at
`/usr/bin/google-chrome`, and the gtm plugin from a sibling `hatamoto-first-party` checkout (`../hatamoto-first-party/plugins/gtm`).
`npm ci` runs on first use. The gtm package is built into a temporary copy and signed with a key
generated for the run (or reused from `SHOTS_CACHE` if set); the server trusts only that key (`HATAMOTO_TRUSTED_KEYS`). No real key is read.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `OUT_DIR` | `scripts/homepage-shots/out` | where the images go |
| `QA_VERSION` | `v0.26.0` | version stamped into the build (Settings > Updates leaves the dev-build state) |
| `CHROME` | `/usr/bin/google-chrome` | browser |
| `PG_BIN` | newest `/usr/lib/postgresql/*/bin` | Postgres binaries |
| `GTM_REPO` | `../hatamoto-first-party/plugins/gtm` (in a sibling checkout) | plugin source |
| `ONLY` | all | comma separated slots to capture |
| `HATAMOTO_BIN` | build from source | a prebuilt binary (must be stamped with `QA_VERSION`) |
| `SHOTS_CACHE` | none | directory that caches the demo signing key (`demo.key`, `demo.pub`) and the built `gtm.hmp` between runs, so reruns skip the plugin build. Dev only: it is a throwaway demo key trusted only by the local throwaway server, never put a real key there. Must be outside the repo (run.sh refuses a path inside it) |
| `SHOTS_SERVE` | off | `1` = seed, print the address and wait (no capture), for iterating on seeds |
| `SHOTS_SET` | `homepage` | `readme` = the README images (see above); `OUT_DIR` then defaults to `docs/images` and `QA_VERSION` to the newest release tag |
| `README_WXR` | the wpfixture export in `~/.cache/hatamoto-wpfixture` | WordPress export for the README import report |
| `THEME_SHOTS` | `registry/screenshots` | four theme screenshots for `themes.webp` |

## How a shot is made

`shots.json` has one entry per slot: `url` (tokens `{entity:Title}`, `{type:Name}`, `{menu:Name}`,
`{form:Name}` are resolved from the admin lists), `viewport`, `dpr`, an optional `region` (CSS selector, with
`regionHeight` and `pad` to crop the top part of a tall card), `actions` (`click`, `fill`, `press`, `check`,
`setInputFiles`, `submit`, `waitFor`, `blur`, `eval`, `wait`), `pre` (a shell command run before the page loads) and the `alt` text
for the homepage. Optional: `css` (a style sheet added to the page), `hide` (selectors to hide), `keepBanner`,
`theme` (`dark` renders the admin's dark theme and a dark window bar), `html` (capture a local HTML file instead
of a URL), `bg: "corner"` (letterbox a region in its own on-screen colour) and `settle` (extra wait, ms).
`$NAME` in a file path is read from the environment. `capture.mjs` signs in, aliases the admin font tokens to Geist (the admin itself ships Inter),
waits for fonts and charts, refuses a page that contains `qa.test` or `QA `, crops, and `frame.mjs` adds the
window bar (three dots, URL pill) and a hairline border, then encodes WebP with `sharp`. There is no baked
shadow: the homepage slot CSS already adds radius and shadow.

The updates shot needs the "rolled back" state, which the server's own update check can overwrite, so
`pre` runs `seed.d/reassert-updates.sh` right before it. The API key and plugin shots only fill in or
open forms; the key form is never submitted and the plugin is never installed.

To add a shot: add an entry to `shots.json` and, if it needs data, a step in `seed.d/`. Seeds are numbered
shell files sourced by `seed.sh`; run one alone against a live instance with `SEED_ONLY=20 scripts/homepage-shots/seed.sh`.

## Publishing

Publishing to the live homepage (uploading the WebPs and updating the page document) is a separate,
manual step with its own snapshot and rollback; see the homepage-shots plan.
