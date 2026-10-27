---
title: How these docs are made
nav_title: How these docs are made
description: Where the docs source lives, how pages are written and grouped, the Markdown the publisher accepts, and how to check and publish them.
---

Every page you are reading is a Markdown file in the Hatamoto repository under `docs/site/`. A small Go tool, `docs-publish`, converts those files to the HTML subset the CMS accepts and writes them into a running Hatamoto site as ordinary content. The site then renders them with a generic documentation layout: a sidebar tree, breadcrumbs, an on-page table of contents and previous and next links. Nothing is generated from code, so every page is hand-written and checked against the source by its author.

This page lists the source layout, the page types, the front matter, the accepted Markdown, the lint, and how the tool publishes, for contributors and for anyone who wants to host the same docs on their own site. To send a doc change, follow [Make your first contribution](contributing.md).

## Source layout

```text
docs/site/
  manifest.json                 sections, groups, order and page slugs (the single source of structure)
  index.md                      the landing page, published at /docs
  README.md                     the tool, the lint and the owner bootstrap (not published)
  STYLE.md                      the style guide for writers (not published)
  doc-schema.json               the content type that the pages are stored in
  <section>/index.md            one hub page per section
  <section>/<page>.md           one file per page
```

`manifest.json` owns the structure. Each section has a `slug`, a `title`, a `description` and two ordered lists of page slugs: `guides` and `reference`. A page appears in exactly one of them, and the list decides the page type. An entry is a slug, or an object such as `{"slug": "settings-users-email", "title": "Settings reference"}` whose title is a fallback label. An older manifest with one flat `pages` list per section still loads. The section's own `index.md` is implicit and not listed. Sidebar order, section order and previous and next links all come from this file and nothing else. A page that exists on disk but not in the manifest, or the reverse, is a lint error; `README.md` and `STYLE.md` are the only exceptions. Contributors who add or move pages edit the manifest in the same change.

The manifest also has a `moved` map, empty today, which `--apply` turns into permanent redirects, as described under "How `--apply` behaves" below. Each key is the `section/slug` of a page that no longer exists, and its value is the `section/slug` of the page that replaces it. The lint checks that every key is no longer a manifest page and every value is one.

```json
{
  "slug": "migrate-contribute",
  "title": "Migrate and contribute",
  "description": "Move an existing site into Hatamoto and keep its old links working, or help build Hatamoto itself.",
  "guides": ["move-your-site", "wordpress-import"],
  "reference": ["redirect-rules", "testing"]
}
```

## Guides and reference pages

Every page is one of two types, and `docs/site/STYLE.md` holds the full rules for each. Read it before you write a page.

| | Guide | Reference |
|---|---|---|
| Answers | "How do I do X?" | "What exactly does X do, and what are all its options?" |
| Shape | one task, start to finish | every item of one area, findable by scanning |
| Title | verb phrase, such as "Install the binary" | noun phrase, such as "Template functions" |
| Headings | exactly `## Before you start`, `## Steps`, `## Check it worked` | whatever makes it scannable, usually `## Overview` first and `## Limits and errors` last |
| Length | 500 to 900 words | no limit |
| Ends with | Next steps, from the `next` front matter key | nothing extra |

Each section is written for one reader, named in the style guide: a site owner for Getting started and Using the admin, a web developer for Themes and Plugins, a system administrator for Architecture, How updates work and Operating it, and a site owner for the guides of AI and SEO and of this section. Guides explain every term from the style guide's jargon list the first time it appears; reference pages may use any term but define Hatamoto's own terms on first use.

## Front matter

Every file starts with front matter between `---` lines: `title` and `description` are required, `nav_title` is optional, and `next` is required on guides and forbidden everywhere else. The format is plain `key: value`, one key per line, not full YAML. No other key is allowed.

```markdown
---
title: Install the binary
nav_title: Install the binary
description: Download Hatamoto, point it at a Postgres database and start it.
next: first-run.md, create-users.md
---
```

| Key | Used for |
|---|---|
| `title` | The page heading. The layout renders it as the h1. |
| `nav_title` | The label in the sidebar, breadcrumbs, cards, previous and next links and other pages' Next steps. Defaults to `title`. |
| `description` | One plain sentence under 160 characters: the card text on the section index and the landing page, the meta description and search snippets. |
| `next` | Guides only: 2 to 3 relative `.md` paths, separated by commas, for the Next steps block. |

The body therefore starts at `##`. A top-level `#` heading in the body is an error.

### The next key

The `next` targets resolve like links in the body: relative to the file, such as `first-run.md` in the same section or `../operate/backups.md` in another. They take no `#fragment` and no absolute URL, each must be a manifest page, and none may repeat. The tool renders them, in the order given, as a `Next steps` heading and a list of links at the end of the guide; the link text is the target's `nav_title`, or its `title`. A `## Next steps` heading written in the body is a lint error, because the tool owns that block.

## The allowed Markdown subset

The converter is deliberately small and emits only what the CMS's rich-text sanitiser keeps. Anything outside the subset is **rejected with the line number**, never silently passed through or stripped. That is why the published page always matches the source.

