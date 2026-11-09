# Scripts

Shell scripts for the local gates, CI, releases and a few developer tools. Every script starts with a
comment that says what it does and how to call it; this page groups them. Run them from the repository
root.

## Gates (`make check` and the git hooks)

| Script | What it does |
| --- | --- |
| [check-suites.sh](check-suites.sh) | runs the `make check` suites listed in [check-suites.txt](check-suites.txt), or only those a change touches (`--changed <range>`) |
| [check-changed.sh](check-changed.sh) | runs the guards that apply to a set of changed files, after each edit |
| [check-neutral.sh](check-neutral.sh) | core carries no site-specific, fork-specific or private names |
| [check-migrations.sh](check-migrations.sh) | the rollback-safe schema linter (see `docs/UPGRADES.md`) |
| [check-dco.sh](check-dco.sh) | every commit has a `Signed-off-by` line |
| [check-jsonv1.sh](check-jsonv1.sh) | no `encoding/json` v1 imports under `backend/` |
| [check-channel-freshness.sh](check-channel-freshness.sh) | a signed channel manifest must not be close to expiry |
| [integration-gate.sh](integration-gate.sh), [go-test-flaky.sh](go-test-flaky.sh), [known-flakes.txt](known-flakes.txt) | integration tests at push, and the retry list for known flaky tests |
| [affected-readers.txt](affected-readers.txt) | non-Go files whose change must run given tests at push |
| [n-1.sh](n-1.sh) | the previous release's binary still runs on this tree's schema |
| [baseline-ref.sh](baseline-ref.sh), [version.sh](version.sh) | the ref a change is judged from, and the version stamped into builds |

## CI

[ci-relevance.sh](ci-relevance.sh), [ci-full-plan.sh](ci-full-plan.sh), [ci-full-report.sh](ci-full-report.sh),
[ci-server-shard.sh](ci-server-shard.sh), [ci-smoke-pkgs.sh](ci-smoke-pkgs.sh) with
[ci-smoke-exclude.txt](ci-smoke-exclude.txt), and [ci-integration-pkgdb.sh](ci-integration-pkgdb.sh) decide
what a CI job runs and split the integration tests across jobs. The workflows are in `.github/workflows/`.

## Releases and publishing

| Script | What it does |
| --- | --- |
| [tag-release.sh](tag-release.sh), [verify-tag-signature.sh](verify-tag-signature.sh) | create and check the signed release tags |
| [publish-release.sh](publish-release.sh) | upload one signed release to the update server |
| [publish-install.sh](publish-install.sh) | publish the attested `install.sh` of a release |
| [publish-registry.sh](publish-registry.sh) | upload the built plugin registry |
| [publish-sdk.sh](publish-sdk.sh) | export `backend/pkg/sdk` to the public SDK repository |
| [public-snapshot.sh](public-snapshot.sh) | build the public tree from one ref |
| [third-party-licenses.sh](third-party-licenses.sh) | write the third-party licence files that ship with a release |
| [changelog-bootstrap.sh](changelog-bootstrap.sh) | create the schema behind the `/changelog` page |
| [staging-update-check.sh](staging-update-check.sh), [e2e-update-agent.sh](e2e-update-agent.sh), [e2e-supervisor.sh](e2e-supervisor.sh) | end-to-end checks that a real box updates itself and rolls back |

## Deploying hatamoto.dev

[ship.sh](ship.sh) merges a pull request and deploys it, with a database backup, health checks and
automatic rollback; [ship-db-restore.sh](ship-db-restore.sh) restores that backup and
[deploy-host.sh](deploy-host.sh) resolves the deploy host.

## Developer tools

| Script | What it does |
| --- | --- |
| [new-component.sh](new-component.sh) | scaffold an admin component with all its artefacts |
| [demo-seed.sh](demo-seed.sh) | seed the content of the public demo (`HATAMOTO_DEMO=1`) |
| [admin-qa/](admin-qa/README.md) | browser checks of the admin: consistency, accessibility, screenshots |
| [homepage-shots/](homepage-shots/README.md) | regenerate the homepage and README screenshots from a seeded demo site |

## Tests of the scripts

Each `test-<name>.sh` is an offline test of the script of the same name (for example
[test-check-neutral.sh](test-check-neutral.sh) tests `check-neutral.sh`). Most are registered as suites in
[check-suites.txt](check-suites.txt), so `make check` runs the ones a change touches. `test-sdk-external.sh`
runs in CI and in the full pre-push gate. `test-demo-seed.sh`, `test-new-component.sh`, `test-ship-dryrun.sh` and
`test-ship-restore.sh` are run by hand when their script changes.
