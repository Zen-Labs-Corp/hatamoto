# Admin attack suite

Proves, in headless Chromium against a real binary, that the sealed admin holds. A plugin script
with the `site.scripts` grant runs on public pages and tries every attack below. Each attack must
fail, and each has a positive control (the same probe where success is expected) so a probe that
sees nothing proves nothing.

| Group | Attacks | Run |
|---|---|---|
| Layer A, browser | AT-01 to AT-09: read cookies, credentialed fetch, only-if-cached, iframes (admin, plugin frame, builder canvas, blog preview), `window.open` | admin pinned at `/admin` |
| Layer A, state | AT-10 to AT-15: POST with no, guessed or replayed token, planted recovery copies, service workers | admin pinned at `/admin` |
| Layer A, editors | AT-22: the plugin script tries to select, delete and save from the builder canvas, the blog preview frame and the no-JS preview tab | admin pinned at `/admin` |
| Layer A, cookie auth | AT-03b: AT-03 again with `ENABLE_COOKIE_AUTH=true`; control on the API's own cookie session | admin pinned at `/admin`, cookie auth on |
| Discovery | AT-16 to AT-21: Referer, `/-/edit`, robots and sitemap, error pages, `/admin` against a random path | admin at a random address |
| Layer C | S-1: every well-known scanner path (read from `backend/internal/server/notfound_wellknown.go`) answers the random-path 404 byte for byte, with and without a session | admin at a random address |
| Layer B | B-1 to B-3: sandboxed page has origin `null`, no storage, and its admin fetch carries no Cookie | `hatamoto_qa` build, `HATAMOTO_QA_SANDBOX_ALL=1` |

The fixture plugin is self-built (loaded through `HATAMOTO_PLUGIN_DIR`, no signing). It has the
same origin and the same powers as a first-party plugin, so what stops it is Layer A, not the
plugin policy.

## Run it

```
cd scripts/admin-qa && npm ci          # once: installs playwright-core
PW="$PWD/node_modules/playwright-core" CHROME=/usr/bin/google-chrome bash ../../test/admin-attack/run.sh
```

It builds two binaries, starts a throwaway Postgres (or uses `DATABASE_URL` with
`QA_USE_DATABASE_URL=1`), seeds with `scripts/admin-qa/seed.sh`, and restarts the server four
times on the same data. Set `ATTACK_OUT` to keep the summary and the server logs.

## Output

One line per check:

```
PASS AT-02 credentialed fetch of the admin HTML :: status=404
```

- `PASS`: the attack failed and its control succeeded.
- `HOLE`: the attack worked. This is a product bug. Stop and report it with the request, the
  response and the code that should have blocked it. Never mark it expected to fail and never
  weaken the check.
- `INCONCLUSIVE`: the control failed, so the probe could not have seen success. Fix the probe.

The exit code is 0 only when every line is `PASS`.

## Notes

- Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`. Real installs get a random address
  (`hatamoto admin-url` prints it), so the discovery and scanner checks run a second time on a
  random one: with the pin at `/admin` the address filter lets everything under `/admin` through.
- The not-found throttle is switched off (`HATAMOTO_NOTFOUND_PER_MIN=0`) and no proxy headers are
  sent, so the 400 scanner requests do not depend on how the client address is read.
- The Layer B check needs the pin at `/admin` because the QA capture (`/__qa/seen`) records only
  paths that start with `/admin`.
- `scripts/admin-qa/**` is used read only (`qa-env.sh`, `seed.sh`).
