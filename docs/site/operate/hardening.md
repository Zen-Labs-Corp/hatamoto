---
title: Harden an instance
nav_title: Hardening
description: Go through the production checklist before you open your site to the internet: settings, secrets, headers, plugins, files and outbound calls.
next: backups.md, multi-replica-and-health.md, configuration-reference.md
---

In this guide you work through the checklist for an instance that faces the internet. The defaults boot with only `DATABASE_URL`, which is wrong for a public site. At the end the instance runs in production mode behind HTTPS, with a stable secret, an owner account, tight file permissions and only the outbound calls you want.

## Before you start

- An instance deployed with [Docker](deploy-docker.md) or [systemd](deploy-systemd.md), and access to its environment file.
- A reverse proxy with TLS in front of it. See [Put a reverse proxy in front](reverse-proxy-tls.md).
- A secret manager or another safe place for the server secret.

## Steps

### Settings and secrets

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Bind or publish the backend port to the proxy only, for example `127.0.0.1:8090`.
2. Set `PUBLIC_BASE_URL` to your public origin, such as `https://example.com`.
3. Set `ENVIRONMENT=production`. Logs switch to JSON, every response carries an HSTS header, and `ADMIN_COOKIE_SECURE` defaults to on. Do this only behind HTTPS, and only once no subdomain is served over plain HTTP.
4. Provide `HATAMOTO_SECRET`, the **server secret**, from your secret manager, 32 characters or more, and keep it stable. It keys the flash cookie, theme preview links and stored WordPress import credentials, and encrypts the SMTP password saved in **Settings > Email**. A value in the environment always wins and is never written to disk. Generate one with:

   ```bash
   openssl rand -base64 48
   ```

5. Create the owner account now: the **owner** role can do everything, including managing users. Until the first user exists, anyone who reaches `<admin>/setup` and knows the **setup code**, a one-time code the server prints to its log so only someone with access to the server can create the first owner, can take the site. Set `HATAMOTO_SETUP_CODE` (12 characters or more), read the code from the log, or create the owner from the shell before you open the port:

   ```bash
   hatamoto user create --email you@example.com
   ```

6. Use a strong database password, and a database that is not reachable from the internet.
7. Check that `HATAMOTO_TRUSTED_PROXIES` covers your proxy's address, and nothing more than you control.
8. Set up email at Settings > Email or with the `SMTP_*` variables, so password resets and form notices reach people. See [Set up email](email.md).

### Security headers and CSP

1. Leave `SECURITY_HEADERS_DISABLED` unset. It turns off every security header and is for local debugging only.
2. Test the Content Security Policy, the header that tells browsers which scripts, styles and frames a page may load. It ships in report-only mode, so it never blocks anything. Open your public pages with the browser console open and look for violations. When the pages are clean, set `CSP_ENFORCE=true`. The admin always sends its own strict policy.
3. If editors embed video from hosts other than YouTube and Vimeo, list them in `HATAMOTO_EMBED_HOSTS`. A valid list replaces the default hosts, so include those too if you still want them.

The full header list, the policy text and what a captcha provider adds to it are in the [configuration reference](configuration-reference.md).

### Plugins, files and outbound calls

1. Unset `HATAMOTO_PLUGIN_DIR`. It loads unsigned plugins from a directory and is for development only.
2. Keep the **trusted keys**, the public keys your server accepts signatures from, to the ones you need. Every **package**, the one file that holds a whole theme or plugin, is checked against them. `HATAMOTO_TRUSTED_KEYS` adds keys, and `HATAMOTO_TRUST_FIRST_PARTY=false` removes the built-in first-party key. A malformed key stops the boot.
3. Leave `HATAMOTO_REGISTRY_ALLOW_HTTP` unset, so the **registry**, the signed list of packages you can browse and install, is only fetched over https. `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` caps each plugin's outbound requests (default 600).
4. If a plugin uses its own database tables, read the plugin database access section of the [configuration reference](configuration-reference.md): nothing may be open to `PUBLIC` that the application role does not own, `pg_hba.conf` must allow password logins for plugin roles, and `DATABASE_URL` must use a TCP host.
5. Check file permissions. The secrets file and update backups are created with mode 0600 and their directories 0700; keep them that way. Run the service as a dedicated unprivileged user (the Docker image uses uid 10001) that owns the data and upload directories and nothing else. The environment file holds the database password, so make it readable by root and the service group only.
6. Decide on outbound calls. Hatamoto sends no telemetry unless an owner turns on [install stats](install-stats.md). For zero outbound calls from the server itself, set `HATAMOTO_UPDATES=off` (no update check at all), `HATAMOTO_TELEMETRY=off` (install stats can never be turned on), `HATAMOTO_REGISTRY_URL=off` (uploads still work) and `HATAMOTO_AGENT_RANGES=off` (no IP-range downloads), and `WORKER_DISABLE_JOBS=agent_verify` (no reverse-DNS lookups of addresses that claim to be known crawlers; their hits then stay unverified). Every call is listed in [Outbound connections](outbound-connections.md). Plugins only reach the hosts they declare and you grant. See [How updates work](../updates/index.md).
7. Set up backups of the database, the secret and the uploads. See [Back up and restore](backups.md).

## Check it worked

Restart the service, then look at the headers of your home page through the proxy. The output includes these lines, in some order:

```bash
curl -sI https://example.com/
```

```text
strict-transport-security: max-age=31536000; includeSubDomains
x-content-type-options: nosniff
x-frame-options: SAMEORIGIN
referrer-policy: strict-origin-when-cross-origin
```

You should also see `content-security-policy` once `CSP_ENFORCE=true` is set, or `content-security-policy-report-only` before that, and `<admin>/setup` no longer offers to create an owner: it sends you to the sign-in page or says setup is already finished.

If the service does not start after these changes, read the last log line. It names the setting, for example `HATAMOTO_SECRET must be at least 32 characters` or `HATAMOTO_TRUSTED_KEYS holds "...", which is not an ed25519 public key`.
