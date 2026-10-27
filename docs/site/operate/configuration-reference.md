---
title: Configuration reference
nav_title: Configuration reference
description: Every environment variable Hatamoto reads, with its default and meaning, plus the files, headers and email behaviour they control.
---

This page lists every environment variable the server and the command-line tool read, grouped by area, and then the behaviour those settings control: the files Hatamoto writes, the security headers it sends, how it trusts a proxy and how it sends email. To set the variables on a real server, follow [Deploy with systemd](deploy-systemd.md) or [Deploy with Docker](deploy-docker.md); to lock an instance down, follow [Harden an instance](hardening.md).

## Overview

Only `DATABASE_URL` is required. Everything else has a default that lets `hatamoto serve` boot, and most site settings (site name, language, performance, update policy) are then changed in the admin. A minimal production environment file:

```bash
DATABASE_URL=postgres://hatamoto:CHANGE-ME@127.0.0.1:5432/hatamoto?sslmode=disable
ENVIRONMENT=production
PUBLIC_BASE_URL=https://example.com
HATAMOTO_SECRET=replace-with-at-least-32-random-characters
HATAMOTO_TRUSTED_PROXIES=127.0.0.1
MEDIA_UPLOAD_DIR=/var/lib/hatamoto/uploads
HATAMOTO_DATA=/var/lib/hatamoto/data
SMTP_HOST=smtp.example.com
SMTP_FROM=noreply@example.com
SMTP_USERNAME=hatamoto
SMTP_PASSWORD=change-me
HATAMOTO_UPDATES=notify
```

## How values are read

- **Strings** are used as given. An empty variable counts as unset and the default applies.
- **Booleans** accept what Go's `strconv.ParseBool` accepts: `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, `False`. Anything else falls back to the default without a message. A few switches are stricter and are marked as such.
- **Integers** must parse as a whole number, otherwise the default applies. `API_PORT` is the exception: an invalid port stops the boot.
- **Durations** use Go syntax such as `30s`, `5m` or `1h`. An invalid value falls back to the default.
- Values are read once at boot (a few at each command run). Restart the service after changing them.

Under systemd put them in the environment file; under Docker use `environment:` or `env_file:`.

## General

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | none (required) | Postgres connection string, for example `postgres://user:pw@db:5432/hatamoto?sslmode=disable`. |
| `API_HOST` | `0.0.0.0` | Address the HTTP server listens on. |
| `API_PORT` | `8090` | Listen port (integer). Also used by `hatamoto healthcheck`. |
| `ENVIRONMENT` | `development` | `production` switches logs to JSON, sends HSTS, and makes `ADMIN_COOKIE_SECURE` default to on. Any other value behaves as development. |
| `LOG_LEVEL` | `info` | Set to `debug` for verbose logs. The logger only tells `debug` apart from everything else. |
| `PUBLIC_BASE_URL` | empty | Public origin (for example `https://example.com`) used in canonical links, sitemaps, JSON-LD, `llms.txt` and emailed links. The page cache needs this variable itself; the Site address setting does not turn it on. Emailed reset and invitation links use this variable or, when it is empty, the Site address setting. |
| `HATAMOTO_SITE_NAME` | `My Hatamoto site` | Seeds the site name at first boot, and is the fallback sender name. Once the name is set in the admin, the stored name wins. |
| `HATAMOTO_CANONICAL_SLUG` | `site` | Opaque site identifier stamped into API responses. |
| `PUBLIC_API_PREFIX` | empty | An extra path where the public forms and page-view endpoints are also served, so the theme script calls it instead of `/api/v1/public`. Lower-case `a-z`, `0-9`, `-` and `_`, at least two segments (for example `/x/v1`), no trailing slash, and the first segment must not be one the server owns (`admin`, `api`, `p`, `media`, `theme`, `health`, `ready`, `forms`, `auth`, `mcp`, `plugin-ui`, `uploads`, or a feed, sitemap, `robots.txt` or `llms.txt` file). An invalid value stops the server at boot. `/api/v1/public` stays mounted either way. A page at the same path is shadowed by the API. |
| `HATAMOTO_PAGE_CACHE_TTL` | `5m` | How long rendered public pages stay in the HTML cache. Go duration from `30s` to `720h`; an invalid or out-of-range value falls back to the default and logs a warning at boot. A lifetime saved in **Settings > Performance** wins. |
| `HATAMOTO_WORKER` | on | The in-process scheduler runs unless this is `off` (case-insensitive). |
| `WORKER_DISABLE_JOBS` | empty | Comma-separated scheduler jobs to skip. The job names are listed in [Multiple replicas and health checks](multi-replica-and-health.md). |
| `WORKER_DRAIN` | unset | `1`, `true`, `yes` or `on` (case-insensitive) stops the worker loops from starting. For maintenance windows. |
| `REPORT_OUTPUT_DIR` | `./reports` | Read into the configuration but not used by any current feature. Leave it alone. |

