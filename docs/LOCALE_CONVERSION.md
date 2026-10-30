# Converting region page trees to locales

Some sites model regions as page trees: a top-level page per region (`/xx`, `/yy`) with the regional pages below it, and a region attribute on each page. Core has a locale per entity, translation groups and URL segments (see [URL_PATTERNS.md](URL_PATTERNS.md#locale)). The converter moves such a site to the core model in one locked step, shows what it will do first, and can undo it.

Run it from the command line (`hatamoto locales convert`, see [CLI.md](CLI.md#locales-convert)) or from the admin (Settings > General > Locales > Convert; owners only). Both use the same code and the same report.

## When to use it

Use it when all of these hold:

- Several top-level pages each root a tree of regional pages, and each page carries an attribute (for example `region`) that names its region.
- You want those trees to become locales: `/xx/...` and `/yy/...` URLs that stay as they are, hreflang between translations, a language switcher.

Do not use it on a site that already lists locales, or to add a language to a single-tree site: create the locale in the site settings instead.

## Flow

1. **Suggest** (optional). `hatamoto locales convert --suggest` reads the data, prints a plan on stdout and warnings on stderr, and writes nothing. It looks for the top-level attribute whose value matches the slug of the most root pages, one locale per matching root, and for an attribute whose value links pages across locales. Fill in any `tag` it left empty.
2. **Dry run.** `hatamoto locales convert --plan plan.json` validates the plan, analyses the data and prints the report. Nothing is written. Exit code 0 means the plan can be applied, 2 means blocking errors (the report lists them), 1 means usage or IO trouble. `--text` prints a readable summary, `--json` (the default) the full report.
3. **Read the report.** Check the per-locale counts, `path_changes` (must be empty), renamed collisions, groups and conflicts, unmatched attribute values and the site settings change.
4. **Apply.** `hatamoto locales convert --plan plan.json --apply` re-analyses under the lock and applies only if there are no blocking errors. It prints the conversion id; keep it for a revert. In the admin, a passing dry run offers Apply; the confirm page is behind sudo and needs the typed word `convert`. The admin also refuses a report that went stale: if the data changed since the dry run it says so and you run the dry run again.
5. **Revert**, if needed (see below).

## Plan format

The plan is a JSON file (or the admin textarea). It is strict: unknown fields are rejected.

| Field | Type | Required / default | Validation (error code) |
|---|---|---|---|
| `version` | int | optional, default `1` | must be `1` (`unsupported_version`) |
| `locales` | array of `{tag,segment,root_slug}` | required, 2 to 32 entries; the order is the hreflang order | fewer than 2: `need_two_locales`; more than 32: `too_many_locales` |
| `locales[].tag` | string, BCP 47 | required | valid, unique after normalising, not `und` (`bad_tag`, `duplicate_tag`) |
| `locales[].segment` | string | required | lowercase letters, digits and single hyphens, at most 32 characters, unique, not a reserved route (`bad_segment`, `duplicate_segment`, `reserved_segment`) |
| `locales[].root_slug` | string | required | valid slug (`bad_root_slug`); exactly one published top-level row of `root_schema` has it (`root_not_found`, `root_ambiguous`); unique (`duplicate_root`) |
| `root_schema` | string | optional, default `"page"` | the schema exists (`unknown_schema`) |
| `default` | string | required | one of the listed tags (`default_not_listed`) |
| `x_default` | string | optional, default `""` (none) | `""` or one of the listed tags (`x_default_not_listed`) |
| `prefix_all` | bool | optional, default `true` | `false` is accepted, but then the path check normally fails (`path_changes`) |
| `locale_from.attribute` | string | required | non-empty (`missing_attribute`) |
| `locale_from.map` | object, attribute value to tag | optional, default `{}` | every value is a listed tag (`map_unknown_tag`) |
| `locale_from.fallback` | `"root"` or `"shared"` | optional, default `"root"` | `root`: a page without a mapped value takes the locale of its nearest region-home ancestor, none means shared. `shared`: a map miss means shared (`bad_fallback`) |
| `group_from.attribute` | string | optional (absent: no groups) | non-empty when present (`bad_group_attribute`); pages sharing a value form one translation group |
| `shared` | string | optional, default `"und"` | only `"und"` is accepted (`bad_shared`) |
| `strip_attributes` | array of string | optional, default `[]` (keep all data) | no duplicates (`duplicate_strip_attribute`); must not contain `locale_from.attribute` while a schema override still matches on it (`strip_needed_attribute`) |
| `home.slug` | string | optional, default `"home"` | valid slug (`bad_home_slug`) |
| `home.on_conflict` | `"rename-archived"` or `"abort"` | optional, default `"rename-archived"` | a published page that collides always blocks (`published_collision`) (`bad_on_conflict`) |

Example:

```json
{
  "locales": [
    {"tag": "en-XX", "segment": "xx", "root_slug": "xx"},
    {"tag": "en-YY", "segment": "yy", "root_slug": "yy"}
  ],
  "default": "en-XX",
  "x_default": "en-XX",
  "locale_from": {"attribute": "region", "map": {"xx": "en-XX", "yy": "en-YY"}, "fallback": "root"},
  "group_from": {"attribute": "translation_key"}
}
```

Blocking errors (the dry run exits 2, apply refuses): every validation code above, plus `path_changes`, `group_two_in_locale`, `published_collision`, `root_not_found`, `root_ambiguous` and `trigger_guard_missing` (a site on an older database layout: run `hatamoto migrate up` first). Warnings never block: `attribute_without_home`, `unmatched_value`, `home_children_shadow`.

## What the converter does

Inside one transaction, in this order:

1. Sets the locale of each page: from `locale_from.map`, else from the region-home ancestor, else shared (`und`). Pages in a group get one new translation group id per distinct `group_from` value that two or more pages share; a page alone with its value stays ungrouped.
2. Renames non-published pages that would collide with a region home or its children to `<slug>-old-<n>` (per schema, locale and parent; `n` is the first free number).
3. Gives each region home the slug `home.slug`; the home stays top-level.
4. Moves the direct children of a region home to the top level. Deeper pages keep their parents.
5. Removes the keys named in `strip_attributes` (none by default).
6. Edits schema URL overrides: for an override that matches on `locale_from.attribute`, only its `sitemap_group` key is removed. `url_pattern` and the `attribute`/`equals` match stay, so URLs do not change.
7. Writes the site settings in one update: `language` (the default), the locale list, `prefix_all` and `x_default`.
8. Stores the conversion record (the plan, the report, before and after values of every changed row and of the settings).

The converter keeps the region attribute on every page (unless you strip it) and every `url_pattern`. Themes can still read the attribute.

The path check plays the role of the settings validation: it computes every published page's public path before and after with the real URL code and blocks the apply when any path would change.

## Locking

Apply and revert take a write lock on the entities and schemas tables and on the site settings row. Editors' saves wait while it is held and fail if they wait more than 5 seconds; public pages keep being served from the old data until the commit. If the lock cannot be taken in 5 seconds, the command fails with "editors may be saving, retry" and nothing is written. Apply is all or nothing: any failure rolls everything back. The conversion keeps each page's modified date (`updated_at`), so `dateModified` and sitemap `lastmod` do not change.

## Cache pickup

The converter writes straight to the database. A running server polls the content change log (every 3 seconds in production, every second in development) and then drops its cached site settings, URL registry and rendered pages. No restart is needed. The CLI prints this note after a write.

## Revert

`hatamoto locales convert --revert ID` (admin: History, Revert; behind sudo, typed word `revert`) undoes a conversion in one locked transaction, in the reverse order. `--history` lists conversions, newest first.

It refuses, exit code 2 with a list of everything it found, when since the apply:

- a converted page was edited (its modified date changed), moved, re-linked to another translation group, or deleted (`revert_conflict`);
- a schema's settings changed, or the locale settings changed (`revert_conflict`);
- pages were created in a converted locale ("stranded" pages, listed with a count and a sample) (`revert_conflict`).

`--force` (admin: the Force option on the refusal) proceeds anyway. Changed pages are still restored to their recorded before-values and the overwritten edits are listed in the output. Stranded pages are left untouched and listed.

Two refusals can never be forced:

- `already_reverted`: the conversion was reverted before.
- `newer_conversion`: a later conversion is still applied; revert that one first.

A clean revert restores exactly the recorded columns (locale, slug, parent, translation group, stripped attributes) and the settings and schema JSON; modified dates were never touched.

## Checklist before running

- Back up the database ([BACKUPS.md](BACKUPS.md)).
- Run the migrations (`hatamoto migrate up`) so `trigger_guard_missing` does not block.
- Make sure the site has no locale list yet.
- Run `--suggest`, review the plan, fill in empty tags.
- Run the dry run until `path_changes` is empty and there are no blocking errors.
- Read the renamed collisions and the unmatched values.
- Pick a quiet time; editors wait up to 5 seconds during the apply.
- Note the conversion id printed by the apply.
- After the apply, open a page of each locale and the home of each segment.
