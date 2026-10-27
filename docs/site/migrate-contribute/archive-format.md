---
title: Archive format
nav_title: Archive format
description: What a site export archive holds and leaves out, how the file is laid out and the rules an import follows.
---

This page describes the full-site archive that `hatamoto export` writes and `hatamoto import` reads: the commands, what is included and left out, the file layout (format version 1) and the import rules. To move a site with it, follow [Export a site and import it elsewhere](export-import.md).

## Overview

```bash
hatamoto export site.tar.gz                    # uses DATABASE_URL and MEDIA_UPLOAD_DIR
hatamoto export site.tar.gz --no-submissions   # leave out form submissions
DATABASE_URL=postgres://new-host/hatamoto hatamoto import site.tar.gz
hatamoto user reset-password --email you@example.com
```

| Command | Arguments | Meaning |
|---|---|---|
| `hatamoto export <file>` | `--no-submissions` | Write the whole site to `<file>`. `-` writes to standard output. |
| `hatamoto import <file>` | none | Run migrations, then rebuild the site from `<file>` in an empty database. `-` reads standard input. |

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Both commands need `DATABASE_URL` and read media from, or write it to, `MEDIA_UPLOAD_DIR` (default `./uploads`). They are command line only. The admin page Settings, Import & export, Site export & import (`<admin>/settings/export`, owners only) is informational: it shows these commands and has no upload or download button.

Export writes beside the target and renames the file into place when it finishes, so a failed export never leaves a half archive under the final name. This is a content backup, not a full server backup. See [Backups](../operate/backups.md) for the database dump, secrets and media directory that a complete restore needs.

## What is included

| Archive entry | Content |
|---|---|
| `data/hatamoto_entity_schemas.jsonl` | Content types, with URL settings. |
| `data/hatamoto_users.jsonl` | Users **without password hashes** or lockout state. Sessions are not exported. |
| `data/hatamoto_site_config.jsonl` | Site settings. |
| `data/hatamoto_entities.jsonl` and `data/hatamoto_entity_versions.jsonl` | Pages, posts and every stored version. |
| `data/hatamoto_terms.jsonl` and `data/hatamoto_entity_terms.jsonl` | Tags, categories and custom term kinds with their assignments. |
| `data/hatamoto_menus.jsonl` and `data/hatamoto_menu_items.jsonl` | Menus. |
| `data/hatamoto_redirects.jsonl` | Redirects. |
| `data/hatamoto_forms.jsonl` and `data/hatamoto_form_submissions.jsonl` | Forms and, unless `--no-submissions`, their submissions. |
| `data/hatamoto_media.jsonl` and `media/<path>` | Media rows and the files, originals and image variants. |
| `data/hatamoto_authors.jsonl` | Legacy authors. Entities keep their `author_id`. |
| `data/hatamoto_user_people.jsonl` | Which login owns which person. The people themselves are `person` entities. |
| `data/hatamoto_section_templates.jsonl`, `data/hatamoto_internal_link_rules.jsonl` | Reusable sections and internal linking rules. |
| `data/hatamoto_agent_robots_policy.jsonl`, `data/hatamoto_plugin_grants.jsonl` | The AI crawler robots policy and the permissions granted to plugins, when the database has these tables. |

**Form submissions are included by default**, because a content backup should be complete, but they hold visitor data. Use `--no-submissions` for an archive you will share. Users are included because they own content, and their emails are personal data too.

## What is left out

- API keys and sessions, which are secrets.
- Crawler hits, analytics, 404 logs and the link index. They are history or derived data, and the link index fills again as pages are saved.
- Plugin credentials, settings, stored data and plugin schemas.
- Installed plugin and theme packages (the `hatamoto_packages` table), the active theme and theme settings.

Packages are left out on purpose: a package is code the new host must trust by its own keys. Reinstall them on the new host with `hatamoto package install`, then run `hatamoto theme activate <name>`. Packages that are already installed do not stop an import.

## Format, version 1

The archive is a gzip-compressed tar, written and read as a stream:

1. `format.json`, always first: `{"format":"hatamoto-export","version":1,"created_at":...,"schema_version":<newest migration>,"include_form_submissions":bool,"tables":[...]}`. The `tables` list is the order of the data files, parents before children.
2. `data/<table>.jsonl`: one JSON object per row, as PostgreSQL's `to_jsonb(row)` renders it. Generated columns such as the search vector are left out.
3. `media/<storage_path>`: uploaded files, with paths relative to `MEDIA_UPLOAD_DIR`.
4. `manifest.json`, always last: row counts per table, the media file count and the media byte count. The import checks it, so a truncated archive is rejected.

The `version` field changes only when the layout changes, and an importer refuses a version newer than it knows. Rows carry whatever columns the exporting version had, and columns missing from an older archive take their defaults.

Memory use does not depend on the site size. Rows are streamed, and each exported table is spooled to a temporary file in `$TMPDIR` before it is added to the archive.

## Import rules

- **Only into an empty database.** The import runs migrations first, then refuses with `database is not empty` if there are any entities, users, terms, menus, redirects, forms, media, authors, people links, section templates or link rules. The built-in schemas and site config that migrations seed are replaced by the archive's.
- **Newer archives are refused.** If the archive's `schema_version` is above the database's, upgrade and migrate first, so no column is dropped silently.
- **IDs and timestamps are preserved**, including `created_at`, `updated_at` and `published_at`. Parent links are restored without touching `updated_at`.
- **One transaction.** Any error, including a bad manifest, rolls the database back. Media files are staged in a temporary directory under `MEDIA_UPLOAD_DIR` and linked into place right before the commit. A failure removes only the files the import created.
- **Users get a password that cannot match anything.** The command prints a count and tells you to run `hatamoto user reset-password --email <email>` for each.
- **Media is never overwritten.** A file that already exists under `MEDIA_UPLOAD_DIR` fails the import, and nothing is deleted. Every media path must be relative and stay inside the upload directory, and `storage_provider` must be `local`.

## Older archives and people

An archive written before people existed has authors but no `person` entities and no `hatamoto_user_people` file. It imports as it is. After the import commits, every author gets a non-public person and the posts are re-pointed at it, as an upgrade does. The `version` stays 1, but a binary from before people refuses an archive that lists the new table, so import new archives with a new binary. If the people step fails, the import itself is kept and the error says so. The only expected cause is a content type of your own with the slug `person`, so rename it before exporting.

## Limits and errors

| Limit or message | Meaning |
|---|---|
| `database is not empty` | The target holds content. Use a new, empty database. |
| Archive `schema_version` above the database's | Refused. Upgrade Hatamoto on the target and migrate first. |
| Archive `version` above 1 | Refused by this importer. |
| Data row over 64 MB | Refused. |
| Decompressed archive over 64 GiB | Refused. |
| Media bytes that do not match the manifest | Refused, and the database is rolled back. |
| `unsafe media path` | A media path in the archive is absolute or climbs out of the upload directory. |
| `usage: hatamoto export <file.tar.gz> [--no-submissions]` | Give exactly one file name. |
