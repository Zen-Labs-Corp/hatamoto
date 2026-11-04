---
title: Tag packs
nav_title: Tag packs
description: Add a tracking tag to Settings > Tracking with a package that holds only a small JSON file and runs no code.
---

A **tag pack** is a plugin package that adds one to four tracking tags to [Settings > Tracking](../admin/settings-users-email.md). It holds no code: each tag is a JSON file, and the server loads the tag's script itself, after the visitor's cookie consent, the same way it loads the built-in tags. Use it for a vendor the built-in list does not cover. To pack and sign a package, see [Package and install a plugin](package-and-install.md).

## The package layout

```text
plugin.json
tags/hotjar.json
```

`plugin.json` may hold only `name`, `version`, `sdk` and `description`. A tag pack cannot also hold `plugin.wasm`, `widgets/` or `ui/`, and a theme cannot hold `tags/`. Only `tags/<name>.json` files are allowed under `tags/`; the file name is lower-case letters, digits and dashes, at most 31 characters. A package holds at most 4 tag files.

The tag's key in the settings is `<plugin name>.<file name>`, for example `hotjar-pack.hotjar`.

## The tag file

```json
{
  "name": "Hotjar",
  "id": {"label": "Site ID", "pattern": "^[0-9]{6,9}$"},
  "script": "https://static.hotjar.com/c/hotjar-{id}.js?sv=6",
  "hosts": ["*.hotjar.com", "*.hotjar.io"],
  "websockets": true,
  "category": "analytics"
}
```

| Field | Rule |
|---|---|
| `name` | The name shown on the page. Plain text, at most 60 characters. |
| `id.label` | What the owner is asked for, for example "Site ID". At most 40 characters. |
| `id.pattern` | A regular expression, at most 120 characters. It must be anchored with `^` and `$`. The owner's ID is checked against it before it is saved. |
| `script` | An `https` address, at most 512 characters. `{id}` may appear once and is replaced by the owner's ID. Its host must be listed in `hosts`. |
| `hosts` | One to 8 host names, exact (`cdn.example.com`) or one wildcard level (`*.example.com`). They are added to the page policy only while the tag is on. |
| `websockets` | Optional. `true` also allows `wss://` connections to `hosts`. |
| `category` | `analytics`, `marketing` or `functional`. The tag loads only after the visitor agrees to that category. |
| `noscript` | Optional `https` image address, may use `{id}`, host listed in `hosts`. Shown only when the consent banner is off. |

Unknown fields are refused. A broken file refuses the whole package with a message that names the file, such as `tags/hotjar.json: script host is not listed in hosts`. `hatamoto plugin pack` runs the same checks and prints a summary of the tags and their hosts.

## What the owner sees

The install prompt shows each tag's hosts, its consent category and the ID it needs, instead of a permission list, because a tag pack asks for no permissions. After install, each tag appears in Settings > Tracking labelled "Tag pack v1.0.0", off until the owner turns it on and enters an ID. The page policy allows a pack's hosts only while the package is installed, on and not blocked, and the tag's switch is on.

An update that adds a host or changes a tag's category needs the owner to confirm again, like any update that widens access. Uninstalling or turning off the plugin removes the tag from the page at once; its saved ID is kept until the next save of Settings > Tracking, which drops it.

## Registries

A registry index lists a tag pack as a normal plugin with a `tags` list (name, category, hosts) that the index builder reads from the package. The directory and the install prompt show it before anything is downloaded. Community packs are eligible: a tag pack runs no code and needs no permission.

Servers older than this release refuse a package with `tags/` ("needs plugin.wasm or widgets/").
