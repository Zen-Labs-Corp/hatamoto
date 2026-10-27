# Caddy: a plain reverse proxy to the one Go binary

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

The desired `hatamoto.dev` and `www.hatamoto.dev` site blocks are tracked in
[`deploy/caddy/hatamoto.dev.caddy`](../../deploy/caddy/hatamoto.dev.caddy).
`scripts/ship.sh` does not deploy Caddy; it is a manual step after the Go build
is live.

Everything (pages, `<admin>`, `/api/*`, `/media/*`, `/health`, `robots.txt`, sitemaps,
`llms.txt`, `/theme/assets/*`, site icons, `/sitemap.xsl`, `/forms/*`) goes to Go
on :8090. The old Next sign-in URLs (`/auth/*`) are redirected to `<admin>/login`
by Go itself. Caddy keeps only what a proxy should own: TLS, the security
headers, `encode gzip` and the `www` to apex redirect. It also serves static files
itself: `/releases/**`, the package registry (`/registry.json`, `/packages/*`) and
and the append-only `/archive/*`, the last two from
`/opt/hatamoto/registry` (see [`registry/README.md`](../../registry/README.md)).
`/plugins` and `/themes` are ordinary pages proxied to the app (the "Plugin directory"
and "Theme directory" sections). Only `/screenshots/<name>.(png|webp|jpg)` is static, from
the same root, with the same hardening headers as `/archive`; any other name under
`/screenshots/` falls through to the app.

## Apply

```bash
sudo deploy/apply-caddy.sh                  # /etc/caddy/Caddyfile
DRY_RUN=1 deploy/apply-caddy.sh ./Caddyfile # validate and show the diff only
```

The script finds the blocks with a brace-depth parser (trailing `} # comments`,
CRLF, braces in strings/comments, placeholders, nested handles, snippets and
imports are handled) and matches the site address exactly (`hatamoto.dev`,
`www.hatamoto.dev`). It refuses when a hatamoto address shares a line with other
addresses or the braces do not balance. It then validates the result with
`caddy validate` (next to the real file, so relative imports resolve), checks
with `caddy adapt` that every other site is unchanged (needs `jq`), backs up to
`<Caddyfile>.bak` (first original) and `<Caddyfile>.bak.<timestamp>`, reloads
Caddy and, if the reload fails, restores the backup and reloads again.
`deploy/test-apply-caddy.sh` tests the splice against real `caddy adapt`
(`make check` runs it; needs `caddy` and `jq`).

## Site icons

Go serves `/favicon.ico`, `/favicon-16.png`, `/favicon-32.png`,
`/apple-icon.png`, `/icon-192.png` and `/icon-512.png` from the active theme's
`assets/icons/` (the embedded default set unless the theme ships its own) with a
30 day cache.
