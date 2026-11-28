---
title: Install stats
nav_title: Install stats
description: Install stats are on by default and can be turned off: what is sent, what is never sent and how to turn it off or rule it out.
---

Install stats are a report from a running Hatamoto install, sent with a random install ID and the site address (its domain, or its public IP address). They have two states: **on** (the Detailed report) and **off**. A new install, and an existing one that was never asked, sends the report once setup is finished, unless an owner turns it off or the server sets `HATAMOTO_TELEMETRY=off`. Development builds and public demos send nothing. An owner who turned stats off keeps that choice. This page lists what is sent and how to control it. The update check is a separate request and stays anonymous; see [Outbound connections](outbound-connections.md) for every call the server makes.

An install that sent the older, smaller Basic report moves to the Detailed report when it upgrades. Off stays off.

## What is sent

Once a day, Hatamoto sends one `POST` to `https://hatamoto.dev/p/stats/v1/ping`. Hatamoto checks every hour (the first check 5 to 15 minutes after the server starts) and sends when the last report is more than 20 hours old or the version changed since the last report, so a restart or an update does not delay it by a day. After a version change the report goes out at the next hourly check, but never twice within 30 minutes, so a receiver that is down is tried once per check at most. The request has the user agent `hatamoto-stats` and a 10 second timeout. It sends no cookies and follows no redirects. A development build, whose version is not a release, never sends. A failed request is not retried for 20 hours (or 30 minutes after a version change).

### The report

The body is JSON with these keys:

```json
{
  "install": "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65",
  "version": "v1.4.2",
  "channel": "stable",
  "level": "detailed",
  "host": "example.com",
  "os": "linux",
  "arch": "amd64",
  "postgres": 16,
  "age_months": 6,
  "worker": true,
  "theme": {"name": "example-theme", "version": "1.6.0"},
  "plugins": [{"name": "searchconsole", "version": "1.0.0", "enabled": true}],
  "other_plugins": 2,
  "health": {
    "since_hours": 24,
    "panics": 0,
    "http_5xx": 3,
    "render_errors": 0,
    "plugin_errors": {"searchconsole": 1},
    "update_failed": 0,
    "update_rolled_back": 0,
    "migration_failed": 0
  },
  "comments_on": true,
  "static_mode": false,
  "sso_on": false,
  "locales_bucket": "1",
  "forms_in_use": true,
  "offsite_backups_on": false,
  "csp_enforced": true,
  "wp_imported": false
}
```

| Key | Value |
|---|---|
| `install` | A random ID of 32 hexadecimal characters, made when stats are first turned on, by the owner or by the default. It is not derived from anything on your server. |
| `version` | The release the server runs. |
| `channel` | The update channel in use: `stable`, `edge`, `canary` or `lts`. It is whatever you set, also when the update mode is `off`. |
| `level` | Always `detailed`. |
| `host` | The public site address, taken from `PUBLIC_BASE_URL` if it is set, else from the site address setting: a domain name (lower-case, no port, no path, for example `example.com`) or a public IP address (IPv6 in canonical lower-case form without brackets, for example `2606:4700:4700::1111`). Left out for `localhost`, for names ending in `.local`, `.internal` or `.localhost`, and for private, loopback, link-local, shared (CGNAT), reserved and documentation addresses. It is never looked up and never the machine's own hostname. |
| `os`, `arch` | The operating system and CPU architecture the server runs on, as Go names them (`linux`, `amd64`). |
| `postgres` | The major version of the database, for example `16`. |
| `age_months` | How long the install has existed, rounded down to one of 0, 1, 3, 6, 12, 24 or 36 months (36 means 36 or more). Never the exact date. |
| `worker` | Whether the background worker runs on this server. |
| `theme` | The name and version of the active theme. Always sent by name, whoever made it. |
| `plugins` | The installed plugins that are first-party or published in the main registry (first-party and community plugins): name, version and whether it is enabled. At most 100, sorted by name. |
| `other_plugins` | A count of all other plugins (self-built, unsigned or signed by another key). Their names never leave the server. |
| `health` | Counts of problems since the last report, see below. |
| `comments_on` | Whether the site comment switch is on. |
| `static_mode` | Whether static hosting mode is on. |
| `sso_on` | Whether single sign-on (OIDC) is on. |
| `locales_bucket` | How many locales are configured, as a range: `1`, `2-3` or `4+`. |
| `forms_in_use` | Whether at least one form is active. |
| `offsite_backups_on` | Whether off-site backups are on. |
| `csp_enforced` | Whether the Content-Security-Policy is enforced (`CSP_ENFORCE`). |
| `wp_imported` | Whether a WordPress import has completed. |

