# Release hooks for the changelog page

The release skill (`release-hatamoto`) lives outside this repository, so these
are instructions for its `release.sh` and `SKILL.md`, not patches. Prerequisite:
the site is bootstrapped and `.hatamoto-changelog.env` exists (see
[CHANGELOG_SITE.md](CHANGELOG_SITE.md)).

## One command: `refresh`

```
cd backend
go run ./tools/changelog refresh [--dry-run]
```

`refresh` walks every `vX.Y.Z` tag and makes its entry match the live, signed
channel manifests (`$HATAMOTO_URL/releases/channels/{canary,edge,stable}.json`,
or `HATAMOTO_RELEASES_URL`):

- a tag live on some channel and missing from the page gets an entry (the PR
  list since the previous tag; a first release gets a placeholder summary);
- the badge is the furthest channel whose ramp has **started** and that is not
  **halted** there (installs are not offered a halted entry), so a release
  scheduled with `canary now, edge +2d, stable +7d` reads canary today and moves
  on as the ramps start, and a halted one stays where it was stopped;
- `yanked` follows the manifests (yanked on any channel = yanked). The manifests
  win: a hand `sync --yanked` / `--unyank` is undone by the next refresh;
- notes are regenerated only while nobody has edited them; an edited entry only
  ever gets its badge, yanked mark and (if empty) date refreshed;
- a tag not live anywhere yet (or halted everywhere) is skipped;
- one failing version is reported and the rest still run (exit 1 at the end);
  finding no channel manifest at all is an error, not "nothing released".

It is idempotent and reads the manifests without the API key, so it can run as
often as you like. Because a scheduled ramp starts later without any command
running, something has to call `refresh` after the start times too: the hooks
below cover every release command plus `status`, and a daily timer covers the
rest.

For one version by hand: `sync vX.Y.Z --channel auto` (or an explicit
`canary|edge|stable`, `--yanked`, `--unyank`).

## release.sh

One phase. A changelog failure must not fail a release that already shipped,
so it warns and prints the retry:

```bash
p_changelog() { # refresh the /changelog page from the live manifests; never fails the release
  (cd "$REPO/backend" && go run ./tools/changelog refresh) \
    || printf 'WARN: changelog refresh failed; retry: (cd %s/backend && go run ./tools/changelog refresh)\n' "$REPO" >&2
}
```

Call `p_changelog` (no arguments) at the end of: `p_channel` (both the
`already on` branch and after the live check), `p_schedule`, `p_mod`
(halt/unhalt/yank), `p_all` and `p_status`.

## Daily timer

A scheduled ramp starts with nobody watching, so run `refresh` daily as well
(a systemd user timer on the release machine, `Persistent=true` so a missed run
happens at the next boot):

```
# ~/.config/systemd/user/hatamoto-changelog.service
[Service]
Type=oneshot
WorkingDirectory=%h/src/hatamoto/backend
ExecStartPre=/usr/bin/git -C %h/src/hatamoto fetch -q --tags
ExecStart=/usr/bin/env go run ./tools/changelog refresh

# ~/.config/systemd/user/hatamoto-changelog.timer
[Timer]
OnCalendar=*-*-* 01:15:00 UTC
Persistent=true
[Install]
WantedBy=timers.target
```

`systemctl --user daemon-reload && systemctl --user enable --now hatamoto-changelog.timer`

## SKILL.md

- **A (cut):** "the changelog entry appears when the release goes live on a
  channel (`p_changelog` runs after `channel`/`schedule`); if it printed WARN,
  run the retry".
- **B (promote):** "the badge follows the manifests: it moves when the ramp
  starts, via `status`, the next release command or the daily timer".
- **C (halt/yank):** "yank marks the entry yanked; a halt stops the badge moving
  further (it stays on the furthest channel where the release is not halted)".
- **Known gaps:** not run from CI (no key there); an entry edited in the CMS is
  never regenerated, only its badge, yanked mark and empty date are refreshed.