Allowed:

- Headings `##`, `###` and `####`.
- Paragraphs, bold, italic and inline code.
- Bullet and numbered lists, nested by two-space indentation. A fenced code block or a table indented to a list item's text stays inside that item, so a numbered step can carry its command. A list that continues across such a block stays one list.
- Fenced code blocks with a language tag. Their content is escaped, and the language becomes a `language-` class.
- Links, as relative `.md` paths or absolute URLs.
- Pipe tables.
- Blockquotes and horizontal rules.
- Images only with an `https` or repository-relative source. No images are published today.

Rejected:

- Raw HTML of any kind, including `<details>`, `<kbd>`, `<sup>` and comments.
- A `#` heading in the body.
- Unknown constructs such as footnotes and definition lists.

Link to other pages with a relative path to the Markdown file, for example `[Template functions](../themes/template-funcs.md)`. The publisher rewrites it to the site path, such as `/docs/themes/template-funcs`, and fragments are kept. The lint runs the converter over every page, so raw HTML and the other rejected constructs fail it, and it checks each relative link: a target that is not in the manifest fails, and so does a `#fragment` that matches no heading of the target page (ids are computed the way the layout does). External links are left as they are.

Headings get their `id` attributes **at render time only**, by the layout, from the heading text, with duplicates numbered. The stored content never has ids, because the sanitiser strips them. You therefore cannot hand-write anchors, but a link to `#some-heading` works once the id exists.

## House style

The lint also enforces the project's prose rules, so a contributor gets the failure before review. The converter and link checks above are part of the same run:

- No em or en dashes. Use commas, colons, parentheses or a new sentence, and write ranges as "1 to 5".
- None of the words that the repository's neutrality check bans.
- No internal slice or phase labels.
- Example hosts use `example.com`, and URL examples avoid language or region path segments (use `/blog/hello`).
- Every environment variable, command, flag and path is verified against the code. Where an older developer document and the code disagree, the code wins and the page states the code's behaviour.

The lint also enforces the page types: a guide whose `next` has fewer than two or more than three targets, a `next` key on a reference page, an index or the landing page, a `## Next steps` heading in a body, a page listed twice, a page with the slug `docs` (it would compete with the landing page at `/docs`) and a bad `moved` entry are all errors. The rest of the style guide, such as the guide headings, the word counts and the glossing of terms, is checked in review.

## Lint and publish

The tool lives in `backend/tools/docs-publish`. It is a nested Go module with no third-party dependencies, and the Makefile wraps it:

```bash
# Offline check of the whole tree. Exits 1 on errors, makes no network call.
make docs-publish ARGS=--lint

# The same, run from the tool's directory.
cd backend/tools/docs-publish && go run . --lint --root ../../../docs/site

# Dry run (the default): prints the plan, writes nothing.
make docs-publish

# Publish for real.
make docs-publish ARGS=--apply

# Tool tests (vet and go test inside the module).
make docs-publish-test
```

| Flag | Meaning |
|---|---|
| `--root DIR` | The docs source directory (the Makefile passes `docs/site`). |
| `--lint` | Validate the sources offline and stop. |
| `--apply` | Perform the writes. Without it, the run is a dry run that only reads. |
| `--env-file FILE` | Read credentials from a file of `KEY=VALUE` lines. Without it, `.docs-publish.env` is used if present. Under `make`, relative paths are resolved from the repository root. |
| `--schema-id ID` | The id of the `doc` content type. Required to publish unless `HATAMOTO_DOC_SCHEMA_ID` is set. Not needed for `--lint`. |

A dry run lists, for every page, whether it would be created, updated, published or skipped, and how many sidebar items the menu would hold and, when `moved` has entries, one line per redirect. It issues only read requests. It also warns, and `--apply` repeats the warning, about published pages that exist on the site but not in the manifest, with their URLs. The tool never deletes or unpublishes anything, so a removed or renamed page stays live until you unpublish it in the admin. Always review the dry run before `--apply`.

Exit codes are 0 for success, 1 for a failure or lint errors, 2 for a usage error or missing credentials, and 3 when an owner prerequisite is missing.

## Credentials

The tool talks to the site through its public interfaces, the REST API and the MCP endpoint, never the database. It reads its settings from the environment or the env file and never accepts the key as a command-line flag, because arguments show in the process list:

| Variable | Meaning |
|---|---|
| `HATAMOTO_URL` | Base URL of your site, `https` or loopback `http`, for example `https://example.com`. |
| `HATAMOTO_API_KEY` | An API key with the scopes `read:entities`, `write:entities` and `write:menus`, plus `write:redirects` only when `moved` has entries. |
| `HATAMOTO_DOC_SCHEMA_ID` | The id of the `doc` content type (or pass `--schema-id`). |

Mint the key on the server with a file outside the repository, and put the three values in `.docs-publish.env` (any `*.env` file is already ignored by git):

