# Hatamoto CMS

Hatamoto is an open-source CMS that ships as one Go binary plus Postgres. Download one file, point it
at Postgres and, in under ten minutes, you have a fast server-rendered site with an admin, your
WordPress content imported with every old URL still working, and AI-crawler analytics out of the box.
No Node, no Redis, no telemetry.

> **Status: pre-1.0, plugin SDK v0.** It is used in production on [hatamoto.dev](https://hatamoto.dev),
> but the SDK and some APIs can still change between minor versions. See the [roadmap](docs/ROADMAP.md).
> What stays stable and how changes are announced: [STABILITY.md](STABILITY.md).

<!-- OWNER: screenshot 1 of 3, the page editor. Add the image to docs/images/ and replace this comment with:
![The page editor](docs/images/page-editor.png) -->
<!-- OWNER: screenshot 2 of 3, the AI-crawler analytics dashboard:
![AI crawler analytics](docs/images/ai-analytics.png) -->
<!-- OWNER: screenshot 3 of 3, the WordPress import report:
![WordPress import report](docs/images/wordpress-import.png) -->

## Features

- A fast, server-rendered website with a good default theme, a blog and pages.
- An admin with a proper **blog editor** and a visual **page editor**.
- A **WordPress import** that keeps every old URL working, with a URL audit.
- **AI-crawler analytics** (which AI read which page, and did it send visitors), `llms.txt`,
  JSON-LD and a knowledge graph.
- One-click, sandboxed **plugins** and themes (WebAssembly), with no compiling.
- An **MCP server**, so AI agents can write and publish through the CMS.
- Signed release binaries with signed, automatic updates you control, and a Docker image (from the first public release).
- [Apache-2.0](LICENSE).

## Links

- Docs: <https://hatamoto.dev/docs> (sources in [docs/site](docs/site/index.md))
- Plugins: <https://hatamoto.dev/plugins>, [hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins)
  and the [plugin guide](docs/PLUGINS_WRITE_AND_SUBMIT.md)
- Security: [SECURITY.md](SECURITY.md) (report problems privately)
- Contribute: [CONTRIBUTING.md](CONTRIBUTING.md), [Code of Conduct](CODE_OF_CONDUCT.md)
<!-- OWNER: when the public demo is live, add: - Demo: <https://demo.hatamoto.dev> -->

## Outbound connections

No telemetry, no usage reports. These are the main connections the server makes; the full list, with the update download,
registry package downloads and code references, is [Outbound connections](docs/site/operate/outbound-connections.md).

| Connection | When | Sends | Turn off |
| --- | --- | --- | --- |
| Update check (`hatamoto.dev/releases`) | About every six hours | One anonymous GET for a signed file; nothing about your site | `HATAMOTO_UPDATES=off` (`notify` still checks, it only stops installs) |
| Plugin and theme registry (`hatamoto.dev/registry.json`) | Only when an admin browses or installs | Anonymous GET | `HATAMOTO_REGISTRY_URL=off` |
| AI-crawler IP ranges (nine operators, listed in `backend/internal/agents/registry.json`) | Daily, to verify that a hit claiming to be a crawler is one | GET with a `hatamoto-agent-verifier` user agent | `HATAMOTO_AGENT_RANGES=off` |
| Reverse DNS | To verify crawler hits that are checked by host name | A lookup of the visitor's address | `WORKER_DISABLE_JOBS=agent_verify` (crawler hits stay unverified) |
| Captcha (Cloudflare Turnstile, reCAPTCHA, hCaptcha) | A form submit, only if you switch it on | Secret key, token, visitor IP | Do not enable it |
| WordPress import | Only during an import you start | Requests to the source site, every media URL it lists and the `--audit-url` site | Do not run one |
| SMTP, webhooks, plugins' `http.fetch` | Only what you configure or grant | The message, event or plugin request | Leave unconfigured or ungranted |

Embedded videos and maps are loaded by the visitor's browser, not by your server.

## Quickstart

**Download the binary** for your platform from the
[GitHub releases](https://github.com/Zen-Labs-Corp/hatamoto/releases) (from the first public release, each release has a
`hatamoto-<os>-<arch>.tar.gz` bundle with the binary and the licences; the binary sits in the
`hatamoto-<os>-<arch>/` folder inside it). Linux or macOS,
Intel or ARM:

```sh
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fL "https://github.com/Zen-Labs-Corp/hatamoto/releases/latest/download/hatamoto-$OS-$ARCH.tar.gz" | tar -xz --strip-components=1 "hatamoto-$OS-$ARCH/hatamoto"
./hatamoto version
```

How releases are signed: [Trust and signing](docs/site/updates/trust-and-signing.md). Then point it at Postgres and run it:

```sh
DATABASE_URL=postgres://user:pw@localhost:5432/hatamoto?sslmode=disable ./hatamoto serve
```

You do not need Go to run a release. If you build from a clone instead
(`cd backend && go build -o hatamoto ./cmd/hatamoto`), the `toolchain` line in
`backend/go.mod` makes an older Go download the pinned toolchain on first build.

**Or with Docker**, which starts Postgres too:

```sh
git clone https://github.com/Zen-Labs-Corp/hatamoto.git hatamoto
cd hatamoto
docker compose up
```

That builds the image from the `Dockerfile` in the clone. A signed, multi-arch image,
`ghcr.io/zen-labs-corp/hatamoto`, is published for each release, after it is signed and published, starting at the first public
release (`ghcr.io/zen-labs-corp/hatamoto:vX.Y.Z`); until then build it as above. Behind a reverse proxy in Docker, set
`HATAMOTO_TRUSTED_PROXIES` to the bridge gateway; see the
[configuration reference](docs/site/operate/configuration-reference.md#trusted-proxies) and
[Install with Docker](docs/site/getting-started/install-docker.md).

Then open **http://localhost:8090/admin**. The first-run wizard asks for a setup
code; it is printed in the server logs (`docker compose logs hatamoto`), or set
`HATAMOTO_SETUP_CODE` yourself. The wizard creates your account, names the site
and picks a theme. The site is at **http://localhost:8090/**.

The compose file starts two services: `db` (Postgres 16, healthchecked) and
`hatamoto`, the single Go binary. On first boot it generates and persists a strong
server secret (an env value always wins), applies its embedded migrations under
a Postgres advisory lock, creates the site config and a starter home page, then
serves the site, the admin, the API and the background worker, all on one port.

Prefer the command line? Create a user instead of using the wizard:

```sh
docker compose exec hatamoto hatamoto user create --email you@example.com
```

It prints a temporary password (or pass `--password-stdin`). Forgot it later?
`hatamoto user reset-password --email you@example.com` sets a new one, unlocks the
account and signs it out everywhere.

Restarting the stack reuses the persisted secret (in the `secrets` volume at
`/data/secrets`), so existing sessions remain valid. An older stack keeps its secret
across `git pull && docker compose up -d`: the compose file still mounts that volume
where it always was (see [docs/UPGRADES.md](docs/UPGRADES.md)).

## Production hardening

The `docker-compose.yml` defaults are for local evaluation/demo use only.
For a real deployment:

- Set `ENVIRONMENT=production`. This enables `Strict-Transport-Security` on responses — only do this behind
  HTTPS (terminate TLS in front of `hatamoto`; this repo's Docker setup
  does not include TLS termination). Any reverse proxy works: it only needs to
  forward everything to the one port (see `deploy/caddy/hatamoto.dev.caddy`).
- Set `CSP_ENFORCE=true` only after verifying the public pages render
  correctly under the enforcing `Content-Security-Policy` (check the browser
  devtools console for violations first; the default ships
  `Content-Security-Policy-Report-Only`, which never blocks anything).
  Expect `connect-src` and `img-src` in the shipped policy to need tuning to
  your deployment's actual media origins (the default policy assumes
  same-origin `'self'`, which breaks a CDN setup). The admin always sends its
  own strict policy.
- The production API (`ENVIRONMENT=production`) sends
  `Strict-Transport-Security: max-age=31536000; includeSubDomains` on every
  response. Browsers cache this for a year and it applies to **every**
  subdomain of the response's host. Confirm no subdomain is served over
  plain HTTP before enabling `ENVIRONMENT=production` — an HSTS-pinned
  subdomain that's HTTP-only becomes unreachable for anyone who has visited,
  for up to a year, with no quick way to undo it.
- Provide your own `HATAMOTO_SECRET` (at least 32 characters) via your secret
  manager rather than relying on the auto-generated one persisted in
  `HATAMOTO_SECRETS_DIR`, which is intended for the local/demo path. `JWT_SECRET`
  is the old name of the same setting and still works; `HATAMOTO_SECRET` wins
  if both are set. Keep it stable: it keys the flash cookie, theme previews and
  stored import credentials.
- Configure real SMTP credentials (`SMTP_HOST`/`SMTP_USERNAME`/
  `SMTP_PASSWORD`) — the compose defaults leave email disabled, and form
  notification and confirmation emails are then silently skipped (the API logs
  a warning at boot in production).
- Plugin cookies use `ADMIN_COOKIE_SECURE` (default on in production). It
  replaces `LEAD_COOKIE_SECURE`; set it explicitly if you relied on that variable.

### Follow-ups (not in this release)

- Demo content seeding beyond the starter home page.
- Nonce-based CSP (removing `'unsafe-inline'` from `script-src`/
  `style-src`) once Report-Only telemetry validates the tighter policy.

## The `hatamoto` binary

One binary does everything:

| Command | What it does |
| --- | --- |
| `hatamoto serve` | Migrates the database, then serves the site, API and worker (`HATAMOTO_WORKER=off` disables the worker, for multi-replica setups where only one replica should run it). Secrets are generated and kept in `$HATAMOTO_SECRETS_DIR` (default `./data/secrets`) unless set in the environment. |
| `hatamoto supervise` | The process to run as the service (systemd `ExecStart`, container `CMD`). Runs `serve` as a child, keeps releases in `$HATAMOTO_DATA/releases` (default `./data`), switches to a newly applied release under a health gate and rolls back on its own (a release applied while it was down is gated at its next start). Nothing is fetched from the network. |
| `hatamoto update apply --file F --record R [--sig S]` / `update rollback [--force]` | Install a signed release from local files, and go back to the previous one. Run as the service user, never root. Both tell a running supervisor to switch; see [docs/UPGRADES.md](docs/UPGRADES.md). |
| `hatamoto backup create` / `backup list [--json]` / `backup delete <id>` | Full backups (database, plugin schemas, content) under `$HATAMOTO_DATA/backups`; see [docs/BACKUPS.md](docs/BACKUPS.md). |
| `hatamoto migrate` / `migrate status` | Apply pending migrations / list them. |
| `hatamoto user create --email E [--name N] [--role owner\|editor] [--password-stdin]` | Create a user. |
| `hatamoto user reset-password --email E [--password-stdin]` | Set a new password. |
| `hatamoto routes`, `plugins list`, `version` | Inspect the binary and the plugins in `HATAMOTO_PLUGIN_DIR` (development). |
| `hatamoto keys generate`, `theme pack`, `plugin pack`, `theme dev`, `package install`, `registry index`, `registry page` (optional static directory page) | Build, sign and install packages ([docs/PACKAGES.md](docs/PACKAGES.md)). |

```sh
docker compose up db
cd backend && go build -o hatamoto ./cmd/hatamoto
DATABASE_URL=postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable \
./hatamoto serve
```

`DATABASE_URL` is the only required variable. `serve` generates and keeps its own
server secret, and everything else has a neutral default (listed below) that you
change in the admin once you are signed in.

Browse `http://localhost:8090/`. The binary serves pages, `/robots.txt`,
`/sitemap.xml`, `/llms.txt`, theme assets and the no-JS form post, next to the
JSON API (`/api`, `/media`, `/health` keep precedence). Go rendering is always
on. The admin (`/admin`) is server-rendered by the same binary: there is no
separate frontend and no Node anywhere (ADR 0002).
Migrations are embedded; see [`backend/migrations/README.md`](backend/migrations/README.md).

| Variable | Effect |
| --- | --- |
| `DATABASE_URL` | **Required.** PostgreSQL connection string. |
| `API_HOST` / `API_PORT` | Listen address (default `0.0.0.0:8090`). |
| `ENVIRONMENT` | `production` switches to JSON logs and HSTS. Admin cookies are `Secure` when the request arrives over TLS (or a local proxy sets `X-Forwarded-Proto: https`), so a first run over plain HTTP still signs in. |
| `PUBLIC_BASE_URL` | Pins the public origin used in canonicals, sitemaps and emailed links. Reset and invitation emails are only sent when this or the Site address setting is set. |
| `HATAMOTO_PAGE_CACHE_TTL` | How long rendered public pages stay in the HTML cache (Go duration, default `5m`, range `30s` to `720h`, i.e. 30 days; invalid values fall back to the default with a boot warning). Content, menu, settings, theme and plugin changes clear the cache at once in the process that made them. Page and menu writes from another process (the CLI, another replica, the worker) clear it within about 3 seconds (1 outside production), see [`docs/THEMES.md`](docs/THEMES.md#page-cache); other changes made elsewhere (settings, terms, media, forms, schemas, themes, plugins) show on other replicas when the TTL ends; plugin-rendered data lives for the TTL. See [`docs/THEMES.md`](docs/THEMES.md#page-cache). The cache needs `PUBLIC_BASE_URL`. A lifetime saved in Admin > Settings > Performance wins over this value. |
| `HATAMOTO_TRUSTED_PROXIES` | Comma-separated CIDRs whose `X-Forwarded-*` headers are believed (default: loopback only; Docker deployments set the bridge gateway, see the reverse-proxy docs). |
| `HATAMOTO_SITE_NAME` | Seeds the site name on first boot (default `My Hatamoto site`). Once set in the admin, the stored name wins. |
| `HATAMOTO_CANONICAL_SLUG` | Opaque site identifier stamped into API responses (default `site`). |
| `SMTP_HOST` `SMTP_PORT` `SMTP_USERNAME` `SMTP_PASSWORD` | Outgoing mail. Email is off while `SMTP_HOST` is empty or `localhost`; password reset by email then explains the CLI instead. |
| `SMTP_FROM` / `SMTP_FROM_NAME` | Sender. `SMTP_FROM` is required once `SMTP_HOST` is set; the name defaults to the current site name. |
| `HATAMOTO_SECRET` | Server secret, at least 32 characters (flash cookie, theme previews, stored import credentials). Generated and persisted when unset. `JWT_SECRET` is accepted as its old name. |
| `HATAMOTO_SETUP_CODE` | The first-run wizard's code (at least 12 characters); a random one is logged each boot when unset. |
| `HATAMOTO_ADMIN_DIR` | Dev only: load admin templates, CSS and JS from disk and rebuild per request. |
| `HATAMOTO_SECRETS_DIR` | Where the generated server secret is persisted (default `./data/secrets`). |
| `HATAMOTO_UPDATES` | `auto` (default), `notify` (look, never install) or `off` (no network call at all). See [docs/UPGRADES.md](docs/UPGRADES.md#automatic-updates-self-hosters). |
| `HATAMOTO_UPDATE_CHANNEL` | `stable` (default), `edge` or `canary`. |
| `HATAMOTO_REGISTRY_URL` | Signed plugin and theme registry (default `https://hatamoto.dev/registry.json`; `off` disables). |
| `HATAMOTO_TRUSTED_KEYS` | Base64 ed25519 keys of the signers whose packages and registry are trusted ([docs/PACKAGES.md](docs/PACKAGES.md)). |
| `HATAMOTO_TRUST_FIRST_PARTY` | `false` stops trusting the built-in first-party signing key. |
| `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` | Outbound requests one plugin may make per minute (default 600). |
| `HATAMOTO_PLUGIN_DIR` | Development override: load plugins from a directory (unset by default). |

Themes and plugins are installed packages (stored in Postgres), not directories:
see [docs/THEMES.md](docs/THEMES.md) and [docs/PACKAGES.md](docs/PACKAGES.md).
`HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` were removed and stop the boot
if set.

## One site, one language

An install is one site in one language. The site setting `language` (a BCP 47
tag, default `en`) feeds `<html lang>`, the JSON-LD `inLanguage` and the feeds;
the public contact details (`phone`, `business_hours`, `address`) live next to
it in the same settings (`GET/PATCH /api/v1/settings/site`, owner only) and
appear in the Organization JSON-LD. URLs carry no language or region segment:
pages live at `/{slug}`, posts at `/blog/{slug}` (see
[docs/URL_PATTERNS.md](docs/URL_PATTERNS.md)). A site imported from WordPress
can switch on the built-in WordPress redirects with `legacy_wp_redirects`
(default off; see [docs/REDIRECTS.md](docs/REDIRECTS.md)). Sites that once had
region prefixes get a 301 from every old `/xx/...` URL to its new path.

## Development

Go is the only toolchain (`go build`, `go test`; no Node). See `Makefile`: `make build`
builds `backend/bin/hatamoto`, `make check` runs the repository guards
(neutrality, Caddy splice test, vendored-asset hashes). Run the tests from
`backend/` with `go test ./...`; add `-tags=integration` and `DATABASE_URL`
for the Postgres-backed ones. Editing the admin templates, CSS and JS: set
`HATAMOTO_ADMIN_DIR=backend/web/admin` to reload them from disk. The
JavaScript editors (Tiptap, canvas host) are tested by hand: [docs/QA.md](docs/QA.md).

Contributions are welcome: read [CONTRIBUTING.md](CONTRIBUTING.md) (build and test
commands, the admin UI rules, DCO sign-off) and the [Code of Conduct](CODE_OF_CONDUCT.md).
Report security problems privately as described in [SECURITY.md](SECURITY.md).

## Plugins

Plugins are sandboxed WASM modules (Extism on wazero), installed as signed
packages from Admin > Settings > Plugins (registry or upload) without a restart;
there is nothing to compile into the binary. The contract is `backend/pkg/sdk` (published as `hatamoto.dev/sdk`)
and [`docs/PLUGIN_API.md`](docs/PLUGIN_API.md). To write a plugin in your own repo and
submit it for the registry, see [`docs/PLUGINS_WRITE_AND_SUBMIT.md`](docs/PLUGINS_WRITE_AND_SUBMIT.md);
approved plugins are listed at <https://hatamoto.dev/plugins> (served by the app itself), and their sources live in [hatamoto-plugins](https://github.com/Zen-Labs-Corp/hatamoto-plugins). Themes are data, not code:
to write one and submit it for the registry, see [`docs/THEMES_WRITE_AND_SUBMIT.md`](docs/THEMES_WRITE_AND_SUBMIT.md);
approved themes are listed at <https://hatamoto.dev/themes>. The first-party plugins
([Google Tag Manager](https://github.com/Zen-Labs-Corp/hatamoto-plugin-gtm) and
[Google Search Console](https://github.com/Zen-Labs-Corp/hatamoto-plugin-searchconsole)) and the
[SDK](https://github.com/Zen-Labs-Corp/hatamoto-sdk) live in their own public repositories. Try the reference plugin:

```sh
make plugin-hello
hatamoto plugin pack examples/plugins/hello/build                       # unsigned: allow it in Admin > Plugins first
hatamoto package install hello-*.hmp                                    # or upload it in the admin
# development shortcut, no packaging: HATAMOTO_PLUGIN_DIR=./data/plugins with ./data/plugins/hello/{plugin.json,plugin.wasm}
```


## Licence

Hatamoto is open source under the [Apache License 2.0](LICENSE). You may use,
modify, self-host and offer hosting for it, commercially or not. Copyright
Zen Labs Corporation; see [NOTICE](NOTICE) for third-party components.

"Hatamoto" and the Hatamoto logo are trademarks of Zen Labs Corporation. The
licence covers the code, not the name: forks must use a different name, and
hosting providers may say "hosting for Hatamoto" but not imply they are
official. See [TRADEMARKS.md](TRADEMARKS.md).
