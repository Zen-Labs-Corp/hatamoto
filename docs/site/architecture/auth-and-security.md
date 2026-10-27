---
title: Auth and security
nav_title: Auth and security
description: How Hatamoto signs in people and API keys, blocks forged requests, cleans stored content, sets security headers and guards outbound requests.
---

This page describes every security control in the server: accounts, sessions, API keys, rate limits, content cleaning, headers, outbound requests and the server secret. To lock down a production install step by step, follow [the hardening guide](../operate/hardening.md).

## In one minute

- People sign in to the admin with a password (argon2id) and get a session cookie; scripts and AI tools use API keys with scopes.
- Every admin form and admin API call is checked for a matching anti-forgery token and a same-site origin.
- Rich text and HTML are cleaned on the server when they are saved and again when they are shown.
- Every response carries security headers; the admin has a strict policy with a per-request nonce.
- Plugins and the WordPress importer reach the internet only through a guard that refuses private addresses.

## Who can do what

Hatamoto has two kinds of principals.

| Principal | Used for | Credential |
|---|---|---|
| A person in the admin | the admin UI, and the JSON API when called from a browser session | an opaque session cookie, `__Host-s` (`s` on plain HTTP) |
| An API key | scripts, the command line, MCP clients, publishers | a key sent in the `X-API-Key` header (or as a bearer token to `/mcp`) |

There are two user roles, **owner** and **editor**. Owners manage settings, users, plugins, themes and keys; editors work with content. Role checks run per route. Plugin admin routes and admin pages are owner-only unless the plugin manifest lists more `roles`. There is no visitor login and no gated content: the public site is anonymous.

## Passwords and lockout

Passwords are hashed with **argon2id** (`golang.org/x/crypto/argon2`) using 64 MiB of memory, one pass, four threads, a 16-byte random salt and a 32-byte hash. The encoded hash records its own parameters so they can be raised later. The admin requires passwords of at least 12 characters.

After **5** consecutive failed sign-ins an account is locked for **15 minutes**. The state is stored in the database, so a restart does not reset it. A per-IP rate limiter on the sign-in routes is the first line of defence and the lockout is the second.

Account recovery uses one-time tokens in `hatamoto_user_tokens`. Only the SHA-256 of the token is stored; the raw value exists only in the email and the link. Reset tokens are valid for one hour and invitations for seven days. When email is not set up you can still reset a password from the command line:

```bash
hatamoto user create --email you@example.com
hatamoto user reset-password --email you@example.com
```

## Admin sessions

Signing in creates a row in `hatamoto_sessions` and sets the session cookie, `__Host-s` when `ADMIN_COOKIE_SECURE` is true (the production default) and `s` otherwise, because browsers drop `__Host-` cookies that lack `Secure`. Neither name identifies the software. Releases before this one used `hatamoto_session`, so everyone signed in once after the upgrade. The cookie value is 32 random bytes (base64) and only its SHA-256 is stored, so a database leak does not give out usable sessions. The cookie is `HttpOnly`, `SameSite=Strict`, path `/`, and `Secure` when `ADMIN_COOKIE_SECURE` is true (it defaults to true in production). Sessions have an absolute lifetime of 30 days and a sliding idle timeout of 7 days. A daily worker job purges expired sessions and tokens.

Sessions are not rotated on every request, because parallel htmx requests would race the rotation.

## CSRF protection

The admin uses a **session-bound token** plus an origin check:

- The token is derived from the session, so no CSRF cookie is set. Before sign-in, a short-lived `hm_pre` cookie binds the token of the sign-in forms.
- Every non-read admin request must echo it in the `X-CSRF-Token` header (set by the admin script from a `<meta>` tag) or in a `_csrf` form field. Comparison is constant time.
- Requests with a cross-site `Origin` or `Sec-Fetch-Site` are rejected outright.

Failures show a "Request blocked" page. The JSON API applies a double-submit check (a readable `__Host-c` cookie, `c` on plain HTTP) to cookie-authenticated mutations when `ENABLE_COOKIE_AUTH` is on; bearer-token and API-key requests are exempt, because they carry no ambient credential.

## API keys

API keys are created in the admin (Settings) or with `hatamoto apikey create`. A key is a random token with a fixed prefix; the database stores only a SHA-256 hash and a short prefix for identification. The scopes a key can hold are:

`read:entities`, `write:entities`, `write:media`, `write:menus`, `write:redirects`, `write:terms`, plus the wildcard `*`. The admin form also offers `read:reports`, which unlocks the MCP insight tools, and four reserved scopes (`read:citations`, `write:reports`, `read:query_suites`, `write:query_suites`) that no route checks today, so they grant nothing.

Two further limits apply to every key:

1. **A fail-closed route allowlist.** Through the `/api/v1` group an API key can reach only the routes listed in `internal/middleware/apikey_route_auth.go` (entity read and write, versions, restore, render preview, internal linking, and similar). Everything else returns 403 no matter what scopes the key holds, including `*`.
2. **A per-key rate limit** in requests per hour, default 1000, enforced in memory. The burst is a tenth of the hourly limit, and at least 10.

MCP calls to `/mcp` authenticate the same way and are checked per tool against the key's scopes. Every MCP tool call is written to `hatamoto_mcp_audit` and pruned after 90 days.

## Rate limiting and client IPs

Several routes carry in-memory per-IP limiters (token buckets):

