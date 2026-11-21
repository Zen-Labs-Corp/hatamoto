# Staging mode (`HATAMOTO_ENV=staging`)

Staging mode is for a copy of a site (a rehearsal of an upgrade, a theme change or a restore) that must
not talk to the outside world. In staging the server stops every outbound sender it owns, tells search
engines not to index the copy, and shows a banner on every admin page. Unset, nothing changes.

```sh
HATAMOTO_ENV=staging
```

`HATAMOTO_ENV` accepts `production` (the default, also when unset or empty) and `staging`, in any case.
Any other value (`stage`, `stagin`, a typo) stops the server and every command at start with an error
that names the variable and the allowed values, so a mistake cannot start a copy in a mode you did not
mean.

`HATAMOTO_ENV` is separate from `ENVIRONMENT`. `ENVIRONMENT` still decides log format, HSTS and cookie
defaults. A staging copy usually keeps `ENVIRONMENT=production` so it behaves like the real site, and adds
`HATAMOTO_ENV=staging` on top.

At start, `serve` logs one line:

```
staging mode: mail, webhooks, IndexNow, off-site backup, updater, plugin outbound HTTP, install statistics and static export to S3 are off
```

Each blocked send also logs one `staging mode: suppressed` line that names the sender.

## What is blocked

| Sender | In staging | What you see |
|---|---|---|
| Mail (including the SMTP **Test connection** button) | Nothing is sent | The email log records the message as suppressed; the test button reports "staging mode: nothing is sent from this site" |
| Webhooks | Nothing is queued and the delivery job does nothing; **Send test** is refused | The test button reports the same message |
| IndexNow | Queued URLs are dropped instead of submitted | A log line with the URL count |
| Off-site backup | The whole run is refused (no local backup is taken for it, no upload, no pruning of the bucket); **Test connection** is refused | The run is recorded as failed with the staging message |
| Updater (check and apply) | Skipped, nothing is stored | Settings > Updates shows "skipped, staging mode" |
| Plugin outbound HTTP (`http.fetch`, downloads, import by URL) | Refused | The plugin gets a blocked-fetch error |
| Install statistics | No report is sent | A log line |
| Static export to S3 | The upload is refused (the local directory target still works) | The export reports the staging message |

Why these: a copy holds the real credentials and the real install identity. Without the stops it would
email customers, call their webhook receivers, overwrite the live static site, delete objects in the
production backup bucket, or report the production install ID as a second install.

## What stays on, and why

| Still works | Why |
|---|---|
| Captcha checks on forms | Forms must keep working in a rehearsal, and a check changes nothing at the provider |
| Package registry, fonts, bot IP ranges, WordPress import | These only read from other servers |
| Static export to a local directory | It is local |
| Media stored in S3 | Blocking writes would break uploads |
| Requests the server makes to itself | They never leave the host |

## Before you start a copy

- Give the copy its own `MEDIA_*` bucket (or local storage). Otherwise uploads in staging write to the
  production bucket.
- Webhook deliveries that were pending when the copy was taken stay pending. They are not sent in staging,
  but they would be sent if the copy is later switched to production.
- Browser-side tracking tags (the Tags setting) still render on public pages, because the visitor's
  browser sends them, not the server. Remove them from the copy if you do not want staging visits counted.

## Search engines

Every response from the server carries `X-Robots-Tag: noindex` (pages, 404s, feeds, sitemaps, media and
the API), and public pages print `<meta name="robots" content="noindex">`. `robots.txt` is left unchanged
on purpose: a crawler that is blocked by `Disallow: /` never fetches a page, so it never sees the
`noindex`.

## The banner

Every admin page, including the sign-in page and the entity editor, shows a warning that says
"Staging: nothing is sent from this site." It cannot be dismissed.