The features keys (`comments_on` to `wp_imported`) are yes/no answers, except `locales_bucket`, which is a range. A key is left out when Hatamoto cannot read it. They say nothing about content or visitors.

The health counts are numbers only: never a message, a path, a URL, a stack trace or a person. They are stored in the database, so a restart does not lose them, and each is capped at 1,000,000.

| Count | What it counts |
|---|---|
| `since_hours` | The length of the window the counts cover, in hours. |
| `panics` | Recovered panics in request handling. |
| `http_5xx` | Responses with a status of 500 or more, except 503, which Hatamoto answers on purpose (maintenance mode, a failing readiness check, a feature that is not configured, a busy upload queue). Includes the 500 written for a recovered panic. |
| `render_errors` | Public page render failures, attributed to the active theme. |
| `plugin_errors` | Per listed plugin: traps, timeouts, memory-limit stops and errors returned by the plugin code. Errors of plugins that are not listed are summed under the key `other`. |
| `update_failed` | Updates that failed. |
| `update_rolled_back` | Updates that were rolled back. |
| `migration_failed` | Failed database migrations, recorded by a marker file in the data directory that survives the rollback. |

The counts cover the time since the last successful report of an earlier UTC day. A second report on the same UTC day (for example after a version change) replaces that day's report and carries the counts already sent that day plus any new ones. The first report of the next UTC day starts after the last report of the day before, so no count is sent on two days. A failed report loses nothing.

## What is not sent

Content, pages, settings, user names, email addresses, visitor data, IP addresses of visitors, the names of plugins that are not first-party or in the registry, error messages and server details beyond the platform, Postgres major version and worker flag above. No machine hostname is collected, and no IP address except a public one you set as the site address. The server's own address reaches hatamoto.dev as the source of the connection, as with any request. The update check does not carry the install ID.

## Retention

The receiver keeps one report per install and UTC day: a second report the same day replaces the first. This page does not state how long those daily reports are kept, because no retention period has been published yet; it will be added here when there is one.

## Change it or turn it off

Installs that were never asked send the report once setup is finished. **Settings > Updates** is where to see and change it.

There is no setup step for it. The **Usage stats** section has one switch, **Share usage stats with Hatamoto**, on by default, and says what is sent: "Version, site address, theme, plugins, which features are on (yes/no) and error counts, once a day." Turn it off and select **Save** to turn stats off, and turn it on again to turn them on. The section also has a **What is collected** link to this page and shows when a report was last sent successfully. Editors do not see it.

Turning stats on creates the install ID. Turning them off deletes it, and the health counts with it. There is no separate reset: turn stats off and on again to get a new random ID.

## Operator override

Two environment variables, both listed in the [Configuration reference](configuration-reference.md#updates):

- `HATAMOTO_TELEMETRY=off` rules stats out. Nothing is sent, the stored install ID is deleted at start and before every daily run, and the choice is set to off, so removing the variable later does not start sending again without a new answer. The switch is disabled and off, and Settings > Updates shows **Turned off by the server configuration.** Any other value changes nothing: the variable can only turn stats off, never on, and no default is applied while it is set to `off`.
- `HATAMOTO_TELEMETRY_URL` replaces the endpoint, for testing. It must be a plain `https://host/path` with no user, query or fragment. An invalid value is ignored with a warning and the default is used.

To skip the daily job without touching the choice, add `stats_ping` to `WORKER_DISABLE_JOBS`. Nothing is scheduled in a public demo.

## Where it is stored

In one row of the site's database, next to the other site settings: the on or off state, the install ID while stats are on, the time of the last successful report, the version it reported (`last_sent_version`), the health counts and the result of the last attempt (`ok`, `error: http <code>`, `error: network` or `error: build`). `error: build` means no body could be made and nothing was sent. When only the full body cannot be built (for example a theme version the receiver would refuse), a minimal report of three keys (`install`, `version` and `channel`) is sent instead and the health counts stay stored. A failed attempt shows nothing in the admin. All replicas share the row, and a report is sent by one of them at most once a day, plus once at the next hourly check after a version change (never twice within 30 minutes). The ID and the endpoint are never written to the log. A backup of the database carries the row, so a restore keeps the choice and the ID.
