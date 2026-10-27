# Documentation sources

This directory holds the Hatamoto documentation as markdown. A small tool,
`backend/tools/docs-publish`, converts it to HTML and publishes it to a running
Hatamoto site through the REST API and the MCP endpoint. The site renders the
pages with the `single-doc` layout of the default theme.

Publish only after the change is merged. Never run `--apply` from a review
branch.

## Layout

```
docs/site/
  manifest.json        sections, page groups, order and slugs (the only source of order)
  STYLE.md             the technical-writing style guide (not a page, not published)
  index.md             the /docs landing page (published as the doc with slug `docs`)
  doc-schema.json      the body of the `doc` schema, for the owner bootstrap
  <section>/index.md   one index page per section
  <section>/<page>.md  the pages of a section
```

Read `STYLE.md` before writing or changing a page: it sets who each section
is for, how long a guide is, and how guides differ from reference pages.

`manifest.json` lists every section and, in each section, its page slugs in two
ordered groups. A section index (`index.md`) is implicit and is not listed. The
tool refuses to run when a file is missing from disk or present but not listed
(`STYLE.md` and `README.md` are not reported).

```json
{
  "root": "docs",
  "sections": [
    {
      "slug": "themes",
      "title": "Themes",
      "description": "One sentence for the section card.",
      "guides": ["your-first-theme", "add-a-section"],
      "reference": ["layouts", {"slug": "settings-users-email", "title": "Settings reference"}]
    }
  ],
  "moved": {"themes/old-slug": "themes/new-slug"}
}
```

- `guides` are task pages. Each one needs a `next` key (below). `reference`
  pages are lookup pages and have none. A slug appears once per section, in one
  group.
- An entry is a slug or `{"slug", "title"}`; the title is the sidebar label when
  the page has no `nav_title`.
- The older flat `pages` list is still read (no groups, no `next` rule), so old
  manifests keep working. Use `guides` and `reference` for new work.
- `moved` maps the `section/slug` of a page that no longer exists to the
  `section/slug` that replaces it. Keys must not be manifest pages, values must
  be. It is `{}` while no slug has changed. See Redirects below.

## Front matter

Every markdown file starts with front matter: `key: value` lines between two
`---` lines. No YAML features, no other keys.

```markdown
---
title: Install the binary
nav_title: Install the binary
description: One sentence, plain text, under 160 characters.
---
```

| Key | Required | Use |
|---|---|---|
| `title` | yes | Entity name and the page heading (h1). |
| `nav_title` | no | Label in the sidebar; defaults to `title`. |
| `description` | yes | Meta description and the `description` attribute. |
| `next` | guides only | 2 to 3 relative `.md` paths, comma separated, for the Next steps block. Not allowed on reference pages, section indexes or the landing page. |

The body starts at `##`. An h1 in the body is an error. The converter accepts
headings `##` to `####`, paragraphs, bullet and numbered lists (nested by
indentation; a fenced code block or table indented under an item stays inside
it and the list stays one list), fenced code with an optional language, inline code, bold, italic,
links, images with an https or relative source, blockquotes, pipe tables and
rules. Raw HTML, indented code blocks and unknown link schemes are errors with
a line number. Links to other pages are relative `.md` paths, for example
`[Layouts](../themes/layout.md#template-hierarchy)`; the tool rewrites them to site paths.

A guide ends with a Next steps block that the tool renders from `next`: an `h2`
"Next steps" followed by a list (`<ul class="hm-next-steps">`) with one link per
target, in the order given. The link text is the target's `nav_title`, else its
`title`. Never write a `## Next steps` heading in the body: it is a lint error.
A target that is not a manifest page is a lint error too.

```markdown
---
title: Add a section
description: Add a reusable block to a page in your theme.
next: preview-and-dev.md, ../admin/page-editor.md
---
```

Heading ids and the "On this page" list are added when the page is rendered, not
stored, so nothing in the markdown needs an id.

## Add a page

1. Create `docs/site/<section>/<slug>.md` with front matter. Slugs use lowercase
   letters, digits and hyphens.
2. Add the slug to the `guides` or `reference` list of its section in
   `manifest.json`. The order in the lists is the order in the sidebar and for
   previous and next links; guides come first.
3. Run the lint, then a dry run (see below).

