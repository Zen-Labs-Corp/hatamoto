# Channel freshness alarm (user timer)

A signed channel manifest is valid for 14 days and re-signed daily. If re-signing
stops, boxes stop updating when it expires. The daily `channels-freshness`
GitHub workflow is the alarm that does not depend on the signing machine; this
timer is the local one, on the signing machine itself. It runs `scripts/check-channel-freshness.sh --min-days 7` against the live
manifests (read-only, no key, no write) once a day and alerts on failure.

Install on the owner's machine (not done by any script in the repository):

```bash
cd /path/to/hatamoto           # a checkout of this repository
install -D -m 0755 scripts/check-channel-freshness.sh ~/.local/bin/hatamoto-check-channel-freshness
install -D -m 0644 deploy/channels/freshness.service ~/.config/systemd/user/hatamoto-channels-freshness.service
install -D -m 0644 deploy/channels/freshness.timer   ~/.config/systemd/user/hatamoto-channels-freshness.timer
install -d -m 0700 ~/.config/hatamoto
# one or both; the file is optional but then a failure is only visible in the journal
umask 077; cat > ~/.config/hatamoto/freshness.env <<'ENV'
ALERT_WEBHOOK_URL=https://example.invalid/hook
ALERT_MAIL=you@example.com
ENV
systemctl --user daemon-reload
systemctl --user enable --now hatamoto-channels-freshness.timer
systemctl --user start hatamoto-channels-freshness.service   # run it once now
systemctl --user status hatamoto-channels-freshness.service --no-pager
systemctl --user list-timers hatamoto-channels-freshness.timer
```

Prove the alarm itself works (the alert fires only on failure):

```bash
systemctl --user edit --runtime hatamoto-channels-freshness.service   # add: [Service]\nExecStart=\nExecStart=%h/.local/bin/hatamoto-check-channel-freshness --min-days 99
systemctl --user start hatamoto-channels-freshness.service; journalctl --user -u hatamoto-channels-freshness -n 20 --no-pager
systemctl --user revert hatamoto-channels-freshness.service
```

`mail` (mailutils/bsd-mailx) is needed only for `ALERT_MAIL`; the webhook needs `curl`.
Keep running the user manager after logout with `loginctl enable-linger $USER`.
