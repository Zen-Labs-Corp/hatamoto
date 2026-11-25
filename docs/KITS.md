# Kits

The `kit.json` format (version 1) is fixed here, together with how a kit is
applied: from the command line (`hatamoto kit apply`) or from Settings > Kits,
and how a registry lists kits.

## What a kit is

A kit is a signed package that sets up a site in one step: plugins, a theme,
content types, pages, menus and a few switches. It holds no code, no SQL and
asks for no permissions of its own.

A kit is **applied and recorded**, not stored as a package row. Core keeps one
record per applied kit in the table `hatamoto_applied_kits`. There is no
uninstall in version 1: what a kit created is ordinary site content that the
owner edits or deletes like any other.

## Package

- Kind `kit`, manifest file `kit.json`, packed as a zip and signed like any
  other package (see [PACKAGES.md](PACKAGES.md)).
- A package holds exactly one of `plugin.json`, `theme.json` or `kit.json`.
- Registry index entries may have kind `kit`. Older clients ignore a kind they
  do not know.

## Example

```json
{
  "name": "community",
  "version": "1.0.0",
  "kit": 1,
  "sdk": "^0.27",
  "title": "Community",
  "description": "A community site with ranked stories and threaded comments.",
  "requires": ["comment.identity", "comment.policy", "comment.rank", "plugin.entity-write", "comment.signin"],
  "plugins": [{"name": "example-board", "range": "^1", "grant": "all"}],
  "themes": [{"name": "example-theme", "range": "^1", "activate": false}],
  "schemas": [
    {
      "slug": "story",
      "name": "Stories",
      "schema": {"type": "object"},
      "settings": {
        "url_pattern": "/item/{slug}",
        "comments": {
          "mode": "open",
          "order": "rank",
          "policy": {"enabled": true, "max_depth": 32, "edit_window_minutes": 120, "slow_mode_seconds": 30}
        }
      }
    }
  ],
  "pages": [
    {
      "slug": "latest",
      "name": "Latest",
      "schema": "page",
      "blocks": [{"type": "rich-text", "props": {}}],
      "homepage": false
    }
  ],
  "menus": [
    {"location": "header", "name": "Header", "items": [{"label": "Latest", "url": "/latest"}]}
  ],
  "switches": {"comments": true, "comment_policy": ["story"], "homepage": "latest"}
}
```

## Fields

| Key | Type | Limit | Default |
| --- | --- | --- | --- |
| `name` | string | package name rule `^[a-z][a-z0-9-]{1,39}$` | required |
| `version` | string | semver | required |
| `kit` | integer | the format version, `1` | required |
| `sdk` | string | a semver range, as in `plugin.json` | required |
| `title` | string | short text | the `name` |
| `description` | string | short text | empty |
| `requires` | array of string | feature names (see Rules) | none |
| `plugins` | array of `{name, range, grant}` | at most 8 (plugins and themes each) | none |
| `plugins[].name` | string | a registry plugin name | required |
| `plugins[].range` | string | a semver range | required |
| `plugins[].grant` | string | `"all"`, or a list of permission names | none (the owner ticks each permission) |
| `themes` | array of `{name, range, activate}` | at most 8 | none |
| `themes[].name` | string | a registry theme name | required |
| `themes[].range` | string | a semver range | required |
| `themes[].activate` | boolean | needs the owner's tick | `false` |
| `schemas` | array of `{slug, name, schema, settings}` | at most 16 | none |
| `schemas[].slug` | string | the package schema slug rule `^[a-z]([a-z0-9]\|-[a-z0-9]){0,39}$` | required |
| `schemas[].name` | string | display name | required |
| `schemas[].schema` | object | a JSON Schema object | required |
| `schemas[].settings` | object | `url_pattern` and `comments` (see Rules) | none |
| `pages` | array of `{slug, name, schema, blocks, homepage}` | at most 32; each page document at most 64 KiB | none |
| `pages[].slug` | string | the page slug rule | required |
| `pages[].name` | string | display name | required |
| `pages[].schema` | string | the content type slug, normally `page` | `page` |
| `pages[].blocks` | array of `{type, props}` | within the 64 KiB page document | none |
| `pages[].homepage` | boolean | a request, needs the owner's tick | `false` |
| `menus` | array of `{location, name, items}` | at most 24 items each | none |
| `menus[].location` | string | a menu location of the active theme | required |
| `menus[].items` | array of `{label, url}` | at most 24 | none |
| `switches` | object | `comments`, `comment_policy`, `homepage` | all off |
| `switches.comments` | boolean | needs the two comment warnings | `false` |
| `switches.comment_policy` | array of string | schema slugs from `schemas` | none |
| `switches.homepage` | string | a page slug from `pages` | none |

