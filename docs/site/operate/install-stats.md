---
title: Install stats
nav_title: Install stats
description: The optional anonymous install count: what it sends, what it never sends, how an owner turns it on, off or resets it, and how an operator rules it out.
---

Install stats are an anonymous count of running Hatamoto installs and the release they run. They are off until an owner opts in: nothing is sent, and no install ID exists, before that. This page is the reference for what is sent and how to control it. The update check is a separate request and stays anonymous; see [Outbound connections](outbound-connections.md) for every call the server makes.

## What is sent

Once a day (give or take an hour), the first time 5 to 15 minutes after the server starts, Hatamoto sends one `POST` to `https://hatamoto.dev/p/stats/v1/ping`. The body is JSON with exactly three keys:

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

The request has the user agent `hatamoto-stats` and a 10 second timeout. It sends no cookies and follows no redirects. A development build, whose version is not a release, never sends. A failed request is not retried before the next day.

## What is not sent

Your site address, content, pages, settings, user names, email addresses, visitor data, IP addresses of visitors, plugin lists and server details. The server's own address reaches hatamoto.dev as the source of the connection, as with any request. The update check does not carry the install ID.

## Turn it on, off or reset it

An owner chooses in one of three places, and can change the choice at any time:

- On a new site, the setup wizard has a **Help improve Hatamoto** step after **Look**. **No thanks** and **Send stats** are the two answers. See [First run](../getting-started/first-run.md).
- On an install that has not answered yet, a banner asks the owner once. **No thanks** or **Send stats** answers it, and **See what is sent** opens the section below.
- **Settings > Updates** has an **Install stats** section with a **Send install stats** switch, the exact request body, and when it was last sent. Editors do not see the question.

Choosing **Send stats** creates the install ID. Switching off deletes it. While it is on, **Reset ID** replaces the ID with a new random one, so earlier reports can no longer be linked to later ones. Until an owner opts in, the preview shows an example ID.

## Operator override

Two environment variables, both listed in the [Configuration reference](configuration-reference.md#updates):

- `HATAMOTO_TELEMETRY=off` rules install stats out. Nothing is sent, the stored install ID is deleted at start and before every daily run, and the choice is set to off, so removing the variable later does not start sending again without a new answer. The setup step and the banner are left out, and the switch in Settings > Updates shows **Turned off by the server configuration.** Any other value changes nothing: the variable can only turn stats off, never on.
- `HATAMOTO_TELEMETRY_URL` replaces the endpoint, for testing. It must be a plain `https://host/path` with no user, query or fragment. An invalid value is ignored with a warning and the default is used.

To skip the daily job without touching the choice, add `stats_ping` to `WORKER_DISABLE_JOBS`. Nothing is scheduled in a public demo.

## Where it is stored

In one row of the site's database, next to the other site settings: the choice, the install ID while it is on, and the time and result of the last report (`ok`, `error: http <code>` or `error: network`). All replicas share it, and a report is sent by one of them at most once a day. The ID and the endpoint are never written to the log. A backup of the database carries the row, so a restore keeps the choice and the ID.