## Data, secrets and files

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_DATA` | `./data` (the Docker image sets `/data`) | The data directory. Made absolute against the working directory at start. Its contents are listed under [Files Hatamoto writes](#files-hatamoto-writes). |
| `HATAMOTO_SECRETS_DIR` | `./data/secrets` (the image sets `/data/secrets`) | Where the generated server secret is kept. Not used when the secret is set in the environment. Read by `serve` only. |
| `HATAMOTO_SECRET` | generated and kept | Server secret, at least 32 characters. See [The server secret](#the-server-secret). |
| `JWT_SECRET` | unset | Old name of `HATAMOTO_SECRET`, still accepted. `HATAMOTO_SECRET` wins if both are set. |
| `MEDIA_UPLOAD_DIR` | `./uploads` (`/app/uploads` in the image) | Directory for uploaded media files. It may itself be a symlink (it is resolved once at start). Symlinks **inside** it that lead elsewhere (for example `uploads/<folder> -> /mnt/disk`) are refused: uploads fail with an error in the log and existing files there return 403. To keep media on another disk, point `MEDIA_UPLOAD_DIR` at that disk. |
| `MEDIA_BASE_URL` | `/media` | URL prefix under which media is served. |
| `MEDIA_MAX_SIZE_MB` | `10` | Largest accepted upload, in megabytes (integer). |

## Database pool

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_MAX_CONNECTIONS` | `25` | Maximum connections in the pool (integer). |
| `DATABASE_MIN_CONNECTIONS` | `5` | Connections kept open (integer). |
| `DATABASE_MAX_CONN_LIFETIME` | `1h` | Longest age of a connection before it is recycled (duration). |
| `DATABASE_MAX_CONN_IDLE_TIME` | `30m` | How long an idle connection may stay open (duration). |

With several processes, multiply the maximum by the number of processes and check it against Postgres's `max_connections`.

## HTTP server