## Commands

Run these from the repository root. `make docs-publish` runs the tool from
`backend/tools/docs-publish` with `--root ../../../docs/site`; extra flags go in
`ARGS`.

```bash
make docs-publish ARGS=--lint     # offline check, no credentials needed
make docs-publish                 # dry run: reads the site, prints the plan, writes nothing
make docs-publish ARGS=--apply    # create, update and publish, then replace the sidebar menu
make docs-publish-test            # go vet and go test of the tool
```

The lint checks the manifest against the files (duplicate pages, `moved`
entries), front matter including `next`, em and en dashes, banned words,
internal slice labels, h1 in bodies and a hand written Next steps heading. It also runs the
converter over every page, so raw HTML and other unsupported constructs fail,
and it resolves every relative `.md` link and every `#anchor` against the
heading ids the site layout generates. It also runs in `make check`.

The dry run prints one line per page (`create`, `update`, `publish` or `skip`),
the number of menu items and, when `moved` has entries, one line per redirect.
It only reads. Both the
dry run and `--apply` end the plan with a warning that lists the URL of every
published `doc` page that is no longer in the manifest (removed, renamed or
moved). The tool never deletes or unpublishes them; do that in the admin. `--apply`:

1. checks the owner prerequisites (below),
2. creates missing `doc` entities (the landing page `docs` first, then sections,
   then pages as their children) and updates changed ones, then publishes every
   entity that is not published,
3. replaces the whole `sidebar` menu so it only links pages that exist. Each
   section holds its pages under two label-only items, Guides and Reference (no
   link; an empty group is left out). The menu is read first and left alone when
   it already holds exactly the items that would be written,
4. creates the redirects of the `moved` map (below), last.

Each entity stores a hash (`source_hash`) of its source file, the converter
version, its parent path and its converted HTML, so moving a linked page or
fixing the converter updates the pages it affects. A second
`--apply` on unchanged sources performs no writes. The tool talks to the API
and MCP only, never to the database, so the page cache is invalidated by the
server.

Exit codes: 0 ok, 1 failure or lint errors, 2 usage or missing credentials,
3 an owner prerequisite is missing.

## Redirects

Each `moved` entry becomes a permanent (301) redirect from the old site path to
the new one, for example `/docs/themes/old-slug` to `/docs/themes/new-slug`. A
`section/index` value means the section path. The step runs after the pages and
the menu, so the target exists, and it only runs when `moved` is not empty: with
an empty map no redirect call is made and the key needs no redirect scope.

The step only creates and never deletes or edits:

| Output line | Meaning |
|---|---|
| `redirect create /a -> /b` | Missing, so it is created (a dry run prints it without creating). |
| `redirect exists /a -> /b` | The source already redirects to that target. Nothing is written, so a second `--apply` creates none. |
| `redirect conflict /a already points to /x, not changed (wanted /b)` | The source redirects somewhere else. Fix it in the admin; the tool will not touch it. |

A dry run only lists the existing rules. A published page that is still in the
site under a `moved` key is reported as `redirected, still live`. Unpublishing
it is a manual step in the admin.

The server checks redirects before it looks up a page (see
`backend/internal/server/entity_live.go` and `public_pages_loader.go`), so a
redirect wins over a still published page at the same path. That is intended
for a moved page. It also means a redirect left on a path you later reuse for a
live page would hide the page: remove the redirect in the admin first.

## Credentials

The tool needs `HATAMOTO_URL` (https, or http for a loopback host) and
`HATAMOTO_API_KEY`. It never accepts the key as a flag, so it cannot appear in
`ps` output or shell history, and it never prints it. The tool does not follow
HTTP redirects (the key would travel with them), so `HATAMOTO_URL` must be the
final address.

Create a key on the machine that runs Hatamoto, as the service user, with the
server's environment loaded (the command reads `DATABASE_URL`, so export it or
source the service's env file first). Write the key outside the repository:

```bash
hatamoto apikey create --name docs \
  --scopes read:entities,write:entities,write:menus \
  --out ~/.config/hatamoto/docs-publish.key
```

`read:entities` covers every read, including the redirect list of a dry run.
Add `write:redirects` only when `manifest.json` has `moved` entries; without it
a run with `moved` entries stops with an error that names the scope.

