# Demo mode (`HATAMOTO_DEMO=1`)

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

Demo mode turns an instance into a showcase that anyone can sign in to and reset. It is for a
site you run yourself so visitors can try the admin. Unset, nothing changes.

```sh
HATAMOTO_DEMO=1
HATAMOTO_DEMO_LOGIN_EMAIL=demo@example.com         # shown on the sign-in page, demo mode only
HATAMOTO_DEMO_LOGIN_PASSWORD=<at least 12 chars>   # taken from the environment; never built in
```

`HATAMOTO_DEMO` is on for `1`, `true`, `yes` or `on`, and off for `0`, `false`, `no`, `off` or
unset/empty (any case). Any other value (`enabled`, `2`, a typo) stops the server at start with
an error, so a mistake cannot start an instance in a mode you did not mean.

## What it refuses

Every refusal answers `403` with a message (an error page, or a toast for an htmx request), never a 500.

| Action | Refused because |
|---|---|
| Upload a plugin or theme (`<admin>/plugins/upload`), allow unsigned packages | uploads run code of unknown origin |
| Install anything that is not signed by the first-party key (any source, any API) | only first-party packages |
| Create, invite, edit, delete or reset users (admin and `/api/v1/users`) | the next visitor must not be locked out |
| Change a password or an email address (`<admin>/profile/password`, forgot, reset, accept, `PATCH /api/v1/me`) | same |
| Create or regenerate API keys | |
| Webhooks, email (SMTP) and captcha (spam) settings, WordPress import | they point at other servers |
| Redirects to another site (the redirect target must be a path on the site) | otherwise the demo is an open redirector on your domain |
| Backups (create, delete, download) | they copy the whole site to disk |
| Updates: no check, no apply, the Updates page says so | the demo is replaced by deployment, not by itself |

Also off: plugins' `http.fetch` (the host function answers "unavailable"), webhook delivery, and all
outbound mail (it sets the existing `EMAIL_DISABLED=true` switch). The unauthenticated admin screens
get a 1 request per second limit (burst 5) instead of 5.

## Disk limits

A visitor must not be able to fill the disk the demo shares with other things. The hard bound is
infrastructure: the demo has its **own Postgres cluster whose data dir is on a size-capped
filesystem**, together with its uploads (`docs/deploy/public-demo.md`, steps 2 and 3). The settings
below keep one visitor from using all of it, and the hourly reset empties it.

| Setting | Default | Effect |
|---|---|---|
| `HATAMOTO_DEMO_MEDIA_QUOTA_MB` | 200 | total size of the media directory (originals and image variants); an upload that would pass it is refused (`507` on the API) |
| `HATAMOTO_DEMO_WRITE_BUDGET_MB` | 150 | bytes a signed-in session may write until the next restart (and, separately, a client address: IPv6 counts as its /64); that key's next write answers `429` with a message |
| `HATAMOTO_DEMO_WRITES_PER_SECOND` | 2 | write requests (anything but GET/HEAD/OPTIONS) per second and address (IPv6: per /64), burst 20; then `429` |

How the write budget works: it charges only requests of a valid session, by the bytes the server
actually reads from the body (not the declared `Content-Length`), to that session and to the client
address. A key whose budget is spent is refused its own writes and nobody else's. Sign-in, sign-out and
every read are never refused for budget, and an anonymous request (the contact form, a flood of
fake POSTs) spends nothing, so anonymous traffic cannot use up the budget of a signed-in visitor or
lock anyone out; anonymous writes only meet the public per-address rate limits. Restoring an entity
version writes a whole snapshot for a request of a few bytes, so it has its own limit: 3, then one
every 5 seconds per session. The entity-version prune runs every 10 minutes (instead of daily), so
old versions cannot pile up between two resets.

The limits are per process, so the hourly restart gives every visitor a fresh budget. The shared
demo login is never locked out by failed sign-ins (the per-address limit still applies), so nobody
can keep everyone else out.

Registry installs with the first-party restriction refuse a non-first-party entry before downloading it.

The list of refused routes is one table, `demoRules` in `backend/internal/config/demo.go`. A route
that is not in it is allowed, so a new route that sends data out must be added there. Rules match the
decoded request path with regular expressions, not the route pattern the router picked; ids are
matched with `.+` (not `[^/]+`) so an encoded slash inside an id cannot slip past a rule. Rules only
over-match (they deny more), never under-match.

## What it shows

A fixed notice, "Public demo, resets every hour", in the admin (a warning callout above the page)
and as a small strip at the foot of every public page, and the demo login on the sign-in page.

## Resetting

`deploy/demo/reset.sh` puts a demo back to its seeded state (database copied from a template
database, data directory refilled from a template copy); `scripts/demo-seed.sh` builds that seed.
`deploy/demo/` holds a systemd unit and an hourly timer, a Caddy block and the database setup.
Run them only on a machine and database that exist for the demo.
