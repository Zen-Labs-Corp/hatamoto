# Contributing to Hatamoto

Thanks for helping. Hatamoto is one Go binary (site, admin, API, worker) plus
Postgres. There is no Node toolchain to install for the product itself.

## What you need

- **Go 1.27** (the exact toolchain is pinned by the `toolchain` line in
  `backend/go.mod`; with `GOTOOLCHAIN=auto`, the default, `go` fetches it).
- **PostgreSQL 16 or 18** (CI tests both) for the integration tests and for running the server
  (`docker compose up db` starts one on `127.0.0.1:5432`).
- `git`, `bash`, `make`, `jq`, `curl` and [Caddy](https://caddyserver.com/) 2 for the
  repository checks (`make check` runs the Caddy splice test; `REAL_CADDY=/path/to/caddy`
  points it at a binary that is not on `PATH`).
- Optional: Chrome and Node 20 for the admin browser checks in `scripts/admin-qa`
  (only needed when you change admin templates, CSS or JS).

## Build and test from a fresh clone

```sh
git clone https://github.com/Zen-Labs-Corp/hatamoto hatamoto
cd hatamoto

# build the binary
cd backend && go build -o bin/hatamoto ./cmd/hatamoto && cd ..

# unit tests (no database)
cd backend && go vet ./... && go test ./... && cd ..

# the plugin SDK is its own module
cd backend/pkg/sdk && go vet ./... && go test ./... && cd ../../..

# integration tests (Postgres)
docker compose up -d db
export DATABASE_URL='postgres://hatamoto:hatamoto@localhost:5432/hatamoto?sslmode=disable'
export HATAMOTO_TEST_DB_URL="$DATABASE_URL"
cd backend && go run ./cmd/hatamoto migrate && go test -tags=integration ./... && cd ..

# repository guards: admin UI lint, neutrality, migration linter, vendored
# asset hashes, licences, govulncheck, size budgets (needs network for the
# vulnerability database)
make check
```

Run the server locally with `docker compose up` (see the README quick start), or
against the database above with `cd backend && go run ./cmd/hatamoto serve`.
Set `HATAMOTO_ADMIN_DIR=backend/web/admin` to reload admin templates, CSS and JS
from disk while you edit them.

## Admin UI rules

Every admin screen is built from the same partials. The rules (footers, button
order, switches, spacing, colours) are in
[`docs/ADMIN_UI_RULES.md`](docs/ADMIN_UI_RULES.md) and are enforced:

```sh
make design-check                                       # every static design check, seconds, no database
bash scripts/admin-qa/run-local.sh consistency.mjs      # Chrome + Postgres, see scripts/admin-qa/README.md
```

## Pull requests

- One topic per pull request, branched from `main`. Keep it small enough to review.
- `make check` and `go test ./...` must pass; add or update tests for every
  behaviour change (bug fixes start with a failing test).
- Migrations go in `backend/migrations/` with a timestamp version
  (`YYYYMMDDHHMM_name.sql`). Never edit a migration that has shipped; add a new
  one. See [`backend/migrations/README.md`](backend/migrations/README.md).
- A change to a public surface (SDK, manifest, hooks, permissions, themes, REST, MCP, CLI, env vars,
  `.hmp`, registry, update protocol) follows [STABILITY.md](STABILITY.md): a breaking change needs a
  deprecation notice first; every change gets a release note.
- No site-specific code in core: `scripts/check-neutral.sh` rejects it. Site
  features belong in WASM plugins built on `backend/pkg/sdk`
  ([`docs/PLUGIN_API.md`](docs/PLUGIN_API.md)).
- New dependencies need a reason (they must remove more code than they add)
  and an entry in [`docs/DEPENDENCIES.md`](docs/DEPENDENCIES.md); licences
  must be permissive (`go run ./tools/licenses` in `backend/`).
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit
  subjects (`feat:`, `fix:`, `docs:`, `chore:` ...).
- Security problems are never reported in a public issue or pull request:
  see [`SECURITY.md`](SECURITY.md).

## Questions, reviews and response times

Ask questions in [GitHub Discussions](https://github.com/Zen-Labs-Corp/hatamoto/discussions)
(see [`SUPPORT.md`](SUPPORT.md)); issues are for bugs and concrete requests.
These are targets, not promises, and they count business days:

| What | Target |
| --- | --- |
| A new issue or discussion gets a first answer or a label | 5 business days |
| A pull request gets a first review | 5 business days |
| A security report is acknowledged (see [`SECURITY.md`](SECURITY.md)) | 3 business days |

If a pull request has had no answer after 5 business days, a comment on it is welcome.

## Local gates

Until the GitHub checks are switched on for this repository, your machine
enforces the gates through git hooks in `.githooks`. Install them once per clone:

```sh
make hooks        # make hooks-off removes them
```

`make hooks` installs small dispatchers into the git common directory
(`.git/hatamoto-hooks/`), together with a **snapshot** of `.githooks` taken at
that moment, and points `core.hooksPath` there. The setting is shared by every
worktree of the clone, and it does not depend on any checkout: a worktree of an
old branch (one with no `.githooks` of its own), or a main checkout that is on
an old commit, still runs the hooks. **Re-run `make hooks` after pulling
changes to `.githooks`** to refresh the snapshot. If the snapshot is missing, a
hook exits 1 with "hooks missing: run make hooks". `pre-commit` falls back to the
installed copies of `scripts/check-neutral.sh` and `.gitleaks.toml`. `pre-push`
runs the gates of the *pushed* commit in a temporary worktree, and refuses with
"predates the local gates" if that commit lacks a script the gates need; rebase
it on `origin/main`.

| Hook | When | What runs |
| --- | --- | --- |
| `pre-commit` | every commit, seconds | `gofmt -l` on staged Go files, `scripts/check-neutral.sh`, `gitleaks` on the staged diff |
| `commit-msg` | every commit | a Conventional Commit subject (subjects starting `Merge `, `Revert `, `fixup! `, `squash! `, `amend! ` are exempt) and a `Signed-off-by` trailer matching the author (not on merge commits) |
| `pre-push` | every push, minutes | in a temporary worktree of the pushed commit: `gofmt -l .` and `gitleaks` over the push's own commits (rebase, merge, cherry-pick and `--no-verify` commits skip the commit hooks) and `scripts/check-dco.sh`, then, at the same time, the **affected** gates: the check suites whose paths changed (`scripts/check-suites.txt`; `make check` runs them all by hand), `go test`, `go vet -tags=integration` and staticcheck (v0.8.2, blocking, with and without `-tags integration`) over the changed Go packages and the packages that import them, and the integration tests of the changed packages on a throwaway Postgres (`backend`), `go test` of the other modules, wasip1 `go vet` of `examples/plugins/*`. `testdata/` directories are not packages. A **full** run (below) adds `./...` everywhere, every check suite and the SDK module. A gate that already passed on the same tree is reported `cached` and skipped |

Needs: `gitleaks` (`go install github.com/zricethezav/gitleaks/v8@v8.30.1`),
`staticcheck` v0.8.2 (`go install honnef.co/go/tools/cmd/staticcheck@v0.8.2`) and
PostgreSQL 16 or 18 tools on `PATH` or in `PG_BIN` (a throwaway cluster is created and
removed by `scripts/integration-gate.sh`). A missing tool fails the hook; it is
never skipped silently. `HATAMOTO_SKIP_INTEGRATION=1` skips only the integration
tests and says so loudly. Set `REAL_CADDY` to run the Caddy checks against a real
binary. Tests known to be flaky on `main` are listed in `scripts/known-flakes.txt`:
one that fails is re-run alone and only counts as a pass if it passes then and the rest of its package passes too. A package listed as `<package> *` (load-sensitive as a whole) that fails is re-run once in full and must pass then. A package that panics fails whatever is listed.
Passes are cached per tree in `<git common dir>/hatamoto-gate-cache/` (`.githooks/lib/gate-cache.sh`): re-pushing an unchanged tree (an amended message, a rebase that changed nothing) skips the gates it passed; `HATAMOTO_GATE_CACHE=off` re-runs them. DCO and gitleaks are never cached. The pre-push hook sets `HATAMOTO_PERF_STRICT=0` unless it is set: absolute perf goals are noise on a shared machine, the regression bounds still apply.
`HATAMOTO_PREPUSH_RACE=1` adds the race run CI had (`go test -race ./internal/wasmhost ./internal/pluginhost`); it is opt-in because it is slow. Run it before touching those packages.

**Affected or full.** A push runs only what it can affect: `go list -test` gives the
import graph, the changed packages plus their reverse dependencies are tested, and a
non-Go file maps to the package that embeds it or to the package named for it in
`scripts/affected-readers.txt` (tests that read files by path). The hook prints
`affected: ...` or `mode: FULL (<reason>)`, and `check suites: skipped ...` for the suites
no changed path asks for. It never narrows silently: the push runs FULL (everything, as
before) when a module file (`go.mod`, `go.sum`, `go.work`), a `//go:build` line, a
GOOS/GOARCH-suffixed file, a migration, the SDK, an example plugin, the hooks, the Makefile, a
gate script or its data (`scripts/affected-readers.txt`, `scripts/check-suites.*`), `.gitleaks.toml`, a
workflow or `deploy/sql` changes, when a non-Go file under `backend/` maps to nothing, when
`go list` fails, or when the affected packages are more than `HATAMOTO_AFFECTED_MAX_PCT`
(default 60) percent of a module. `HATAMOTO_PREPUSH_FULL=1` forces FULL, and
`scripts/pipeline-gate.sh full` does so. A FULL run in which every gate passed and none was
skipped for the environment (so not with `HATAMOTO_SKIP_INTEGRATION=1`) records `full-suite`
for the tree; an affected push never does.

**The full suite runs once, in CI.** With the GitHub `ci` workflow active, `scripts/ship.sh`
does not wait: it turns on GitHub auto-merge pinned to the checked head, starts a deploy waiter
in the background on the shipping machine, prints `SHIP-H QUEUED ... log=<path>` and exits.
GitHub merges the PR when the ruleset's required checks pass (`backend` is the whole Go suite
with `-tags=integration`, `admin-ui` the UI gate). The waiter (log under
`<git-common-dir>/ship-logs/`) re-checks those two on the merged head and deploys; a failed
check, a push or a closed PR ends it with nothing merged or deployed, and the PR stays open for
a fix. `scripts/ship.sh --wait <PR>` is the blocking ship (wait for CI here, `SHIP_CI_WAIT`,
then merge and deploy). Run `scripts/pipeline-gate.sh full` before the push only if you want
the suite locally first.
With `ci` disabled, `scripts/ship.sh` runs the full suite on the PR head
before it merges (also with `--no-deploy`): it runs the installed pre-push snapshot with
`HATAMOTO_PREPUSH_FULL=1`, reuses a `full-suite` record for the same tree (from
`pipeline-gate.sh full` or an earlier ship; it prints `cached`), and aborts on a red run.
There is no skip knob, with one exception decided from the file list: a PR whose changed files
are all under `.pi/` or Markdown that no test or check suite reads (judged by the snapshot's copies
of `scripts/affected-readers.txt` and `scripts/check-suites.txt`; `docs/site/`, `backend/`, `scripts/`,
the docs the tests name, symlinks, a PR branch that does not contain the current `origin/<base>`, and any doubt keep the suite) still runs the content checks
(the same hook in its normal affected mode: gofmt, gitleaks and DCO on exactly the PR's own
commits (`origin/<base>..<head>`, via `HATAMOTO_PREPUSH_SINCE`, even when the branch is already
fetched), neutrality and the other always-suites; a red run aborts before the merge, nothing is recorded) and prints
`full suite on <sha>: skipped (plans/docs only: N files); content checks: ok`. Re-run `make hooks` after pulling changes to `.githooks`: ship and
`pipeline-gate.sh full` refuse a snapshot that predates this ("run make hooks"). ship
re-executes the copy at `origin/main` only after the merge, so the first ship after a
change to its gates must run from a checkout that has the change.

ship runs the PR's own code: the driver is the trusted snapshot, but the PR's tests,
`scripts/`, `.githooks/`, `Makefile` and `.gitleaks.toml` execute on the shipping machine
(with credentials scrubbed from the environment). Review them before shipping. A PR from a
fork is refused: review it, push it to a branch of this repository
(`git fetch origin pull/N/head && git push --no-verify origin FETCH_HEAD:refs/heads/review/pr-N`), and
(only after reading the diff: a plain push would run its tests here through pre-push) open and ship the PR from that branch.

**One heavy phase at a time per slot.** A push with uncached heavy work takes one of
`HATAMOTO_FARM_SLOTS` (default 2) host-wide slots under `HATAMOTO_FARM_DIR` (default
`${XDG_CACHE_HOME:-$HOME/.cache}/hatamoto-farm`) and prints `waiting behind <branch>` while
they are busy; quick gates and fully cached pushes never wait. When one gate fails the
hook stops the others (TERM, then KILL after 5 s), prints them as `cancelled` and refuses
the push. Each gate prints `skipped: N (log <path>)`: the count of skipped tests and the
copy of its log under `<git common dir>/hatamoto-gate-logs/` (the newest 20 are kept).

`git commit --no-verify` and `git push --no-verify` exist, but reviewers reject
pull requests that were pushed with them. `scripts/test-hooks.sh` (part of
`make check`) tests the hooks themselves.

## Developer Certificate of Origin (DCO)

Hatamoto uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/)
instead of a CLA. By signing off a commit you certify that you wrote it, or
otherwise have the right to submit it under the project's licence
(Apache-2.0). Every commit in a pull request needs a `Signed-off-by` line with
your real name and an email you can be reached at, matching the commit author:

