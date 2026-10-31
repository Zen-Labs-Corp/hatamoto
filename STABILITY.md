# Stability and compatibility

Hatamoto is pre-1.0. This document says which parts of Hatamoto you can build on, how long old
plugins keep working, and how a breaking change is announced. It is written for plugin and theme
authors and for people who run their own server. Where a promise is backed by a test or a script,
the name is given. Where it is project policy only, the text says so. Paths in code spans, such as
`docs/THEMES.md`, are files in the Hatamoto core repository.

## Versions at a glance

| Thing | Scheme | Today |
|---|---|---|
| Product (the `hatamoto` binary) | `vX.Y.Z`, git tag `vX.Y.Z` | `v0.31.0` |
| Go module for the product source | `hatamoto.dev/core`, git tag `backend/vX.Y.Z`, created by `scripts/tag-release.sh` | follows the product |
| Plugin SDK (`hatamoto.dev/sdk`) | semantic, git tag `vA.B.C` in the public `Zen-Labs-Corp/hatamoto-sdk` repository, exported from this repository by `scripts/publish-sdk.sh` | `0.14.0` (`0.12.0` shipped with product `v0.31.0`) |
| Plugin manifest | the `sdk` range in `plugin.json` | follows the SDK |
| Theme contract | `data-hm-contract` on `<html>` | `1` |
| HTTP API | `/api/v1` path prefix | `v1` |

## What is covered

The table lists every public surface, how stable it is, which rule governs a change, and the document
that defines it. The rules are in the sections after the table.

Levels:

- **stable (pre-1.0 rules)**: you can build on it. A change follows the rule named in the third column,
  and the 0.x rules in "What 0.x means" apply until 1.0.
- **experimental**: may change without notice. The one planned experimental surface is the content delivery API,
  which is not yet shipped. Anything marked experimental in its own documentation gets this level.
- **not covered**: no promise.

