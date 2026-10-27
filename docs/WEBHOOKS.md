# Webhooks

Hatamoto can tell another system when content changes. A webhook is a URL you
choose; Hatamoto sends it a signed `POST` for the events you pick, retries
failures and keeps a delivery log. Webhooks are built into core
(`backend/internal/webhook`). They used to be a first-party plugin; see
[What changed](#what-changed) and [Upgrading](#upgrading).

Configure them in the admin under **Settings > Webhooks** (owner only): add,
edit, pause, send a test, replace the secret, delete, and read the delivery log,
where a failed delivery can be retried by hand. There is no REST API for
webhooks.

## Events and payload

| Event | Payload `data` |
|---|---|
| `entity.created` | `entity_id`, `entity_name`, `schema_slug`, `locale` |
| `entity.updated` | `entity_id`, `entity_name`, `version`, `locale` |
| `entity.published` | `entity_id`, `entity_name`, `locale` |
| `entity.deleted` | `entity_id`, `entity_name`, `schema_slug`, `locale` |
| `form.submitted` | `form_id`, `form_slug`, `submission_id` (never the fields) |
| `import.completed` | `created`, `failed`, `source`, `updated` |

`form.submitted` carries ids only, because submitted fields are personal data;
read the submission through the API if you need it. An import fires
`import.completed` once per batch and no per-entity events. "Send test" delivers
a synthetic `webhook.test` event (`data.message`) that cannot be subscribed to.
Changes made through the CLI fire no hooks, so they send nothing.

Every request is a `POST` with `Content-Type: application/json` and the body

```json
{"id": "<delivery id>", "event": "entity.published", "timestamp": "2026-10-15T01:02:03Z", "data": {...}}
```

## Headers

| Header | Value |
|---|---|
| `X-Webhook-ID` | the subscription id |
| `X-Webhook-Event` | the event name |
| `X-Webhook-Delivery` | the delivery id; a retry repeats it |
| `X-Webhook-Timestamp` | time of this attempt (RFC 3339); not signed |
| `X-Webhook-Signature` | `sha256=<hex>`: HMAC-SHA256 of the exact body |
| `X-Webhook-Signature-V2` | `t=<unix>,v1=<hex>`: HMAC-SHA256 of `<unix>.<body>` |
| `User-Agent` | `Hatamoto-Webhook/2.0` |

Both signatures are keyed by the subscription's secret (`whsec_...`, shown once
when the subscription is created or its secret replaced). A subscription may add
up to 20 custom headers, but not `Host`, `Content-Type`, `Content-Length`,
`User-Agent` or hop-by-hop headers, and never the signature headers (those are
added last).

## Verifying signatures

`X-Webhook-Signature` proves who sent the body, not when. `X-Webhook-Signature-V2`
signs a timestamp too: verify it and reject a `t` older than five minutes to
defeat replays. Delivery is at least once, so also drop duplicates by
`X-Webhook-Delivery`. Always compare in constant time, over the raw body bytes.

Go:

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

Node:

```js
import { createHmac, timingSafeEqual } from "node:crypto";

export function verifyV2(secret, rawBody, header, nowSeconds = Date.now() / 1000) {
  const m = /^t=(\d+),v1=([0-9a-f]+)$/.exec(header ?? "");
  if (!m || Math.abs(nowSeconds - Number(m[1])) > 300) return false;
  const want = createHmac("sha256", secret).update(`${m[1]}.`).update(rawBody).digest();
  const got = Buffer.from(m[2], "hex");
  return got.length === want.length && timingSafeEqual(got, want);
}
```

For the older header, HMAC the body alone and compare with
`sha256=<hex>` the same way.

## Delivery, retries and backoff

An event writes one row per matching subscription, in one statement and with no
network call, so a slow receiver or a burst of events (bulk publish, an import)
never delays or drops an event. The `webhook_deliver` job runs every 10 seconds
(with a little jitter) and sends due rows one at a time, up to 100 per run and
within a time budget of 25 seconds. Each request's timeout is the subscription's
(1 to 5 seconds, default 5), cut to the time left in the run. A subscription whose
request got no answer is skipped for the rest of the run, so one hung receiver
cannot starve the others.

Any answer other than 2xx, or a network error, counts as a failed attempt. The
next attempt comes after `retry_delay_seconds * 2^attempt` (`attempt` being the
number of attempts made so far), at most one hour, up to `max_retries` retries
(0 to 10, default 3; delay 0 to 3,600 seconds, default 60). After that the
delivery is `failed`. Claiming is atomic and leases the row for 60 seconds; if
the process dies mid-request, the attempt counts when the lease expires and the
request may be sent twice. Delivery is at least once and nothing more is
promised about timing.

