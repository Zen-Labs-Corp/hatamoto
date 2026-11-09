---
title: Install stats
nav_title: Install stats
description: The optional anonymous install stats at three levels (Off, Basic, Detailed): what each sends, what is never sent, how to change it and how an operator rules it out.
---

Install stats are an anonymous report from a running Hatamoto install. An owner picks one of three levels: **Off**, **Basic** or **Detailed**. Nothing is sent, and no install ID exists, until an owner picks Basic or Detailed. This page is the one place that lists what each level sends and how to control it. The update check is a separate request and stays anonymous; see [Outbound connections](outbound-connections.md) for every call the server makes.

## The three levels

| Level | What is sent |
|---|---|
| Off | Nothing. |
| Basic | The install ID, the version and the update channel: exactly three keys. |
| Detailed | Basic, plus the platform, the theme, the plugins and counts of errors. Described below. |

An install that chose to send stats before the levels existed is on Basic and sends exactly what it sent before. It moves to Detailed only when an owner chooses it.

## What is sent

Once a day, Hatamoto sends one `POST` to `https://hatamoto.dev/p/stats/v1/ping`. Hatamoto checks every hour (the first check 5 to 15 minutes after the server starts) and sends when the last report is more than 20 hours old or the version changed since the last report, so a restart or an update does not delay it by a day. After a version change the report goes out at the next hourly check, but never twice within 30 minutes, so a receiver that is down is tried once per check at most. The request has the user agent `hatamoto-stats` and a 10 second timeout. It sends no cookies and follows no redirects. A development build, whose version is not a release, never sends. A failed request is not retried for 20 hours (or 30 minutes after a version change).

### Basic

The body is JSON with exactly three keys:

```json
{
  "install": "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65",
  "version": "v1.4.2",
  "channel": "stable"
}
```

| Key | Value |
|---|---|
| `install` | A random ID of 32 hexadecimal characters, made when the owner opts in. It is not derived from anything on your server. |
| `version` | The release the server runs. |
| `channel` | The update channel in use: `stable`, `edge` or `canary`. It is whatever you set, also when the update mode is `off`. |

### Detailed

The body has the three Basic keys and these:

```json
{
  "install": "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65",
  "version": "v1.4.2",
  "channel": "stable",
  "level": "detailed",
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
  }
}
```

| Key | Value |
|---|---|
| `level` | Always `detailed`. A Basic body never has it. |
| `os`, `arch` | The operating system and CPU architecture the server runs on, as Go names them (`linux`, `amd64`). |
| `postgres` | The major version of the database, for example `16`. |
| `age_months` | How long the install has existed, rounded down to one of 0, 1, 3, 6, 12, 24 or 36 months (36 means 36 or more). Never the exact date. |
| `worker` | Whether the background worker runs on this server. |
| `theme` | The name and version of the active theme. Always sent by name, whoever made it. |
| `plugins` | The installed plugins that are first-party or published in the registry (community plugins): name, version and whether it is enabled. At most 100, sorted by name. |
| `other_plugins` | A count of all other plugins (self-built, unsigned or signed by another key). Their names never leave the server. |
| `health` | Counts of problems since the last report, see below. |

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

Your site address, content, pages, settings, user names, email addresses, visitor data, IP addresses of visitors, the names of plugins that are not first-party or in the registry, error messages and server details beyond the platform, Postgres major version and worker flag above. The server's own address reaches hatamoto.dev as the source of the connection, as with any request. The update check does not carry the install ID.

## Retention

The receiver keeps one report per install and UTC day: a second report the same day replaces the first. This page does not state how long those daily reports are kept, because no retention period has been published yet; it will be added here when there is one.

## Turn it on, off or reset it

An owner chooses in one of three places, and can change the choice at any time:

- On a new site, the setup wizard has a **Help improve Hatamoto** step after **Look**, with **Off**, **Basic** and **Detailed**, none preselected. See [First run](../getting-started/first-run.md).
- On an install that has not answered yet, a banner asks the owner once, with **No thanks**, **Basic** and **Detailed**. An install on Basic is offered Detailed once, and **Dismiss** ends the offer for good.
- **Settings > Updates** has an **Install stats** section with the three levels, a **What is collected** link to this page, and when it was last sent successfully. Editors do not see the question.

Choosing Basic or Detailed creates the install ID. Choosing Off deletes it, and the health counts with it. While stats are on, **Reset ID** replaces the ID with a new random one, so earlier reports can no longer be linked to later ones.

## Operator override

Two environment variables, both listed in the [Configuration reference](configuration-reference.md#updates):

- `HATAMOTO_TELEMETRY=off` rules out both levels. Nothing is sent, the stored install ID is deleted at start and before every daily run, and the choice is set to off, so removing the variable later does not start sending again without a new answer. The setup step and the banners are left out, and Settings > Updates shows **Turned off by the server configuration.** Any other value changes nothing: the variable can only turn stats off, never on.
- `HATAMOTO_TELEMETRY_URL` replaces the endpoint, for testing. It must be a plain `https://host/path` with no user, query or fragment. An invalid value is ignored with a warning and the default is used.

To skip the daily job without touching the choice, add `stats_ping` to `WORKER_DISABLE_JOBS`. Nothing is scheduled in a public demo.

## Where it is stored

In one row of the site's database, next to the other site settings: the level, the install ID while stats are on, the time of the last successful report, the version it reported (`last_sent_version`), the health counts and the result of the last attempt (`ok`, `error: http <code>` or `error: network`). A failed attempt shows nothing in the admin. All replicas share the row, and a report is sent by one of them at most once a day. The ID and the endpoint are never written to the log. A backup of the database carries the row, so a restore keeps the level and the ID.