| Surface | Level | Change rule | Authoritative doc |
|---|---|---|---|
| Go SDK `hatamoto.dev/sdk` and `hatamoto.dev/sdk/guest` | stable (pre-1.0 rules) | SDK rule | the SDK `README.md`, `docs/PLUGIN_API.md` |
| `plugin.json` manifest | stable (pre-1.0 rules) | SDK rule | `docs/PLUGIN_API.md`, [`docs/site/plugins/manifest.md`](https://hatamoto.dev/docs/plugins/manifest) |
| Host functions, the `hm_call` envelope, error codes and payload schemas | stable (pre-1.0 rules) | SDK rule | `docs/PLUGIN_API.md` (Host functions, Payload schemas) |
| Hook names (actions, filters, export naming) | stable (pre-1.0 rules) | SDK rule | `docs/PLUGIN_API.md` (Hooks and exports) |
| Permissions | stable (pre-1.0 rules) | permission rule | `docs/PLUGIN_API.md` (Permissions), `docs/SECURITY_MODEL.md` |
| Theme contract: required layouts and partials (`base.html`, `page.html`, `404.html`, `error.html`, and the partials `head`, `header`, `nav`, `footer`, `section`, `scripts`; see `docs/THEMES.md`), optional layouts, `data-hm-contract`, `theme.json`, template functions, `PageView` fields, section schemas | stable (pre-1.0 rules) | product rule | `docs/THEMES.md` |
| Widget packs | stable (pre-1.0 rules) | product rule | `docs/WIDGET_PACKS.md` |
| Builder data sources | stable (pre-1.0 rules) | SDK rule for the plugin side, product rule for the widget side | `docs/WIDGET_PACKS.md` (Data sources), `docs/BUILDER.md` |
| REST `/api/v1` (the route table is pinned by `routes.golden`) | stable (pre-1.0 rules) | product rule; `/api/v1` is kept at least 12 months after `/api/v2` ships | `docs/PLUGIN_API.md` for plugin routes, route table in `backend/internal/server/routes_table.go` |
| MCP tools and scopes | stable (pre-1.0 rules) | product rule | `docs/MCP.md` |
| Content `locale` field (REST `/api/v1`, MCP, webhooks, delivery JSON once it ships; SDK payloads) | stable (pre-1.0 rules) | product rule for REST, MCP, webhooks and delivery JSON (once it ships); SDK rule for SDK payloads | `docs/URL_PATTERNS.md` (Locale), `docs/MCP.md`, `docs/WEBHOOKS.md` |
| CLI commands and flags | stable (pre-1.0 rules) | product rule | `docs/CLI.md`, [`docs/site/operate/cli-reference.md`](https://hatamoto.dev/docs/operate/cli-reference) |
| Environment variables and configuration | stable (pre-1.0 rules) | product rule | [`docs/site/operate/configuration-reference.md`](https://hatamoto.dev/docs/operate/configuration-reference) |
| `.hmp` package format | stable (pre-1.0 rules) | product rule | `docs/PACKAGES.md` |
| Registry index format (signed envelope, `version: 1`) | stable (pre-1.0 rules) | product rule | `docs/PACKAGES.md` (Registry), `registry/README.md` |
| Release and update protocol: channels, signed manifests, ramp, yanks, rollback floor | stable (pre-1.0 rules) | schema rules and product rule | `docs/UPGRADES.md`, [`docs/site/updates/upgrade-rules-and-support.md`](https://hatamoto.dev/docs/updates/upgrade-rules-and-support), [`docs/site/updates/trust-and-signing.md`](https://hatamoto.dev/docs/updates/trust-and-signing) |
| Content delivery API (read-only content JSON for headless sites; not yet shipped) | experimental | none: no stability promise before its v1 | none yet; it is announced before it ships |
| The admin URL (per install, printed by `hatamoto admin-url`; use the delivery API and REST keys instead) | not covered | none | "What is not covered" below, `docs/UPGRADES.md` |
| Admin UI HTML, internal Go packages (`backend/internal/**`), log lines and error text, plugin runtime internals (wazero) | not covered | none | "What is not covered" below |

One language per install; multilingual UI not yet. Every content entity carries a `locale` (BCP 47),
slugs are unique per schema and locale, and public pages serve only the site language.

The SDK is bumped to 0.13.0 for `locale` and to 0.14.0 for the form attribution although no rule here requires it for an additive field
(plugins ignore unknown JSON fields, and no permission is involved), because a published SDK tree cannot
change under the same version (`scripts/publish-sdk.sh` refuses to retag a different tree) and 0.10 and
0.11 set the precedent of shipping additive payload changes as a minor.

## What 0.x means

- A patch release (`0.N.x`) never breaks a covered surface, except a security exception (see "Security
  exception"), which says so in its release note and advisory.
- A minor release (`0.N.0`) may change the behaviour of a covered surface, but only through the
  deprecation process below. Until 1.0, "minor" is where breaking changes live, so read the release
  notes of every minor.
- For the updater a `0.N.0` release is a minor: it installs automatically under the owner's update
  policy, and its migrations may only expand the schema. A change that contracts the schema (drop,
  rename, retype) needs a major release. See "Release and update protocol".
- From 1.0 the usual semantic versioning applies: no breaking change inside a major version.

The change rules in the table are:

- **SDK rule**: the deprecation notice for an SDK change appears in one SDK minor, and the change
  itself lands in a later SDK minor, with a release note.
- **Permission rule**: see "Permissions".
- **Product rule**: at least two product minors and at least 90 days between the notice and the
  removal, whichever is later.

## SDK versions and `CompatibleWith`

The `sdk` range in `plugin.json` is the handshake between a plugin and a host. A host serves its own
SDK version (`sdk.Version`) and every version in `sdk.CompatibleWith`, and refuses a plugin whose range
matches none of them.

- Every 0.x SDK minor from 0.5 stays served until 1.0. Today `Version` is `0.14.0` and
  `CompatibleWith` is `0.5.0` to `0.13.0`. Dropping an entry is a breaking change and is announced like
  any other.
- A newer host serves older ranges. An older host refuses a newer range: a host that serves 0.13 refuses
  a plugin that declares `^0.14`. Declare the oldest range your plugin needs.
- Three tests pin this in `backend/pkg/sdk`: `TestVersionPinned` (the version constant),
  `TestCompatibleWithPinned` (the exact list, sorted, without the current version) and
  `TestCompatibleWithOldHost` (a host that reports 0.12.0 refuses `^0.13` and accepts `^0.12`,
  `^0.11`, `^0.10` and `^0.5`). Changing the version or the list means changing those tests in the same commit.
- Load and install both report a refusal through `pluginhost.SDKRefusal` in
  `backend/internal/pluginhost/compat.go`, a core helper that is not part of the SDK. The message
  names the plugin, the range it asked for, the host's SDK version and the minors the host serves.
  `TestSDKRefusalOldHost` checks its wording, but the wording is not part of the contract.
- Update preflight compares a plugin with the SDK of the release you are updating to, and blocks the
  update when the plugin would be refused there.

## Permissions

- A new permission for a new capability is additive and ships in an SDK minor.
- Gating an existing capability behind a permission is a behaviour change. It needs an SDK minor bump,
  a release note and, unless it is a security exception, a deprecation notice first.
- A permission is never granted automatically: the owner grants it under Plugins > Permissions. An
  update that widens reach (new allowed hosts, `http.fetch.any`, `builder.island`, `site.scripts`) also
  needs the owner's confirmation before it installs (`ConfirmationRequired` in
  `backend/internal/packages`, for example `NewScripts`). An update that asks for another permission,
  such as `kv`, installs without a prompt and the permission stays ungranted until the owner grants it.
- `Manifest.Validate` accepts any known permission under any `sdk` range; the range does not gate a
  permission. A plugin that declares `site.scripts` under `^0.11` is valid, and a host that predates the
  permission rejects it as unknown. `^0.12` only says "built for SDK 0.12".

## The `site.scripts` drift

Enforcement of `site.scripts` shipped in v0.30.0, whose SDK version is 0.11.0. The SDK version moved to
0.12.0 afterwards. The permission gates something plugins could already do (browser JavaScript, script
tags added through `render.head`, scripted sections and partials), so under the permission rule above it
needed an SDK minor bump, a release note and a deprecation notice first. It had none of them when it
shipped. We count that as a mistake we are correcting, not as precedent.

When it shipped, plugin JavaScript ran with the privileges of whoever loaded the page, including
signed-in admins. Today the admin is sealed against page scripts (`docs/SECURITY_MODEL.md`, Admin
isolation). Gating it kept the permission boundary true, so the change would have fit the security
exception below. It was not labelled as one when it shipped, and we do not claim the exception after the fact. From now on a change
that relies on the exception says so in its release note and advisory, or it follows the normal window.
The release notes for v0.31.0 describe the change and the version it belongs to.

## Deprecation

- **SDK surfaces** (Go SDK, manifest, host functions, hooks, permissions): the deprecation is announced
  in one SDK minor, in the SDK README and in the release notes. The change lands in any later SDK
  minor, and the release notes say that it happened.
- **Product surfaces** (REST `/api/v1`, MCP tools and scopes, CLI, environment variables and
  configuration, the theme contract, widget packs, the `.hmp` and registry formats, the release and
  update protocol): at least two product minors and at least 90 days pass between the notice and the
  removal, whichever is later.
- **`/api/v1`** keeps working for at least 12 months after `/api/v2` ships.
- A notice is a release note headed "Deprecated" plus a note in the documentation of the surface.
- After 1.0 nothing is removed inside a major version.

## Security exception

A change may skip the windows above, and may land in a patch release, only when it is needed to keep
a guarantee in `docs/SECURITY_MODEL.md` true (the plugin sandbox and permission boundary, signature and trust checks,
SSRF and SQL guards, authentication, output escaping), or when it fixes a vulnerability reported under
`SECURITY.md`. Convenience, performance and cleanup are never a security exception.

An exception has to say so:

- the release record carries the security flag (`Record.Security` in `backend/internal/release`);
- a GitHub security advisory names the affected and the fixed versions (`SECURITY.md`);
- the release note says what broke and why.

A change without those three is not a security exception, whatever its reason.

## Release and update protocol

- **Channels.** `stable`, `edge` and `canary` (`release.Channels`). The default is `stable`, and
  `HATAMOTO_UPDATE_CHANNEL` selects another. Each channel has its own signed manifest, and moving
  between channels needs no migration.
- **What a server is offered.** `Decide` in `backend/internal/updater/decide.go` picks the newest
  release of the same major version that is not yanked, not halted, not on the box's bad list, and
  whose ramp already includes this install. Which day a release reaches which percentage is release
  tooling convention, not a promise.
- **Yanks.** A yanked release is never offered again. A server running one moves to the replacement
  when there is one, else to a newer release if one is offered, else it rolls back to its previous
  release. The rollback (`yankBack` in the
  same file) is refused when there is no previous release, when it is older than the `rollback_floor`,
  when it is yanked too, or when it is on the bad list.
- **Schema.** Migrations are forward-only. Release N's schema keeps release N-1's binary working, which
  `scripts/n-1.sh` checks, and `scripts/check-migrations.sh` lints the migrations. A rename or drop
  takes two releases and a contract marker, and contraction is reserved for major releases. Each record
  names a `rollback_floor`. The details are in `docs/site/updates/upgrade-rules-and-support.md`. The
  release tooling runs these checks locally and in CI where CI is enabled.
- **Trust.** Release records and channel manifests are signed, and the client refuses an unsigned
  record or an expired manifest (`docs/site/updates/trust-and-signing.md`).
- **Support.** The latest minor of the current major gets every fix. After 1.0 the previous major gets
  security fixes for six months after the next major ships (`SECURITY.md`).

## Where breaking changes are announced

- `docs/RELEASE_NOTES.md`, which ships in each release's source.
- The GitHub release notes of each tag. The signed release record's `notes_url` points there.
- The Versioning section of the SDK `README.md`, for SDK changes.
- GitHub security advisories, for security exceptions.

The `/changelog` page lists merged changes. It is not where deprecation notices go.

## What is not covered

- The admin URL. It is per install: a fresh install gets a private random address, an upgraded one keeps
  `/admin` until its owner changes it, and `hatamoto admin-url` prints it. Do not build on it. The
  delivery API and REST keys are the contract.
- The body of `/health` and `/ready` (the plain `ok` answer to anyone, and the JSON detail that needs a
  session or key), and the name of the admin session cookie.
- Admin UI markup, CSS class names and JavaScript.
- Anything under `backend/internal/`. The server is not a library: do not import its packages.
- Identifiers that are not exported, and the database schema (table and column names, indexes).
  Use the API, the CLI, the MCP tools or the export format instead.
- Log lines, metric names and error text, including the compatibility refusal message and the updater
  messages.
- The plugin runtime internals (wazero, Extism) and performance characteristics. The behaviour a plugin
  sees is covered, the runtime that produces it is not.
- Third-party plugins and themes, and the tooling of the hatamoto-plugins repository.
- Anything documented as experimental.

## Reporting a break

A change to a covered surface that was not announced as described here is a bug. For a break in the
SDK, the manifest, a hook or a permission, open an issue on the public SDK repository,
https://github.com/Zen-Labs-Corp/hatamoto-sdk/issues, with the versions before and after. Once the
Hatamoto core repository is public, you can open it there instead. Report a security problem to
security@hatamoto.dev, as `SECURITY.md` describes.