Copy the key file to the machine that publishes, then build the env file. The
file `.docs-publish.env` is ignored by git (the `*.env` rule); keep it that way.

```bash
{
  echo 'HATAMOTO_URL=https://example.com'
  printf 'HATAMOTO_API_KEY='
  cat ~/.config/hatamoto/docs-publish.key
  echo 'HATAMOTO_DOC_SCHEMA_ID=<id of the doc schema>'
} > .docs-publish.env
chmod 600 .docs-publish.env
```

Alternatively export the same variables in your shell. `--env-file FILE` reads
another file; the default is `.docs-publish.env` when it exists. For a file
outside the repository root, pass it through `ARGS`:

```bash
make docs-publish ARGS="--env-file /path/to/docs-publish.env"
make docs-publish ARGS="--apply --env-file /path/to/docs-publish.env"
```

Under `make` relative paths are resolved from the repository root (the Makefile
sets `DOCS_PUBLISH_BASE_DIR`, so do not set it yourself). When you run the tool
directly, relative paths are resolved from the working directory. Values from
the environment win over the file.

## Owner bootstrap

The API key cannot create schemas or menus, so the site owner prepares two
things once, in the admin or with an owner session. The tool checks for them and
stops with a message that names the missing step.

For the REST calls, sign in as the owner and keep the bearer token. The login
answer holds it in `session_token`; a cookie session does not work for the API
unless `ENABLE_COOKIE_AUTH` is set.

```bash
SITE=https://example.com
read -rsp 'Owner password: ' PW; echo
TOKEN=$(jq -n --arg e owner@example.com --arg p "$PW" '{email:$e,password:$p}' \
  | curl -s -X POST "$SITE/api/v1/auth/login" \
      -H 'Content-Type: application/json' --data @- | jq -r .session_token)
unset PW
```

The password is read without echo and sent on stdin, so it stays out of shell
history and the process list. When the steps below are done, end the session:

```bash
curl -s -X POST "$SITE/api/v1/auth/logout" -H "Authorization: Bearer $TOKEN"
```

1. **Create the `doc` schema.** It serves the landing page at `/docs` because
   `doc-schema.json` sets `section_pages`, so the built-in Page type needs no
   change. Either send the file as the body (run from the repository root; the
   answer holds the new schema id):

   ```bash
   curl -s -X POST "$SITE/api/v1/schemas" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     --data @docs/site/doc-schema.json | jq -r .id
   ```

   or, in the admin, open Settings, Entity models, Entity types, create a type
   with the properties and settings in `doc-schema.json` (`content` as rich
   text, `description`, `nav_title`, `source_hash` as text; URL pattern
   `/docs/{path}`; archive address `/docs`; turn on "Entities named like the
   archive address become section pages"). Copy the schema id.
2. **Create an empty menu** with the location `sidebar`. The tool replaces its
   items on every `--apply`; edits made to it by hand are overwritten.

   ```bash
   curl -s -X POST "$SITE/api/v1/menus" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"name":"Docs sidebar","location":"sidebar"}'
   ```

   > Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

   In the admin, open Theme > Menus (`<admin>/theme/menus`), create a menu and set its location to
   `sidebar`.
3. **Tell the tool the schema id**: set `HATAMOTO_DOC_SCHEMA_ID` (or pass
   `--schema-id`). The REST list API cannot tell the doc schema apart by name, so
   the id is required.

Then create the API key as described above. There is no page to create by hand:
the tool publishes `index.md` as a root `doc` entity with the slug `docs`, and
the schema setting serves it at `/docs`, as the index of the `/docs/{path}`
section. The tool warns during a run when the doc schema lacks `section_pages`
and the key can read schemas. It looks the schema up by the slug `doc`, so keep
that slug. No page may use the slug `docs` either: lint rejects it, because it
would compete with the landing page at `/docs`. After the first `--apply`, open `/docs` and a
section page to confirm both render. The landing page entity also answers at
`/docs/docs`, which the site does not link to; the sitemap and the menu use
`/docs`.

## Constraints worth knowing

- One `sidebar` menu per site: it belongs to the docs.
- Content is hand written and checked against the code; there is no generation
  from source.
- Never put the API key in a file that git tracks, in a command line, or in a
  log.