## Rules

- A schema `url_pattern` must end in `{slug}` or `{path}` (see
  [URL_PATTERNS.md](URL_PATTERNS.md)). A pattern ending in `{id}` is refused.
- Unknown top-level keys refuse the kit.
- Limits: at most 16 schemas, 32 pages (each page document at most 64 KiB),
  24 items per menu, and 8 plugins and 8 themes.
- A `range` is checked against the single version the registry lists for that
  name. A mismatch refuses the kit.
- `grant: "all"` grants every permission in the plugin's manifest **except**
  every permission the install prompt marks "Handle with care" (for example
  `site.scripts`, `builder.island`, `http.fetch.any`, `entities.filter`,
  `client.key`) and `ui.allow:*`. Those are separate ticks on the review
  screen, and a `grant` list cannot grant them either.
- `switches` are requests, applied only with the owner's ticks:
  `comments: true` needs the two comment warnings; `homepage` and a theme's
  `activate` are separate ticks and default to off. On an existing site a kit
  only adds; it never changes what is already there.
- `requires` lists core feature names. Core publishes the list of features it
  has; the names are `comment.identity`, `comment.policy`, `comment.rank`,
  `comment.signin` and `plugin.entity-write`. If a required feature is missing, the apply is refused
  and the message names the missing features. A `switches.comment_policy` entry
  for a feature not listed in `requires` is skipped, with a message.

## Ownership of a content type

When a kit plugin declares `entity_schema` with the slug of a kit `schemas`
entry, the plugin's package creates the content type and the kit applies only
that entry's `settings`.

## Apply

Apply is ensure-by-key: a schema by its slug, a page by its slug, a menu by its
location. A kit never overwrites something the owner edited. The order is fixed:

1. Resolve and inspect all packages first.
2. Plugins.
3. Schemas.
4. Theme.
5. Pages and menus.
6. Switches.
7. Record the kit in `hatamoto_applied_kits`.
8. Write the audit entry `kit.apply` (an owner-only action).
9. Invalidate the cache once.

### Apply in detail

- **Ensure by key.** A content type is found by its slug, a page by its slug
  (inside its content type), a menu by its location. Missing: created. Present:
  **kept**, which means left exactly as the owner has it; nothing is
  overwritten, reordered or republished. Content types that exist but lack some
  of the kit's `settings` are kept as they are; the exception is a content type
  created by a kit plugin (`entity_schema`), see below.
- **Plugins and themes** that are already installed are kept, at the installed
  version. Only a name the registry lists, at a version that satisfies `range`,
  is installed. A plugin a kit asks for is installed with the permissions the
  kit's `grant` names (see Rules) plus the ones the owner ticks.
- **Fresh site.** A site is fresh when it runs the default theme and still has
  the home page created at first start. On a fresh site the theme's `activate`
  and the `homepage` switch are applied without an extra tick; on any other
  site they are applied only when the owner ticks them (`--activate-theme` and
  `--homepage` on the command line).
- **Menu locations.** A menu is created only at a location the active theme
  declares. When the location is already held by a menu, or the theme does not
  declare it, the menu is skipped and listed with a message.
- **Comments.** `switches.comments` turns comments on only with the owner's
  accept of both warnings and only when the site has an address (Settings >
  General, or `PUBLIC_BASE_URL`). Without an address the apply is refused
  before anything is changed. The owner can leave the switch off and turn
  comments on later in Settings > Comments.