A subscription with 5,000 pending deliveries gets no new ones until it drains.
Pausing a subscription marks its queued deliveries `failed`; deleting it removes
them, with its log.
At most 50 subscriptions can exist.

The log stores the receiver's status and the first 1,024 bytes of its answer.

## Safety

- URLs must be `https://` and carry no credentials. Plain `http://` is refused
  unless the subscription has "allow plain http" ticked; the payload and its
  signature then travel unencrypted, and the list marks it "not encrypted".
- Private, loopback and link-local addresses are blocked at connection time.
- Only ports 80 and 443 are allowed.
- Redirects are followed, up to 5, and every hop gets the same checks. A redirect
  from https to http is refused unless the subscription allows plain http.
- The request timeout is at most 5 seconds.

## Retention

Delivered rows are kept 30 days, every other row 90 days (a pending row older
than that is abandoned), and at most 1,000 finished rows per subscription. An
hourly job (`webhook_prune`) enforces this.

## Secrets

Secrets are stored unencrypted in the database so Hatamoto can sign each request.
Like all of Hatamoto's data, they are readable by anyone with the database or its
backups. They are shown once; edit does not reveal them, "New secret" replaces
them and the old one stops working at once.

## What changed

Relative to the first-party webhooks plugin, receivers see no difference in the
body, headers, signatures or secrets. Delivery can be faster: the plugin sent up
to a minute after the event (its cron ran every minute), while the core job runs
every 10 seconds. Delivery is still at least once, so keep deduplicating by
`X-Webhook-Delivery`.

## Upgrading

On the first start after upgrading, Hatamoto copies the plugin's subscriptions
(same ids, URLs, events, headers, retry settings and secrets) into core and then
uninstalls the plugin with its data. Nothing needs to be done, and receivers keep
verifying with the same secret. Subscriptions still waiting in the old hand-over
table are copied by the database migration. Pending and past deliveries are not
carried over, and events fired before the first start of the new version are not
delivered. If the copy fails, the plugin is kept and the copy retried at the next
start. Imported values are checked like new ones: retries and delay are clamped,
the timeout is set to 1 to 5 seconds, headers Hatamoto owns are dropped, and a
subscription left with no events or an unusable URL is imported paused.
Subscriptions are also imported paused when the plugin was not really sending:
disabled, not installed, signed by a key this instance no longer trusts, or not
granted all of `db`, `http.fetch` and `http.fetch.any`. Rows waiting in the old
hand-over table keep their active flag only when they were created within the
hour before the migration ran, which means the same upgrade also ran the v1-19
step (a direct jump from the core that still sent webhooks itself, so they were
live a moment ago). Older rows waited for a plugin that never imported them,
nothing has delivered them, and they are imported paused.
Resume paused ones in Settings > Webhooks.

Only the first-party plugin is moved: a package named `webhooks` signed by one
of the first-party keys (the current one, or the one retired on 2026-10-16, see
PACKAGES.md). Any other plugin by that name is left alone: not imported, not
uninstalled, one warning in the log. That includes an unsigned one, a third-party
one, and the real plugin when you sign your own registry (for example with
`HATAMOTO_TRUST_FIRST_PARTY=false`), because the signer then is you. Settings >
Webhooks shows a notice while such a plugin is installed. To move: recreate the
subscriptions in Settings > Webhooks (new ids and secrets, so receivers need the
new secret), then uninstall the plugin from Plugins. Installing or updating a
plugin named `webhooks` is refused (the name is reserved), but uninstalling one
works. Until you do, the plugin and core would both send.

Rolling the migration back is refused while any webhook exists.

## Operating

- `WORKER_DISABLE_JOBS=webhook_deliver` (comma-separated list, with the other job
  names) turns sending off; `webhook_prune` turns pruning off.
- `HATAMOTO_WORKER=off` runs no jobs at all. Events are still queued, up to the
  5,000 pending per subscription, and nothing is sent until a worker runs. A
  startup log line says so.
- `WORKER_DRAIN=true` starts no jobs: events queue (up to 5,000) and nothing is
  sent.
- In a multi-replica setup run the worker on one replica; delivery is safe with
  several (rows are claimed atomically) but wasteful.