```bash
# Run on the server as the service user, with DATABASE_URL in the environment.
hatamoto apikey create --name docs \
  --scopes read:entities,write:entities,write:menus --out ~/secrets/docs.key

cat > .docs-publish.env <<'ENV'
HATAMOTO_URL=https://example.com
HATAMOTO_API_KEY=<contents of ~/secrets/docs.key>
HATAMOTO_DOC_SCHEMA_ID=<id of the doc schema>
ENV
```

For an env file outside the repository root, use `make docs-publish ARGS="--env-file /path/to/file"`. Values in the environment win over the file. If credentials are missing, the tool exits with code 2 before any network call. It never prints the key, and it does not follow HTTP redirects, because the key would travel with them, so `HATAMOTO_URL` must be the site's final address. Writes through the API and MCP invalidate the page cache, so published changes appear without a restart. See the [MCP server](../ai-seo/mcp.md) for how API keys and scopes work.

## Owner bootstrap

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The tool does not create the content type or the menu, because creating a schema needs an owner session and the tool deliberately holds only an API key. A site owner does these steps once. `docs/site/README.md` is the authoritative checklist:

1. Sign in as the owner with `POST /api/v1/auth/login` (the answer holds the bearer token in `session_token`; a cookie session does not work for the API unless `ENABLE_COOKIE_AUTH` is set).
2. Create the `doc` content type from `doc-schema.json` in the admin schema builder (Settings, Entity models, Entity types), or with `POST /api/v1/schemas` and that file as the body (`curl -X POST "$SITE/api/v1/schemas" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data @docs/site/doc-schema.json`). Its URL pattern is `/docs/{path}`, so a page at `docs` then `themes` then `layout` lives at `/docs/themes/layout`, and its archive path is `/docs`. The file also sets `section_pages`, so the landing page, a `doc` with the slug `docs`, is served at `/docs` instead of the archive listing. The built-in Page type needs no change and no page is created by hand.
3. Create an empty menu at the location `sidebar`: `curl -X POST "$SITE/api/v1/menus" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"Docs sidebar","location":"sidebar"}'`, or Theme > Menus (`<admin>/theme/menus`) in the admin.
4. Mint the API key as above and note the id of the new `doc` type for `HATAMOTO_DOC_SCHEMA_ID`.
5. Run `make docs-publish`, read the plan, then `make docs-publish ARGS=--apply`.

If a prerequisite is missing, the tool stops before writing anything and names the owner step to perform (exit code 3).

## How `--apply` behaves

1. **Lint.** Any error stops the run.
2. **Preflight.** It takes the `doc` type's id from `--schema-id` or `HATAMOTO_DOC_SCHEMA_ID` (without one it stops with exit code 3), checks that the `sidebar` menu exists, and warns when the `doc` type does not have `section_pages` turned on.
3. **Entities.** The landing page is created first as the root `doc` entity `docs`, then sections as root `doc` entities, then their pages as children, so URLs are `/docs/<section>/<page>`. Each is found by its parent and slug, so a re-run updates instead of duplicating. Each write sends `content`, `description`, `nav_title` and `source_hash`. New and draft entities are published.
4. **Menu.** After the last publish, one call replaces the entire `sidebar` menu with the tree built from the manifest: each section, then under it a **Guides** and a **Reference** label item (a custom item without a link) holding that group's pages. Pages use entity links, so the hrefs follow the URL pattern. A group with no pages gets no label. The tool reads the stored menu first and skips the call when it already matches, so an unchanged run does not purge the page cache.
5. **Redirects.** Only when `moved` has entries, it creates one permanent (301) redirect from each old page path to its replacement, after the menu so the target exists. It only creates: a redirect that already exists is left alone (`redirect exists`), and one that points somewhere else is reported (`redirect conflict`) and not changed. With an empty `moved` map no redirect call is made. The rules are explained in [Redirect rules](redirect-rules.md).

### Idempotency

Each page stores a `source_hash`, the SHA-256 of its source file together with the converter version, its parent path under the manifest root and its converted HTML (which holds the resolved link targets and the Next steps block). A renamed or moved link target, or a converter fix, therefore updates the pages it affects even when their files did not change. A page whose hash matches the live one and which is already published is skipped with no write and no publish call, so running `--apply` on unchanged sources performs zero writes, in the pages, the menu and the redirects. Rate-limit responses are retried, honouring `Retry-After`.

## How the site renders it

Documents use the generic `single-doc` layout of the default theme, chosen for any content type whose slug is `doc`. The layout reads the `sidebar` menu and finds the current page in it, which supplies the highlighted item, the breadcrumb trail and the previous and next links in depth-first order. The landing page and each section index show the pages below them as cards, grouped under the Guides and Reference labels, with each page's `description` as the card text. If a page is absent from the menu, it still renders, without those extras. A custom theme can ship its own `layouts/single-doc.html`. The layout's markup and its behaviour on narrow screens are described in [Layouts](../themes/layout.md).

## Contributing a doc change

Edit or add the file following `docs/site/STYLE.md`, add new pages to the `guides` or `reference` list of their section in `manifest.json`, run the lint and `make check`, and open a pull request. Maintainers publish after merge with their own credentials. Never run `--apply` from a review branch.