- **Comment order and community policy.** `settings.comments.order` and
  `settings.comments.policy` are written through the same code as Settings >
  Comments > Content types, with its limits: reply depth 1 to 32, edit window
  1 to 1440 minutes, slow mode 0 to 86400 seconds, and a known order (newest,
  oldest or rank). A bad value refuses the kit before anything changes.
  Order `rank` sorts every sibling level by the rank a plugin writes, then
  newest first; until a plugin supplies ranks all comments tie and threads
  read newest first.
  `policy.enabled` in `kit.json` is ignored: the policy goes on only for the
  types in `switches.comment_policy`, with the owner's accept of its warning
  ("Are you sure? With the community policy on, comments with links and first
  comments on this content type are posted at once, without review. Spam
  checks, blocked commenters, duplicates, captcha and surge mode still hold
  comments."). A type whose policy is already on is kept. Each type a kit turns
  on is recorded as `comments.policy`.
- **Content type owned by a plugin.** When the plugin that creates a content
  type (`entity_schema`) is part of the kit, the content type gets only the
  settings it lacks (including the comment order and policy numbers when it
  has none stored); a type the owner or an earlier apply created is kept as is. When the slug belongs to another
  plugin, the kit is refused.
- **Interrupted runs.** An apply is not one transaction. Every step can be run
  again, and the record is written last, so running the same command again
  finishes what a failed run left. A run that changes nothing reports
  `already applied: nothing to change`.
- **No uninstall.** Nothing a kit created is tracked for removal; delete it in
  the admin like any other content.
- **Visibility.** The command line cannot load a plugin into a running server
  or clear its caches: the server loads a new plugin within a few seconds, and
  a `url_pattern` is visible once the server has refreshed its URL registry.

## The applied record

Table `hatamoto_applied_kits`, one row per kit name:

| Column | Meaning |
|---|---|
| `name` | the kit name (primary key) |
| `version` | the version last applied |
| `applied_by` | who applied it: an owner's email, or `cli` |
| `applied_at` | when (UTC) |
| `summary` | JSON: counts of what was created, filled, kept and skipped |

Applying again updates the row only when something changed or the version is
new. The audit log gets one `kit.apply` entry per apply that did work (an
owner-only action; the target is `kit:<name>`). Turning comments on from a kit
also writes `comments.enable`; each content type whose community policy a kit
turns on writes `comments.policy`.

## Features

Core knows a fixed list of feature names a kit may put in `requires`:
`comment.identity`, `comment.policy`, `comment.rank`, `comment.signin` and
`plugin.entity-write`. `kit.Features()` (package `internal/kit`) returns the ones
this build has. This build has all five. `comment.identity` means a plugin with
`comments.identity` can sign visitors in as email-less commenters (SDK 0.29). A name is added
only by the change that makes the feature work. `hatamoto kit check` prints a note for each
required name this build does not have yet ("needs a newer Hatamoto") and for
each name it does not know at all.

## Command line

```
hatamoto kit check <dir|file> [--package F] [--expect-name N]
hatamoto kit pack <dir> --key F [--out F] [--expect-name N]
hatamoto kit apply <name|file|url> [--accept-comment-warnings] [--grant P,P] [--activate-theme] [--homepage] [--dry-run] [--dev]
hatamoto kit list
```

- `check` validates a kit directory or a packed file without a key, a database
  or the network, and prints `kit <name> <version>: ok`. `--package F` names a
  packed `.hmp`; `--expect-name N` refuses a kit with another name (registry
  builders use both).
- `pack` builds a signed `.hmp` from a directory. `--out` defaults to
  `<name>-<version>.hmp`.
- `apply` takes a registry name, an `https` URL or a file. It prints the
  signer, what it resolved, both comment warnings (when the kit asks for
  comments), then each step, and records the kit. `--accept-comment-warnings`
  accepts the warnings and turns comments and the community policy on (the
  policy warning is printed too); without it the command says `comments left off`. `--grant P,P`
  names permissions beyond what the kit's `grant` gives, as `<plugin>:<permission>`
  (a bare name works only when one plugin of the kit asks for it); those
  "Handle with care" permissions and `ui.allow:*` are granted only when named here. `--dry-run` prints the steps
  and changes nothing. `--dev` allows an unsigned dev kit (never in
  production). A refused kit prints each reason, changes nothing and exits 1.
  Running the command again prints `kit <name> <version> is already applied:
  nothing to change`.
- `list` shows the applied kits, then the kits the registry offers (marking the
  applied ones). When the registry cannot be read it prints
  `registry: unavailable`.

`package install` refuses a kit file and points to `kit apply`.

## In the admin

Settings > Kits lists the applied kits and the kits the registry offers, each
with a review screen that shows the same steps and the same ticks (comment
warnings, homepage, theme, extra permissions). The setup wizard offers the same
choice once on a fresh site. Applying is an owner action, `kit.apply`.

## In the registry

A registry lists a kit as an entry of kind `kit`; older clients ignore it.
The registry builder reads one manifest per kit, `manifests/kits/<name>.json`
in the plugins repository:

| Field | Meaning |
|---|---|
| `name` | the kit name (also the file name) |
| `title`, `about` | display name and one line |
| `features` | the names the kit lists in `requires`, for the listing |
| `repo`, `commit`, `subdir` | where the kit directory lives, pinned to a commit |
| `author`, `license` | shown in the listing |
| `archive_grant` | permission to keep a copy of the archive |
| `homepage` | optional link |

The built file is `<name>.kit.hmp`, so a kit and a plugin may share a name. A
release is tagged `kit-<name>/v<version>`. Kits are never mirrored: the
registry signs the pinned build and nothing else.
