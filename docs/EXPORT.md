# Export and import

`<admin>` stands for the install's private admin address, the path that `hatamoto admin-url` prints. Tests and the QA harness pin `HATAMOTO_ADMIN_PATH=/admin`; real installs get a random address.

`hatamoto export site.tar.gz` writes the whole site; `hatamoto import site.tar.gz` rebuilds it in an empty database (backup, moving hosts, no lock-in).

```
hatamoto export site.tar.gz            # DATABASE_URL, MEDIA_UPLOAD_DIR
hatamoto export site.tar.gz --no-submissions
DATABASE_URL=postgres://new-host/hatamoto hatamoto import site.tar.gz
hatamoto user reset-password --email you@example.com
```

Export and import are command line only. Admin → Settings → Import & export → Site export & import (`<admin>/settings/export`, owners only) is an informational page: it shows these two commands and links here. It has no upload or download button and changes nothing. The WordPress import wizard is the other tab of that section.

## What is included

| Archive entry | Content |
|---|---|
| `data/hatamoto_entity_schemas.jsonl` | content types, with URL settings |
| `data/hatamoto_users.jsonl` | users **without password hashes**, lockout state or sessions |
| `data/hatamoto_site_config.jsonl` | site settings |
| `data/hatamoto_entities.jsonl`, `..._entity_versions.jsonl` | pages, posts and every stored version |
| `data/hatamoto_terms.jsonl`, `..._entity_terms.jsonl` | tags, categories and custom term kinds (with parents and descriptions) and their assignments |
| `data/hatamoto_menus.jsonl`, `..._menu_items.jsonl` | menus |
| `data/hatamoto_redirects.jsonl` | redirects |
| `data/hatamoto_forms.jsonl`, `..._form_submissions.jsonl` | forms and, unless `--no-submissions`, their submissions |
| `data/hatamoto_media.jsonl`, `media/<path>` | media rows and the files (originals and image variants) |
| `data/hatamoto_authors.jsonl` | legacy authors (name, bio, avatar); entities keep their `author_id` |
| `data/hatamoto_user_people.jsonl` | which login owns which person; the people themselves are `person` entities in `hatamoto_entities` (with `byline_person_id` and `byline_org` on the entities they credit) |
| `data/hatamoto_section_templates.jsonl`, `..._internal_link_rules.jsonl` | reusable sections, internal-link rules |
| `data/hatamoto_agent_robots_policy.jsonl`, `..._plugin_grants.jsonl` | the AI-crawler robots policy and the permissions granted to plugins |

**Form submissions are included by default** (a backup should be complete) but hold visitor data: use `--no-submissions` for an archive you will share. Users are included because they own content; their emails are personal data too.

**Not included:** API keys and sessions (secrets), crawler hits, analytics, 404 logs and the link index (history or derived data; the link index fills again as pages are saved), plugin credentials, settings and stored data. **Installed plugin and theme packages** (the `hatamoto_packages` table), the active theme and theme settings are not archived either, on purpose: a package is code the new host must trust by its own keys. Reinstall them on the new host (`hatamoto package install`, then `hatamoto theme activate`); packages already installed do not stop an import.

## Format (version 1)

A gzip-compressed tar, written and read as a stream:

1. `format.json`, always first: `{"format":"hatamoto-export","version":1,"created_at":...,"schema_version":<newest migration>,"include_form_submissions":bool,"tables":[...]}`. `tables` is the order of the data files (parents before children).
2. `data/<table>.jsonl`: one JSON object per row, as PostgreSQL's `to_jsonb(row)` renders it (all columns, UUIDs and timestamps as strings with full precision). Generated columns (the search vector) are left out.
3. `media/<storage_path>`: uploaded files, paths relative to `MEDIA_UPLOAD_DIR`.
4. `manifest.json`, always last: row counts per table and the media file count. The import checks it, so a truncated archive is rejected.

The `version` field changes only when this layout changes; an importer refuses a version newer than it knows. Rows carry whatever columns the exporting version had: columns missing from an older archive take their defaults on import.

## Import rules

- Only into an **empty** database: the import runs migrations, then refuses (`database is not empty`) if there are any entities, users, terms, menus, redirects, forms, media, authors, people links, section templates or link rules. The built-in schemas and site config that migrations seed are replaced by the archive's.
- **Older archives:** an archive written before people existed has authors but no `person` entities and no `hatamoto_user_people` file. It imports as it is (the built-in `person` type is kept), and once the import has committed, every author gets a non-public person and the posts are re-pointed at it, as the upgrade does. The file is optional and the format `version` stays 1; a binary from before people refuses an archive that lists it (unknown table), so import new archives with the new binary. If the people step fails, the import itself is kept and the error says so; the only expected cause is a content type of your own with the slug `person` in the archive, so rename it before exporting.
- IDs and timestamps (`created_at`, `updated_at`, `published_at`) are preserved; parent links are restored without touching `updated_at`.
- Everything runs in one transaction; any error (including a bad manifest) rolls the database back; media files are staged in a temp directory under `MEDIA_UPLOAD_DIR` and linked into place just before the commit, and a failure removes only the files the import created.
- Users are created with a password that cannot match anything. Set one with `hatamoto user reset-password --email ...`.
- Media files are written under `MEDIA_UPLOAD_DIR`. The import **refuses to overwrite**: a file that already exists there fails the import (nothing is deleted). Every media path in the archive (entry names, `storage_path`, variant paths) must be relative and stay inside the upload directory; `storage_provider` must be `local`.
- An archive from a newer Hatamoto (its `schema_version` is above the database's) is refused: upgrade and migrate first, so no column is dropped silently.
- Limits: a data row may be at most 64 MB, the decompressed archive at most 64 GiB, and the media bytes must match the manifest.

Memory use does not depend on the site size: rows are streamed, each exported table is spooled to a temporary file (in `$TMPDIR`) before it is added to the archive.
