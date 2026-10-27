# Public endpoint monitor

`check.sh` is a black-box check of what clients depend on. It needs only `bash`,
`curl`, `jq` and GNU `date`, and exits non-zero with one `FAIL:` line per problem.

| Check | Fails when |
|---|---|
| `/releases/channels/{stable,edge,canary}.json` | not 200, not JSON, no `.index.expires_at`, or it expires in fewer than `EXPIRY_DAYS` (7) days. Manifests are valid 14 days and re-signed daily, so under 7 days left means signing stopped. |
| `/registry.json` | not 200, or no `.index.packages` |
| `/sdk?go-get=1` | not 200, or no `go-import` tag for `<host>/sdk` |
| `/.well-known/security.txt` | not 200, no `Contact`, or `Expires` in fewer than `SECURITY_TXT_DAYS` (30) days |

Run it anywhere: `deploy/monitor/check.sh` (default `BASE_URL=https://hatamoto.dev`).
Optional environment: `ALERT_WEBHOOK_URL` (POSTs `{"text": ...}` on failure; works with
Slack/Discord-style incoming webhooks), `HEALTHCHECK_URL` (healthchecks.io style: pinged on
success, `<url>/fail` on failure, so a monitor that stops running is noticed too).

Run the monitor from somewhere other than the site's own host: a check on the box it watches
dies with the box.

## Option 1: systemd user timer (any machine you own)

```bash
install -m 0755 deploy/monitor/check.sh /usr/local/bin/hatamoto-monitor    # sudo
mkdir -p ~/.config/systemd/user ~/.config/hatamoto
cp deploy/monitor/hatamoto-monitor.{service,timer} ~/.config/systemd/user/
install -m 0600 /dev/null ~/.config/hatamoto/monitor.env
echo 'ALERT_WEBHOOK_URL=https://...' >> ~/.config/hatamoto/monitor.env     # your webhook
systemctl --user daemon-reload
systemctl --user enable --now hatamoto-monitor.timer
loginctl enable-linger "$USER"      # keep it running when logged out
systemctl --user start hatamoto-monitor.service && journalctl --user -u hatamoto-monitor -n 20
```

Prove the alert fires without breaking anything: the live manifests have about 14 days left, so
`EXPIRY_DAYS=30 ALERT_WEBHOOK_URL=... deploy/monitor/check.sh` must exit 1 and post to the webhook.

## Option 2: an external monitor (no machine to look after)

- **healthchecks.io** (or any cron-monitor): create a check with a 15 minute period and 10 minute
  grace, put its URL in `HEALTHCHECK_URL`, and run Option 1 or a scheduled GitHub Actions workflow
  on a different repo/host that runs `check.sh`. Missing pings alert you.
- **Uptime Kuma / UptimeRobot / BetterStack**: HTTP(s) monitors, 5 minute interval, alert on non-200:
  `https://hatamoto.dev/releases/channels/stable.json`, `https://hatamoto.dev/registry.json`,
  `https://hatamoto.dev/sdk?go-get=1`, `https://hatamoto.dev/.well-known/security.txt`. Add a
  keyword monitor on `stable.json` for `"channel": "stable"`. They cannot compute expiry, so
  keep `check.sh` as well for the `expires_at` rule.

## Test

`deploy/monitor/test-check.sh` serves fixtures from a local Caddy: fresh, expiring, expired,
malformed JSON, bad date, missing registry, bad go-import, expiring security.txt, unreachable
origin, and that the alert webhook is called.
