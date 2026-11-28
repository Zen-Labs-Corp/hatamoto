# Staging mode (`HATAMOTO_ENV=staging`)

Staging mode is for a copy of a site (a rehearsal of an upgrade, a theme change or a restore) that must
not talk to the outside world. In staging the server stops every outbound sender it owns, tells search
engines not to index the copy, and shows a banner on every admin page. Unset, nothing changes.

The banner says nothing is sent from this site. That is true of the senders listed under "What is
blocked", not of everything. These stay on in staging: captcha verification (the production captcha
secret and the visitor's token go to the provider), the package registry, fonts, bot IP ranges, WordPress
import, and media uploads and replacements to the configured object storage bucket. See "What stays on".

```sh
HATAMOTO_ENV=staging
```

`HATAMOTO_ENV` accepts `production` (the default, also when unset or empty) and `staging`, in any case.
Any other value (`stage`, `stagin`, a typo) stops the server and every command that loads the configuration with an error
that names the variable and the allowed values, so a mistake cannot start a copy in a mode you did not
mean.

`HATAMOTO_ENV` is separate from `ENVIRONMENT`. `ENVIRONMENT` still decides log format, HSTS and cookie
defaults. A staging copy usually keeps `ENVIRONMENT=production` so it behaves like the real site, and adds
`HATAMOTO_ENV=staging` on top.

At start, `serve` logs one line:

```
staging mode: mail, webhooks, IndexNow, cache purge, off-site backup, updater, plugin outbound HTTP, install statistics, static export to S3, static export over SFTP and media deletions in object storage are off
```

Each blocked send also logs one `staging mode: suppressed` line that names the sender.

## What is blocked

| Sender | In staging | What you see |
|---|---|---|
| Mail (including the SMTP connection check, and mail from compiled-in modules) | Nothing is sent, and the connection check (which would sign in to the copied SMTP server) is skipped | The email log records the message as suppressed. **Send a test email** shows a warning that nothing was sent. Saving the email settings still works and shows a warning that the connection was not checked |
| Webhooks | Nothing is queued and the delivery job does nothing; **Send test** is refused | **Send test** shows a warning and returns to the webhook list |
| IndexNow | Queued URLs are dropped instead of submitted | A log line with the URL count |
| Cache purge (webhook target and registered purgers) | Queued purges are dropped instead of sent to the CDN | A log line with the item count |
| Off-site backup | The whole run is refused (no local backup is taken for it, no upload, no pruning of the bucket); **Test connection** is refused | The run is recorded as failed with the staging message |
| Updater (check and apply) | Skipped, nothing is stored | The on-page **Check** reply says "skipped, staging mode". Nothing is stored, so the page keeps showing whatever it showed before, and **Update now** does nothing |
| Plugin outbound HTTP (`http.fetch`, downloads, import by URL) | Refused | The plugin gets a blocked-fetch error |
| Install statistics | No report is sent | A log line |
| Static export to S3 | The upload is refused (the local directory target still works) | The export reports the staging message |
| Static export over SFTP | The upload is refused, with no connection to the host | The export reports the staging message |
| Media deletions in object storage | The object is kept; the media record is still removed on the copy | A log line with the object key |

Why these: a copy holds the real credentials and the real install identity. Without the stops it would
email customers, call their webhook receivers, overwrite the live static site, delete objects in the
production backup bucket, or report the production install ID as a second install.

## What stays on, and why

| Still works | Why |
|---|---|
| Captcha checks on forms | Forms must keep working in a rehearsal, and a check changes nothing at the provider |
| Package registry, fonts, bot IP ranges, WordPress import | These only read from other servers |
| Single sign-on (OIDC) discovery, key and token calls | They only read from the identity service and change nothing there. The identity service must list the copy's own redirect URL |
| Static export to a local directory | It is local |
| Media uploads and replacements to S3 | Blocking writes would break uploads. They reach the configured bucket, so use a separate one |
| Compiled-in module routes and jobs | They are the module's own code; a module's own outbound calls are not stopped by core |
| Requests the server makes to itself | They never leave the host |

## Before you start a copy

- Give the copy its own `MEDIA_*` bucket (or local storage). This is strongly recommended. Uploads and
  replacements in staging are written to the configured bucket, so a copy that shares the production
  bucket adds and overwrites objects there. Deletions are suppressed (the object stays), but nothing else
  protects the production bucket. `serve` logs a warning at start when staging and S3 media are both set.
- Use a release that has staging mode on the copy. A binary older than staging mode ignores
  `HATAMOTO_ENV` and sends everything, also after a supervisor rollback to such a release. Check for the
  `staging mode:` line at start and the banner in the admin.
- Webhook deliveries that were pending when the copy was taken stay pending. They are not sent in staging,
  but they would be sent if the copy is later switched to production.
- Browser-side tracking tags (the Tags setting) still render on public pages, because the visitor's
  browser sends them, not the server. Remove them from the copy if you do not want staging visits counted.

## Upgrading a staging copy

`hatamoto update apply vX.Y.Z` fetches a release, so it is refused in staging. On a binary or supervisor
install, use the offline path: `hatamoto update apply --file <binary> --record <record> [--sig <file>]`.
With containers, change the image tag.

## Search engines

Every response from the server carries `X-Robots-Tag: noindex` (pages, 404s, feeds, sitemaps, media
served by this server, and the API). The default theme also prints `<meta name="robots" content="noindex">`;
the header covers every theme. Media that a browser fetches straight from an object storage or CDN URL
does not pass through this server, so it carries no header. `robots.txt` is left unchanged
on purpose: a crawler that is blocked by `Disallow: /` never fetches a page, so it never sees the
`noindex`.

## The banner

Every admin page, including the sign-in page and the entity editor, shows a warning that says
"Staging: nothing is sent from this site." It cannot be dismissed.