| Route | Rate | Burst |
|---|---|---|
| sign-in (admin and `/api/v1/auth/login`) | 5 per second | 10 |
| form submissions (`/forms/{slug}/submit` and `/api/v1/public/forms/{slug}/submit`, each with its own budget) | 1 every 10 seconds | 5 |
| site search (`/search` with `q`) | 2 per second | 30 |
| pageview beacon (`/api/v1/track`) | 20 per second | 50 |
| public plugin routes (`/p/{plugin}/...`) | 10 per second | 30 |
| public term archives (`/api/v1/public/terms/{kind}/{slug}/entities`) | 10 per second | 30 |
| access log ingest (`/api/v1/public/agent-logs`, only when `AGENT_LOG_INGEST` is on with a secret) | 2 per second | 10 |

A limited request gets `429` with `Retry-After: 1`.

The client IP used for limits and analytics is the socket address, unless the request arrives from a **trusted proxy**. By default only loopback is trusted (a Docker deployment sets the bridge gateway); set `HATAMOTO_TRUSTED_PROXIES` to a comma-separated list of CIDRs (a bare address means that one host) to change that. An entry that is not a CIDR or an address stops the server at start-up. For a trusted peer, the rightmost `X-Forwarded-For` hop that is not itself a trusted proxy is used. Limiters are per process, so a multi-replica deployment multiplies the effective budget by the replica count.

## Content safety

Content is treated as untrusted at three points:

1. **On write.** Rich text and HTML sections are sanitised with `bluemonday` policies inside the entity service before the row is stored, in addition to any browser-side cleaning. The server is the source of truth.
2. **On render.** The theme engine applies the policy again, so a row that was stored by an older release or an import cannot bypass it.
3. **On preview.** Editor previews go through the same path.

The policies are covered by golden tests with a corpus of real-world markup. Theme templates use `html/template`, which escapes by context. The only places raw HTML is emitted are fields explicitly marked as HTML and the output of trusted theme code.

## Security headers and CSP

Every response gets the baseline headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-XSS-Protection: 0` and `Permissions-Policy: geolocation=(), camera=(), microphone=()`. The public-site Content-Security-Policy is delivered as `Content-Security-Policy-Report-Only` by default so you can watch for violations before enforcing it; set `CSP_ENFORCE=true` to enforce. `Strict-Transport-Security: max-age=31536000; includeSubDomains` is added only in the `production` environment (`ENVIRONMENT=production`; the default is `development`). `SECURITY_HEADERS_DISABLED` removes all of these and exists for local debugging only.

The baseline policy is `default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'self'; form-action 'self'`. Public pages built by the theme change it in three ways, so the admin editor can show them in a frame and embeds work: `X-Frame-Options: SAMEORIGIN`, `frame-ancestors 'self'`, and `img-src` and `frame-src` allow any `https:` source. The origins of the configured captcha provider are added too.

The admin always uses its own strict policy, enforced regardless of `CSP_ENFORCE`: `default-src 'none'`, scripts and styles only from `'self'` plus a random per-request nonce, no `unsafe-inline`, `base-uri 'none'`, `object-src 'none'` and `frame-ancestors 'self'` (the preview frames are same-origin). Admin responses also set `X-Frame-Options: SAMEORIGIN`, drop the report-only header and send `Cache-Control: no-store`.

Plugin responses are sandboxed too: HTML that a plugin route returns is served with `Content-Security-Policy: sandbox; default-src 'none'; ...` so it runs without scripts in an opaque origin and cannot act as the site or the admin. Plugin cookies are confined to the `hmp_<plugin>_<name>` namespace, so a plugin can neither read nor set the session or CSRF cookies.

## Outbound requests

Two features reach out to the internet on a visitor's or owner's behalf: plugin `http.fetch` and the WordPress importer. Both use the shared `safefetch` package, which:

1. resolves the hostname itself and refuses private, loopback, link-local (including cloud metadata addresses), CGNAT, multicast, documentation and reserved ranges, including IPv4 addresses embedded in IPv6 forms;
2. connects to the **checked IP literal**, never to the name again, so DNS rebinding cannot swap the address between check and connect;
3. repeats the checks on every redirect hop and refuses schemes other than `http` and `https`.

Plugins are further limited by the `allowed_hosts` list in their manifest, a per-plugin request budget (`HATAMOTO_PLUGIN_FETCH_PER_MINUTE`, default 600) and size and time caps. See [Plugin runtime](plugin-runtime.md).

## Secrets

The server secret (`HATAMOTO_SECRET`, at least 32 characters; `JWT_SECRET` is accepted as its older name) keys the flash cookie, theme-preview links and stored credentials, and seals the SMTP password saved in Settings > Email. If the secret changes, that password cannot be read: Hatamoto falls back to the `SMTP_*` environment and asks you to "Re-enter your SMTP password". If you do not set it, `hatamoto serve` generates one and saves it to `secrets.env` with mode 0600 inside `HATAMOTO_SECRETS_DIR` (default `./data/secrets`; the Docker image sets `/data/secrets`). A value in the environment always wins and is never written to disk. It must survive restarts, so keep that directory on persistent storage and in your backups; see [Requirements and secrets](../getting-started/requirements-and-secrets.md).

## Hardening checklist

- Serve over HTTPS and run with `ENVIRONMENT=production` semantics so HSTS and secure cookies are on (see the [hardening guide](../operate/hardening.md)).
- Turn on `CSP_ENFORCE` once the report-only policy shows no violations for your theme.
- Keep API keys narrowly scoped and rotate them when people leave.
- Review the admin audit log and the MCP audit log periodically.
- Grant plugin permissions deliberately; each is shown to the owner before install.