```
Signed-off-by: Jane Doe <jane@example.com>
```

`git commit -s` adds it. To sign off commits you already made:
`git rebase --signoff main`, then force-push your branch. The `dco` check on
every pull request fails when a commit is not signed off.

## AI-assisted contributions

You may use AI tools (code assistants, chat models, generators) to prepare a
contribution. The rules are the same as for any other contribution:

- **You are the author and you are responsible.** You have read every line,
  you understand it, you ran the tests, and you can explain and defend it in
  review. "The tool wrote it" is not an answer to a review comment.
- **The DCO applies in full.** By signing off you certify that you have the
  right to submit the work under Apache-2.0. Do not submit output you cannot
  vouch for, for example code a tool reproduced from a project with an
  incompatible licence. The sign-off is always a person's: a tool does not
  sign off for you.
- **Say so when it matters.** If a tool wrote a substantial part of the
  change, mention it in the pull request description. A co-author trailer for
  the tool is welcome but not required.
- **No unreviewed bulk pull requests.** Do not open many pull requests, or one
  very large one, that you have not reviewed yourself: mass-generated
  refactors, style churn, automated "fixes" to docs or tests, or reports of
  security problems that were never reproduced. We close these without a
  detailed review. Small, tested, focused changes are welcome whatever tool
  helped you write them.
- **Security reports** written with a tool must still show a working
  reproduction against a real build.

## Licence

Contributions are licensed under the [Apache License 2.0](LICENSE), the
project's licence. The root `LICENSE` covers every file except third-party
files that keep their own licence, listed in [`NOTICE`](NOTICE). Files carry no
per-file licence or copyright headers (SPDX or otherwise), and you do not need
to add one. "Hatamoto" is a trademark;
see [`TRADEMARKS.md`](TRADEMARKS.md).
