# Kits

The `kit.json` format (version 1) is fixed here. The kit commands, the apply
code and the registry listing arrive in a later release; until then core does
not read kit packages.

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
  "requires": ["comment.policy", "comment.rank", "plugin.entity-write", "comment.signin"],
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
  `site.scripts` and `ui.allow:*`. Those two are separate ticks on the review
  screen.
- `switches` are requests, applied only with the owner's ticks:
  `comments: true` needs the two comment warnings; `homepage` and a theme's
  `activate` are separate ticks and default to off. On an existing site a kit
  only adds; it never changes what is already there.
- `requires` lists core feature names. Core publishes the list of features it
  has; the names are `comment.policy`, `comment.rank`, `plugin.entity-write`
  and `comment.signin`. If a required feature is missing, the apply is refused
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

## Command line

These command names are reserved; they are not available yet.

```
hatamoto kit apply <name|file|url> [--accept-comment-warnings] [--grant P,P] [--dry-run]
hatamoto kit check <dir|file>
hatamoto kit pack <dir> --key F
hatamoto kit list
```
