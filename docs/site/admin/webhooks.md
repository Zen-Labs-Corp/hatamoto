---
title: Webhooks
nav_title: Webhooks
description: Let your own systems know when content changes: set up a webhook, check its signature, and see how delivery, retries and limits work.
---

A **webhook** is a web address you choose that Hatamoto calls when something happens on your site, such as a page being published. Each call is a signed `POST`: the **signature** proves the call came from your site. Hatamoto retries failed calls and keeps a delivery log. This page lists every event, header, limit and rule; it is the companion to [Invite people and create API keys](users-and-api-keys.md), the other way to connect your site to other systems.

Webhooks are built into the server, so there is no plugin to install and no permission to grant. They used to be a first-party plugin; see [Upgrading from the plugin](#upgrading-from-the-plugin).

## Set one up

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Webhooks are owner-only. There is no REST API for them.

1. Open **Settings > Webhooks** (`<admin>/settings/webhooks`). The section has two tabs: **Subscriptions** and **Delivery log** (`<admin>/settings/webhooks/deliveries`).
2. Press **New webhook**. It opens a dialog, or the page `<admin>/settings/webhooks/new` without JavaScript.
3. Give it a name (up to 120 bytes), an address, one or more events and, if you need them, custom headers and retry settings. Save it.
4. Copy the signing secret (`whsec_...`). It is shown **once**, in a banner above the list, right after the webhook is created.
5. Use **Send test** on the webhook's row and check that your receiver got the call.

A row's menu offers **Edit**, **Pause** or **Resume**, **Send test**, **New secret** and **Delete**. Edit does not reveal the secret. New secret replaces it and the old one stops working at once. Delete removes the webhook with its delivery log, after a confirmation. The **Delivery log** lists each delivery with its status, and a failed one has a **Retry** action.

## Limits

| Item | Limit |
|---|---|
| Webhooks per site | 50 |
| Custom headers | 20 per webhook. Never `Host`, `Content-Type`, `Content-Length`, `User-Agent` or hop-by-hop headers. The signature headers are always set last, so a custom one with the same name is replaced. |
| Retries | 0 to 10 (default 3) |
| Retry delay | 0 to 3,600 seconds (default 60) |
| Request timeout | 1 to 5 seconds (default 5) |

## Events and payload

| Event | Payload `data` |
|---|---|
| `entity.created` | `entity_id`, `entity_name`, `schema_slug`, `locale` |
| `entity.updated` | `entity_id`, `entity_name`, `version`, `locale` |
| `entity.published` | `entity_id`, `entity_name`, `locale` |
| `entity.deleted` | `entity_id`, `entity_name`, `schema_slug`, `locale` |
| `form.submitted` | `form_id`, `form_slug`, `submission_id` (never the fields) |
| `import.completed` | `created`, `failed`, `source`, `updated` |

`form.submitted` carries ids only, because submitted fields are personal data. An import sends one `import.completed` per batch and no per-entity events. **Send test** delivers a synthetic `webhook.test` event that cannot be subscribed to. Changes made with the CLI fire no events, so they send nothing.

Every request is a `POST` with `Content-Type: application/json` and this body:

```json
{"id": "<delivery id>", "event": "entity.published", "timestamp": "2026-10-15T01:02:03Z", "data": {}}
```

| Header | Value |
|---|---|
| `X-Webhook-ID` | the webhook's id |
| `X-Webhook-Event` | the event name |
| `X-Webhook-Delivery` | the delivery id; a retry repeats it |
| `X-Webhook-Timestamp` | time of this attempt (RFC 3339); not signed |
| `X-Webhook-Signature` | `sha256=<hex>`: HMAC-SHA256 of the exact body |
| `X-Webhook-Signature-V2` | `t=<unix>,v1=<hex>`: HMAC-SHA256 of `<unix>.<body>` |
| `User-Agent` | `Hatamoto-Webhook/2.0` |

## Verifying signatures

Both signatures are keyed by the webhook's secret. `X-Webhook-Signature` proves who sent the body, not when. `X-Webhook-Signature-V2` signs a timestamp too: verify it and reject a `t` older than five minutes to defeat replays. Delivery is at least once, so also drop duplicates by `X-Webhook-Delivery`. Compare in constant time, over the raw body bytes.

```go
func verifyV2(secret string, body []byte, header string, now time.Time) bool {
	// header: "t=<unix>,v1=<hex>"
	t, sig, ok := strings.Cut(header, ",v1=")
	if !ok || !strings.HasPrefix(t, "t=") {
		return false
	}
	unix, err := strconv.ParseInt(strings.TrimPrefix(t, "t="), 10, 64)
	if err != nil || now.Sub(time.Unix(unix, 0)).Abs() > 5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(unix, 10) + "."))
	mac.Write(body)
	return hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil))))
}
```

For the older header, HMAC the body alone and compare with `sha256=<hex>` the same way.

## Delivery, retries and retention

An event writes one row per matching webhook with no network call, so a slow receiver or a burst of events never delays or drops an event. The `webhook_deliver` job runs every 10 seconds (with a little jitter) and sends due rows one at a time, up to 100 per run within 25 seconds. A webhook whose request got no answer is skipped for the rest of the run, so one hung receiver cannot starve the others.

Any answer other than 2xx, or a network error, is a failed attempt. The next attempt comes after `retry_delay_seconds * 2^attempt` (`attempt` being the number of attempts made so far, counting the one that has failed, so the first retry comes after twice the delay), at most one hour, up to the webhook's retry count. After that the delivery is `failed`. A claimed row is leased for 60 seconds, so if the process dies mid-request the request may be sent twice. A webhook with 5,000 pending deliveries gets no new ones until it drains. Queued deliveries of a paused webhook are marked `failed` when the delivery job next reaches them.

The log stores the receiver's status and the first 1,024 bytes of its answer. An hourly job (`webhook_prune`) keeps delivered rows 30 days, every other row 90 days, and at most 1,000 finished rows per webhook.

## Safety

- Addresses must be `https://` and carry no credentials. Plain `http://` is refused unless the webhook has "allow plain http" ticked. The payload and its signature then travel unencrypted, and the list marks the webhook "not encrypted".
- Private, loopback and link-local addresses are blocked when the connection is made, and only ports 80 and 443 are allowed.
- Up to 5 redirects are followed and every hop gets the same checks. A redirect from https to http is refused unless the webhook allows plain http.
- Secrets are stored unencrypted in the database so Hatamoto can sign each request. Anyone with the database or its backups can read them.

## Operating

- `WORKER_DISABLE_JOBS=webhook_deliver` turns sending off and `webhook_prune` turns pruning off. See [Background work](../architecture/worker-and-background.md).
- With `HATAMOTO_WORKER=off` or `WORKER_DRAIN=true` no job runs: events are still queued (up to 5,000 per webhook) and nothing is sent. A startup log line says so.
- With several replicas, run the worker on one. Delivery is safe with more, because rows are claimed atomically, but wasteful.

## Upgrading from the plugin

Run `hatamoto migrate` (or start the new version, which migrates). On the first start Hatamoto copies the plugin's webhooks (same ids, URLs, events, headers, retry settings and secrets) into core and uninstalls the plugin with its data. Receivers keep verifying with the same secret and see the same bodies and headers. Pending and past deliveries are not copied, and events fired before the new version first starts are not sent, and a copy that fails is retried at the next start with the plugin kept.

Webhooks are imported **paused** when the plugin was not really sending: disabled, signed by a key this instance no longer trusts, not installed, or not granted all of `db`, `http.fetch` and `http.fetch.any`. So is a webhook with no events or an unusable address. Resume them in Settings > Webhooks.

Webhooks left behind by an earlier upgrade that you never installed the plugin for are copied by the database migration: they stay active if you upgrade directly from a release that still sent webhooks itself, otherwise they arrive paused.

Only the first-party plugin named `webhooks` is moved. Any other plugin with that name (unsigned, third-party, or signed by your own registry key) is left alone, with one warning in the log and a notice on the Webhooks page. Recreate its webhooks there (new ids and secrets), then uninstall the plugin. The name is reserved, so installing or updating such a plugin is refused. A rolling deploy should stop the old instances first, and a migration rollback is refused while any webhook exists. A binary-only rollback to a release that still has the plugin stops all webhooks, because they now live in core and the plugin is gone: reinstalling it gives an empty one.