| Variable | Default | Meaning |
|---|---|---|
| `SERVER_READ_TIMEOUT` | `30s` | HTTP read timeout (duration). |
| `SERVER_WRITE_TIMEOUT` | `30s` | HTTP write timeout (duration). |
| `SERVER_IDLE_TIMEOUT` | `120s` | Keep-alive idle timeout (duration). |
| `HATAMOTO_TRUSTED_PROXIES` | loopback only | Comma-separated CIDRs (or bare IPs) whose `X-Forwarded-*` headers are believed. See [Trusted proxies](#trusted-proxies). |

## Security

| Variable | Default | Meaning |
|---|---|---|
| `CSP_ENFORCE` | `false` | `true` sends the Content Security Policy as an enforcing header. Otherwise it is sent as `Content-Security-Policy-Report-Only`. |
| `SECURITY_HEADERS_DISABLED` | `false` | `true` turns off all the security headers. For local debugging only, never in production. |
| `ADMIN_COOKIE_SECURE` | `true` when `ENVIRONMENT=production`, else `false` | Forces the `Secure` flag on admin cookies. Requests that arrive over HTTPS get it regardless. |
| `ENABLE_COOKIE_AUTH` | `false` | Lets the JSON API session also travel as an HttpOnly cookie with CSRF checks. It is its own session with its own cookie: the admin sign-in cookie never authenticates the API. The admin does not depend on it. |
| `HATAMOTO_SETUP_CODE` | random per boot | Code the first-run wizard asks for. Must be at least 12 characters and not a repeated character or pattern (such as `aaaaaaaaaaaa` or `abcabcabcabc`), after case, spaces and dashes are removed. While no users exist a weaker code stops the boot; once the site has users the code is unused and a weak one only logs a warning. When unset, a random code is logged at warn level at every boot while no users exist. |
| `HATAMOTO_EMBED_HOSTS` | empty | Host names allowed for `iframe` embeds in rich text. See [Embeds in rich text](#embeds-in-rich-text). |
| `HTML_SECTION_TYPE_ALIASES` | empty | Comma-separated extra section types whose body is treated as HTML and sanitised, like the `html` type. |
| `HATAMOTO_MCP_ALLOWED_ORIGINS` | empty | Comma-separated browser origins, besides the public origin, that may call `/mcp`. Requests without an `Origin` header are unaffected. |

## Email

These variables are used only while nothing is saved in **Settings > Email** in the admin. Saved settings replace them as a whole. See [Email delivery](#email-delivery).

| Variable | Default | Meaning |
|---|---|---|
| `SMTP_HOST` | `localhost` | SMTP host. Email is off while it is empty or `localhost`. |
| `SMTP_PORT` | `587` | SMTP port (integer). These variables always use STARTTLS; for TLS (SSL) on port 465, use **Settings > Email**. |
| `SMTP_USERNAME` | empty | SMTP login user. Authentication is used only when both user and password are set. |
| `SMTP_PASSWORD` | empty | SMTP login password. |
| `SMTP_FROM` | `noreply@localhost` | Sender address. The boot fails if `SMTP_HOST` is a real host and this is still the default. |
| `SMTP_FROM_NAME` | the site name | Display name in the From header. Follows the site name stored in the database unless you set it. |
| `EMAIL_DISABLED` | unset | The exact value `true` drops every outgoing email without an error, including mail sent with settings saved in the admin. |

What these settings do is described under [Email delivery](#email-delivery).

## Packages, registry and plugins

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_REGISTRY_URL` | `https://hatamoto.dev/registry.json` | Signed registry index to browse for plugins and themes. `off` (case-insensitive) disables browsing; uploads still work. Must be https. |
| `HATAMOTO_REGISTRY_ALLOW_HTTP` | `false` | The exact value `true` (case-insensitive) accepts an `http://` registry. Development only. |
| `HATAMOTO_TRUSTED_KEYS` | empty | Base64 ed25519 public keys of signers you trust, separated by commas, spaces, tabs or new lines. A malformed key stops the boot. |
| `HATAMOTO_TRUST_FIRST_PARTY` | `true` | The exact value `false` (case-insensitive) stops trusting the built-in first-party signing key. |
| `HATAMOTO_ADMIN_PATH` | unset | Pins the admin address and wins over the stored one. One segment of 4 to 64 lowercase letters, digits or dashes, like `/my-place`. When it is unset, a fresh install generates a private address and an upgraded install keeps `/admin`. An invalid or reserved value stops the server at boot. Print the address with `hatamoto admin-url`. |
| `HATAMOTO_NOTFOUND_PER_MIN` | `60` | Not-found requests per minute per client before that client is blocked from the admin address. `0` turns the throttle off. A value that is not a whole number of 0 or more stops the server at boot. |
| `HATAMOTO_NOTFOUND_BLOCK` | `2m` | How long that block lasts, as a Go duration of 1 second or more. A shorter or invalid value stops the server at boot. |
| `HATAMOTO_PLUGIN_DIR` | empty | Development override: load unsigned plugins from `<dir>/<name>/plugin.json` and `plugin.wasm`, after installed packages and replacing a package of the same name. Leave it unset in production. |
| `HATAMOTO_PLUGIN_CACHE_DIR` | `$HATAMOTO_DATA/cache/wasm` | Where compiled plugin code is cached across restarts. `off` keeps it in memory. The cache can be rebuilt and is created with mode 0700. |
| `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` | `600` | Outbound `http.fetch` requests one plugin may make per minute. A value that is not a positive integer uses the default. |
| `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS` | unset | The exact value `1` grants plugin database access even when objects open to `PUBLIC` exist. See [Plugin database access](#plugin-database-access). |
| `HATAMOTO_SIGNING_KEY` | unset | CLI only: default for `--key` of `theme pack`, `plugin pack` and `registry index`. |
| `HATAMOTO_ADMIN_DIR` | unset | Development only: load admin templates, CSS and JS from this directory and rebuild them on every request. |

## Updates

| Variable | Default | Meaning |
|---|---|---|
| `HATAMOTO_UPDATES` | `auto` | `auto` (also `on`, `true`, `1`), `notify` (look but never install) or `off` (no network call; also `false`, `0`, `no`, `disabled`), in any case. Any other value is treated as `off` and logged. The environment wins over the admin setting. |
| `HATAMOTO_UPDATE_CHANNEL` | `stable` | `stable`, `edge` or `canary`. An invalid value is ignored with a warning. |
| `HATAMOTO_UPDATE_URL` | `https://hatamoto.dev/releases` | Base URL of the release channels and files. https only. |
| `HATAMOTO_UPDATE_BACKUP` | on | `0` turns off the database dump taken before an update. |

How these are used is described in [How updates work](../updates/index.md).

## Crawler analytics ingest

| Variable | Default | Meaning |
|---|---|---|
| `AGENT_LOG_INGEST` | `false` | Turns on log ingest. The HTTP endpoint also needs `AGENT_LOG_INGEST_SECRET`; the Caddy log reader (`AGENT_LOG_CADDY_PATH`) does not. |
| `AGENT_LOG_INGEST_SECRET` | empty | Shared secret sent as `X-Agent-Log-Secret` to `POST /api/v1/public/agent-logs`. No default. |
| `AGENT_LOG_CADDY_PATH` | empty | When set with the flag on, the server follows this Caddy JSON access log (read-only). |
| `HATAMOTO_AGENT_RANGES` | on | `off` stops the daily download of AI-crawler IP ranges (see below). Any other value leaves it on. |

### Crawler IP-range refresh

By default the worker downloads the published IP ranges of about nine AI-crawler operators (the sources are in the built-in agent registry), first about 90 seconds after start and then daily, to confirm that a hit claiming to be a known crawler really came from that operator. The requests carry no cookies and no data about your site; the operators' servers see your server's address and the `hatamoto-agent-verifier` user agent.

Set `HATAMOTO_AGENT_RANGES=off` to schedule no refresher and make no such downloads. Crawler hits are still recorded. Verification still runs (it does not wait for a download): it relies on what is already stored plus **forward-confirmed reverse DNS**: for a crawler that publishes a hostname suffix instead of ranges, Hatamoto looks up the hit's IP address through your server's DNS resolver (reverse lookup, then a forward lookup to confirm). Crawlers that publish only IP ranges, and have none stored, end up `unverifiable` rather than `spoofed`. This switch is separate from `WORKER_DISABLE_JOBS=agent_ranges_refresh,agent_verify`, which also works but is the per-job form; `agent_verify` can be disabled the same way if you want no reverse DNS lookups either.

See [Access-log ingest](../ai-seo/access-log-ingest.md).

## Command-line only

| Variable | Default | Meaning |
|---|---|---|
| `WP_APP_PASSWORD` | unset | Default for `--app-password` of `hatamoto import wordpress`, so the password stays off the command line. |

Which command reads which variable is listed in the [command-line reference](cli-reference.md).

## Set by Hatamoto itself

`HATAMOTO_SMOKE_ADDR` and `HATAMOTO_SMOKE_TOKEN` are set by the **supervisor**, the `hatamoto supervise` process that starts the server and switches releases, for the child process it starts, so it can check that a new release answers. Do not set them yourself; the supervisor drops any value it inherits.

## Older names

`JWT_SECRET` is the only older variable name still read (see above). `LEAD_COOKIE_SECURE`, from earlier releases, is replaced by `ADMIN_COOKIE_SECURE`; if you relied on it, set the new one explicitly. Themes are packages kept in the database, so the server reads no theme directory variable. `HATAMOTO_THEME_DIR` and `HATAMOTO_SITE_THEME_DIR` from earlier releases are refused: if either is still set, the server stops at boot with `invalid configuration`. Pack the directory with `hatamoto theme pack`, install the package and unset the variable. Names not listed on this page are not read by the server.

## Files Hatamoto writes

| Path | Holds | Rebuildable |
|---|---|---|
| `$HATAMOTO_SECRETS_DIR/secrets.env` | The generated server secret, mode 0600, directory 0700. | No. Back it up, or set the secret in the environment. |
| `$HATAMOTO_DATA/releases` | Release binaries and the supervisor's state (`state.json`, `supervisor.pid`). | The binaries can be fetched again. |
| `$HATAMOTO_DATA/backups/pre-<version>.dump` | The database dump taken before an update, mode 0600, directory 0700. One is kept. See [Backups and rollback](../updates/backups-and-rollback.md). | No, but it is not a backup plan. |
| `$HATAMOTO_DATA/cache/wasm` | Compiled plugin code. | Yes, recreated on demand. |
| `$HATAMOTO_DATA/install_id` | A random identifier for this install, mode 0600. It decides when this install is offered a new release and never leaves the machine. | Keep it with the data directory; a new one changes when releases arrive. |
| `MEDIA_UPLOAD_DIR` | Uploaded media files, including generated image variants. The database only stores their records. | No. Back it up. |

There is no content directory and no theme directory: content, themes, plugins and plugin data are stored in Postgres. Logs go to standard output; Hatamoto writes no log files.

## The server secret

`HATAMOTO_SECRET` must be at least 32 characters; the server refuses to start otherwise. It keys the flash cookie, theme preview links, the stored credentials of WordPress imports, the stored captcha secret key of forms and the form timing check, and encrypts the SMTP password saved in **Settings > Email**. `JWT_SECRET` is the old name of the same setting.

If neither is in the environment, `serve` reads `secrets.env` from `$HATAMOTO_SECRETS_DIR`, and generates and saves a 64-character value (mode 0600) if it is missing. If it cannot write the file, the boot stops and asks you to set `HATAMOTO_SECRET` or make the directory writable. A value in the environment always wins and is never written to disk.

Keep the secret stable. Changing it invalidates theme preview links, stored import credentials and the stored captcha secret key, and a restored database without the same secret cannot decrypt them. A saved SMTP password it can no longer read makes Hatamoto fall back to the `SMTP_*` variables until an owner enters the password again. Every process that serves the same site must use the same value.

## Security headers

Every response gets these headers unless `SECURITY_HEADERS_DISABLED=true`:

| Header | Value |
|---|---|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY`; public pages and the admin send `SAMEORIGIN`, so the admin preview can frame them |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `X-XSS-Protection` | `0` |
| `Permissions-Policy` | `geolocation=(), camera=(), microphone=()` |
| `Content-Security-Policy-Report-Only` | the policy below, until `CSP_ENFORCE=true` turns it into `Content-Security-Policy` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains`, only with `ENVIRONMENT=production` |

Browsers cache the HSTS header for a year and apply it to every subdomain of the host. Before you turn on production mode, make sure no subdomain of your domain is still served over plain HTTP.

### The Content Security Policy

Public pages send this policy:

```text
default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';
img-src 'self' data: https:; font-src 'self'; connect-src 'self'; frame-src https:;
frame-ancestors 'self'; object-src 'none'; base-uri 'self'; form-action 'self'
```

Other responses, such as the JSON API, send a stricter variant: `img-src 'self' data:`, no `frame-src`, and `frame-ancestors 'none'`.

When a captcha provider is set up under **Settings > Spam protection** and its secret key is readable, its addresses are added to `script-src` and `connect-src` of the public policy:

| Provider | Added origins |
|---|---|
| Turnstile | `https://challenges.cloudflare.com` |
| reCAPTCHA | `https://www.google.com https://www.gstatic.com` |
| hCaptcha | `https://hcaptcha.com https://*.hcaptcha.com` |

The provider's frames are already allowed by `frame-src https:`. Cached pages pick the change up on the next request, but a CDN that caches whole responses can serve the old header for up to 5 minutes. Without a provider the policy is unchanged.

Report-only mode never blocks anything. Images and frames may come from any HTTPS address, but scripts and connections must be same-origin, which a third-party script breaks. The admin always sends its own enforcing policy with a per-request nonce, whatever `CSP_ENFORCE` says.

### Embeds in rich text

Rich text may contain `iframe` embeds only from an allowlist. By default it holds YouTube (`www.youtube.com`, `youtube.com` and the `youtube-nocookie.com` names, under `/embed/`) and Vimeo (`player.vimeo.com`, under `/video/`). Only `https` sources match.

`HATAMOTO_EMBED_HOSTS` is a comma-separated list of bare host names with at least one dot, such as `player.example.com`. When it holds at least one valid host, it replaces the default list: add the YouTube or Vimeo names to it if you still want them. A listed host that is not a default one may be embedded at any path. Invalid entries are skipped with a log warning, and a value with no valid host leaves the defaults in place.

## Cookies and sessions

The admin session cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` when the request is HTTPS (directly, or through a trusted proxy that says so) or when `ADMIN_COOKIE_SECURE` is true. `ADMIN_COOKIE_SECURE` acts as a minimum: you cannot turn `Secure` off for requests that arrive over HTTPS. That is why the first-run wizard still works over plain HTTP on localhost.

`ENABLE_COOKIE_AUTH` lets the JSON API session also travel as an HttpOnly cookie with a session-bound CSRF token. It is a separate session from the admin's, and the admin cookie does not sign in to the API. Leave it off unless a browser client needs it; API clients send a bearer token.

Admin sessions are stored in the database. Five failed sign-ins in a row lock an account for 15 minutes. `hatamoto user reset-password` unlocks it and signs the user out everywhere.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Until the first user exists, anyone who can reach `<admin>/setup` and knows the **setup code**, a one-time code the server prints to its log so only someone with access to the server can create the first owner, can create the owner account.

## Trusted proxies

Hatamoto believes `X-Forwarded-*` headers only when the immediate peer is a trusted proxy. Otherwise a visitor could claim any client address or pretend to use HTTPS.

The list comes from `HATAMOTO_TRUSTED_PROXIES`, a comma-separated list of CIDRs. A bare IP address is accepted and means that one host. When the variable is empty, only loopback (`127.0.0.0/8`, `::1/128`) is trusted. Private ranges are **not** trusted by default: behind a published Docker port every client arrives from the bridge gateway, so trusting private ranges would let anyone spoof `X-Forwarded-For`. If your proxy is not on the same host's loopback (another container, another machine, a load balancer), set this variable to its address. **Docker deployments must set it:** a host proxy reaching a published port arrives from the bridge gateway, not loopback, so set it to that gateway (for example `HATAMOTO_TRUSTED_PROXIES: 172.18.0.1` under `environment:` in `production.yml`; find it with `docker network inspect <project>_default --format '{{(index .IPAM.Config 0).Gateway}}'`) or to the proxy container's address. Until you do, every visitor looks like the gateway, which shares one rate-limit bucket. A non-empty value replaces the default entirely, so keep `127.0.0.1` in the list if a local proxy also needs it. The Docker Compose file sets nothing. When a peer outside the list sends `X-Forwarded-For` and nothing is configured, Hatamoto logs a one-time warning that it ignored the header. An entry that is not a valid CIDR or IP stops the boot with a message.

What trust changes:

| Item | Behaviour |
|---|---|
| Client IP | Hatamoto walks `X-Forwarded-For` from the right and takes the first address that is not itself a trusted proxy, then falls back to `X-Real-IP`, then to the peer address. The IP is used for login lockouts, rate limits and (shortened) crawler analytics. |
| Scheme | A request counts as HTTPS if it arrived over TLS directly, or from a trusted proxy with `X-Forwarded-Proto: https`. Admin cookies get the `Secure` attribute accordingly. |
| Host | `X-Forwarded-Host` is honoured only from a trusted proxy. |

If the proxy runs in another container or on another network, such as a cloud load balancer, add its address or range. If no proxy is in front at all, leave the variable empty.

## Email delivery

Email settings come from one of two places:

| Source | Used when |
|---|---|
| **Settings > Email** in the admin | An owner saved settings there (or in the **Email** step of the first-run wizard). They replace the `SMTP_*` variables as a whole, not field by field. |
| The `SMTP_*` variables | Nothing is saved in the admin, or the saved password cannot be read because `HATAMOTO_SECRET` changed. Email is on when `SMTP_HOST` is a host other than `localhost`. |

**Use the server's SMTP_* settings instead** (`<admin>/settings/email/reset`) deletes the saved settings. The page shows a badge and a `Using:` line with the source in use. `EMAIL_DISABLED=true` switches all sending off whatever the source.

### The admin form

| Field | Rule |
|---|---|
| Provider | **Custom**, **Amazon SES** or **Gmail / Google Workspace**. SES uses `email-smtp.<region>.amazonaws.com` (region default `us-east-1`); Gmail uses `smtp.gmail.com`. Host is only read for Custom. |
| Security | **STARTTLS** (plain connection upgraded, usually port 587), **TLS (SSL)** (encrypted from the first byte, usually port 465) or **None**. None is accepted only for `localhost`, `127.0.0.1` or `::1`. With saved settings, `localhost` counts as set up, for a local mail catcher. |
| Username, Password | Both blank means no sign-in. The password is encrypted with `HATAMOTO_SECRET` and never shown. A blank password keeps the saved one only while host and username are unchanged. |
| From address, From name | A blank From name uses the site name stored in the database, looked up when each mail is sent, so a rename applies at once. |

**Save** (`POST <admin>/settings/email`) connects, negotiates TLS, signs in and sends MAIL FROM, with a 10 second limit, and sends no mail. On failure the page shows the SMTP code and message and nothing is stored. **Send a test email** (`<admin>/settings/email/test`) mails the signed-in owner with the settings in use. A change applies at once on the process that saved it; the others check every 30 seconds and pick it up.

### The connection

Hatamoto connects to the host and port, protects the connection as the Security setting says (always STARTTLS for the `SMTP_*` variables), and authenticates with PLAIN authentication when both a user and a password are set. The server certificate is checked against the host name. So:

- With the `SMTP_*` variables, use a submission port that offers STARTTLS (587 is the usual one, and the default). Ports with implicit TLS, such as 465, need **TLS (SSL)** in **Settings > Email**.
- The certificate must be valid for the host name. Use the provider's host name, not an IP address.
- With STARTTLS, a server that does not offer it fails the send, and the error is logged.

The boot stops with `SMTP_FROM is required when SMTP_HOST is set` when `SMTP_HOST` is a real host and `SMTP_FROM` is still `noreply@localhost`, because relays reject that sender and it fails SPF and DMARC checks.

### What Hatamoto sends

| Mail | When |
|---|---|
| Password reset | A user asks for a reset on the sign-in page. The link expires after one hour. |
| Invitation | An owner invites a new user in **Users**. The link expires after seven days. |
| Form notification | A visitor submits a form; the mail goes to the form's notification addresses. |
| Form forward | An owner forwards a stored submission from the admin; the subject starts with `Fwd:`. |
| Form confirmation | The form has confirmation turned on and the visitor filled in its email field. |
| Test message | An owner presses **Send a test email** in **Settings > Email**. |

Reset and invitation links are built from `PUBLIC_BASE_URL` or the Site address setting, and are sent as mail only when one of them is set: Hatamoto does not build an emailed link from the request host. Mail is rendered from templates built into the program; account mails carry an HTML and a plain-text part. Header and subject values are checked for line breaks.

### Without email

While nothing is saved in **Settings > Email** and `SMTP_HOST` is empty or `localhost`:

- Invitations and password resets show a one-time link to the owner, to pass on by hand.
- The forgot-password page points to the command line instead: `hatamoto user reset-password --email you@example.com`.
- Form notification and confirmation mail is skipped, with the info log line `email service not configured, dropping send`. The submission is still stored and visible in the admin.

With `EMAIL_DISABLED=true`, every send is dropped with the warn log line `email disabled by EMAIL_DISABLED env var, dropping send`, and the caller sees success.

Each process sends directly. There is no shared mail queue, and a failed send is logged, not retried. Settings saved in the admin live in the database and reach every process within 30 seconds; the `SMTP_*` variables are read per process, so give every process the same values.

### Errors in the log

| Message | Meaning |
|---|---|
| `failed to dial SMTP server` | The host or port cannot be reached from the server. |
| `failed to start TLS` | The server does not offer STARTTLS, or its certificate does not match the host name. |
| an authentication error | The username or password is wrong for this provider. |
| `saved SMTP password cannot be read (the server SECRET changed)` | `HATAMOTO_SECRET` changed since the password was saved. The `SMTP_*` variables are used until an owner enters it again. |

## Plugin database access

Plugins that use the `db` host API run under their own Postgres login roles, named `hm_p_<id8>_<name>`. Three things must hold for them:

- Nothing in the database may be open to `PUBLIC` that the application role does not own. Otherwise `db.*` stays off until the owner of those objects runs `REVOKE ... FROM PUBLIC`, and the log says so. `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1` accepts the exposure instead.
- `pg_hba.conf` must allow password authentication for the plugin roles on the host `DATABASE_URL` uses, for example `host all all 127.0.0.1/32 scram-sha-256`.
- `DATABASE_URL` must use a TCP host, not a Unix socket with peer authentication.

Roles belong to the Postgres cluster, not to one database, so `pg_dump` of the database does not include them. A plugin's schema and tables are in the dump. After a restore into another cluster, reload or grant the plugin again so the server creates its role. Plugins that only use their key-value storage are not affected.

## Limits and errors

| Message at boot | Cause |
|---|---|
| `DATABASE_URL is required` | The variable is unset or empty. |
| `invalid API_PORT` | `API_PORT` is not an integer. |
| `HATAMOTO_SECRET must be at least 32 characters` | The secret in the environment is too short. |
| `HATAMOTO_SETUP_CODE must be at least 12 characters` | The setup code is too short (counted after case, spaces and dashes are removed). It stops the boot only while the site has no users. A code that is one repeated character or pattern is refused with `HATAMOTO_SETUP_CODE is too guessable`. |
| `HATAMOTO_TRUSTED_PROXIES: "..." is not a CIDR like 10.0.0.0/8` | An entry in the proxy list is not a CIDR or IP. |
| `HATAMOTO_TRUSTED_KEYS holds "...", which is not an ed25519 public key` | A trusted key is not 32 bytes of base64. |
| `HATAMOTO_REGISTRY_URL "..." must be an https URL (or "off")` | The registry address is not an http or https URL with a host. |
| `HATAMOTO_REGISTRY_URL "..." is http: the registry must be served over https` | The registry address is http and `HATAMOTO_REGISTRY_ALLOW_HTTP=true` is not set. |
| `SMTP_FROM is required when SMTP_HOST is set` | Email is on but the sender is still the default. |
| `HATAMOTO_THEME_DIR is no longer read` | An old theme directory variable is still set. |

The `API_PORT` message appears after `load configuration:`, the others after `invalid configuration:`, and the process exits with status 1.
