# Docs style guide

This is the style guide for every page under `docs/site/`. Follow it when you write a new page or rewrite an old one. It is written for the people who write the docs, not for readers of the site, so it is not published: the publish tool reads only the pages that `manifest.json` names, plus the landing `index.md` and one `index.md` per section, and it does not look at the files in the root of `docs/site/` (this file and `README.md`).

When two sources disagree, use this order:

1. The code. What the program does is the truth.
2. This guide, for how to write it down.
3. The old pages, for what to cover (never for facts you have not checked).

`README.md` in this directory describes the publish tool, the lint and the commands. This guide describes the writing.

## The short version

1. Decide whether the page is a **guide** (one task) or a **reference** page (everything about one area). Never mix the two.
2. Write for the reader of that section (see the next table), not for yourself.
3. Open with what the reader will do and what they get. Never open with how it is built.
4. A guide uses exactly these H2 headings, in this order: `## Before you start`, `## Steps`, `## Check it worked`.
5. A guide is 500 to 900 words and ends with 2 to 3 Next steps, set in front matter with `next:`. Never write a `## Next steps` heading yourself.
6. The first time a term from the jargon list appears on a page, explain it in the same sentence.
7. Every page has a `description`: one plain sentence under 160 characters. It is the text on the page's card.
8. Every command, flag, environment variable, path and default is checked against the code.
9. Nothing is dropped. Detail that leaves a guide moves to a reference page.
10. No em or en dashes, no marketing words, hosts are `example.com`.

## Who reads each section

Each section has one main reader. Picture that person while you write. The "Knows" column is what you may take for granted; everything else gets a gloss or a plain alternative.

| Section | Sidebar title | Reader | Knows | Does not know | Wants |
|---|---|---|---|---|---|
| `getting-started` | Getting started | Capable non-developer: runs a small business site, can follow a command, has used a hosting panel | how to use a browser, copy a command into a terminal, what a domain is | Postgres, environment variables, Docker internals, Go | a running site and an owner account, today |
| `admin` | Using the admin | Capable non-developer: editor or site owner | how to write and publish on the web, menus, images | what an entity or schema is, how caching or webhooks work | to get a page, post or menu change done |
| `themes` | Themes | Web developer new to Hatamoto | HTML, CSS, a template language, HTTP, JSON, the command line | Hatamoto's lookup chain, sections, the editor markers, Go templates in detail | to change or build the look of a site |
| `plugins` | Plugins | Web developer new to Hatamoto | one programming language, HTTP, JSON, the command line | WebAssembly, the host API, hooks, signing | to add a feature without forking the server |
| `architecture` | Architecture | Sysadmin | Linux, services, Postgres, HTTP, TLS, reverse proxies, logs | how Hatamoto is put together inside | to know what runs, where data lives and what can fail |
| `updates` | How updates work | Sysadmin | services, files, signatures in general, backups | the update agent, the supervisor, channels | to update safely and roll back when needed |
| `operate` | Operating it | Sysadmin | systemd, Docker, reverse proxies, TLS, backups, SMTP | Hatamoto's settings and commands | a deployed, hardened, backed up instance |
| `ai-seo` | AI and SEO | Guides: site owner. Reference: developer | Guides: what search engines and AI tools are. Reference: HTTP, JSON, robots.txt | Guides: crawlers, sitemaps, structured data, MCP | Guides: control and see how AI tools use the site. Reference: exact behaviour |
| `migrate-contribute` | Migrate and contribute | Guides: site owner. Reference: developer | Guides: their old site and its admin. Reference: Go, git, Postgres | Guides: archives, redirect rules | Guides: move a site without losing links. Reference: build and release Hatamoto |

What this means for vocabulary:

- Guides in `getting-started`, `admin`, and the guides in `ai-seo` and `migrate-contribute`, follow the jargon list in full.
- Guides in `themes` and `plugins` may use general web terms (HTML, CSS, HTTP, JSON, template, request, response) without a gloss. Hatamoto terms (section, schema, lookup chain, hook, host API, package) still need one.
- Guides in `architecture`, `updates` and `operate` may use standard operations terms (systemd, TLS, reverse proxy, environment variable, Postgres, Docker volume, health check) without a gloss. Hatamoto terms (supervisor, update agent, channel, setup code) still need one.
- Reference pages may use any term, but a Hatamoto term is defined on the page the first time it appears (a gloss or a table row), and the opening links to the guide that uses the page.

## Guide or reference: decide first

| | Guide | Reference |
|---|---|---|
| Answers | "How do I do X?" | "What exactly does X do, and what are all its options?" |
| Shape | one task, start to finish | every item of one area, findable by scanning |
| Reader state | learning, following along | already knows the task, looking something up |
| Title | verb phrase: "Install the binary", "Add a section" | noun phrase: "Template functions", "The update CLI" |
| Length | 500 to 900 words | no limit |
| Ends with | Next steps (front matter `next:`) | nothing extra; no `next:` |
| Listed in `manifest.json` under | `guides` | `reference` |

A page is a guide if its reader wants to finish something. A page is reference if its reader wants to look something up. When a guide grows a table of every flag, every edge case or the history of a design, that part belongs on a reference page: move it there and link to it.

The page type is fixed by the manifest list the slug is in. You may not move a slug between `guides` and `reference` yourself; report it to the plan owner.

## The guide template

```md
---
title: Add a section
nav_title: Add a section
description: Add a new block type to your theme that editors can place on any page.
next: preview-and-dev.md, sections.md
---

Opening paragraph: what you will do and what you get, in 2 to 3 sentences.

## Before you start

- What the reader needs, as a bullet list.

## Steps

1. One action per step.

## Check it worked

What the reader sees when it worked, and the most common failure with its fix.
```

### Title and description

The title is a verb phrase that names the task. Sentence case: capital first letter only, plus names. The `description` is one sentence that says what the reader will do. See [Descriptions](#descriptions-card-text).

### Opening paragraph

Two or three sentences, before the first heading. Say what the reader will do and what they will have at the end. No history, no design reasons, no internals. If the reader must know one idea to follow the guide, state it here in one sentence.

### Before you start

A bullet list of what the reader needs before step 1: access (an owner account, a shell on the server), software and versions, and earlier guides, each linked. Keep it to what is truly needed. This heading is always present; even a short guide needs at least "A running Hatamoto site you can sign in to as an owner".

### Steps

A numbered list. One action per step, written as an instruction: "Open **Settings > Redirects**", "Run this command:". Put the command in a fenced block under the step, and the expected output in a second block marked `text` when it helps the reader confirm they are on track. Give the reason after the action, in one short sentence, only when the reader would otherwise wonder.

When a task has more than about eight steps, split it into parts with `###` headings under `## Steps` (for example `### Create the files`, `### Push the theme`), each with its own numbered list. Do not add other H2 headings between `## Before you start` and `## Check it worked`.

### Check it worked

One or two results the reader can observe: an address that answers, a screen that shows the change, a command that prints a known line. Then the most common failure and its fix, written as "If you see X, do Y." Link to a reference page for the full list of errors.

### Next steps

Do not write this heading. Put 2 to 3 targets in the front matter key `next:` (see [Front matter](#front-matter)), and the publish tool renders the block at the end of the guide, followed by the usual previous and next page links.

Pick targets in this order:

1. The guide a reader most likely does next. In a path (such as Getting started), the first target is the next guide in the path.
2. Another guide that builds on this one.
3. The reference page that holds the detail this guide left out.

The rendered link text is the target's `nav_title` (or its `title`), and nothing else, so there is no room for a reason. Choose targets whose titles speak for themselves.

### Length

500 to 900 words of body, code blocks included. Count with:

```bash
awk '/^---$/ && n < 2 {n++; next} n >= 2' docs/site/themes/add-a-section.md | wc -w
```

Under 500 words usually means the guide skips the check or the context the reader needs. Over 900 words usually means reference detail crept in: move it out.

### Overview guides

Some guides introduce a whole area (`themes/overview`, `plugins/overview`, `architecture/overview`, `updates/overview`, `ai-seo/overview`, `admin/tour`). They use the same template. The task is "get a working picture of X": `## Before you start` says what the reader should have done or have at hand, `## Steps` walks them through looking at the real thing (open this screen, run this command, look at this file), one idea per step, and `## Check it worked` tells them what they should now be able to answer or find.

## The reference template

```md
---
title: Template functions
nav_title: Template functions
description: Every function you can call from a theme template, with its arguments, result and behaviour on bad input.
---

One or two sentences: what this page lists, who needs it, and which guide uses it, linked.

## Overview

A short example or a summary table, so a scanning reader sees the shape first.

## <One H2 per item or area>

Tables for items with the same shape (name, type, default, meaning).

## Limits and errors

What is refused, what the limits are, what each error message means.
```

Rules for reference pages:

- The opening says what the page lists and links to the guide that uses it ("To add your first section, follow [Add a section](add-a-section.md).").
- Order items so a reader can find them: by area, in the order a request or a task meets them, or alphabetically. Say which on long pages.
- Each fact appears once. If two reference pages need it, one holds it and the other links.
- Use tables for flags, environment variables, settings, fields and error messages. Columns, in this order where they apply: name, type or values, default, meaning.
- Exact names are in inline code: `API_PORT`, `--role`, `theme.json`, `<admin>/settings/users`.
- A "how do I" passage belongs in a guide. Move it there and link to it.
- `## Overview` and `## Limits and errors` are the usual first and last headings, but use whatever H2s make the page scannable. Do not end with `## Next steps`; a short `## Related` list of links is fine.
- No `next:` key.

## Section index pages and the landing page

A section's `index.md` is one or two short intro paragraphs: who the section is for and what it helps them do. Do not list the pages: the layout shows the Guides and Reference cards under the intro, built from the manifest and each page's `description`. No `next:` key.

`getting-started/index.md` also has a `## Start here` heading with a numbered list, the one reading path for a new site owner, in this order:

1. Before you install (`before-you-install.md`)
2. Install the binary (`install-binary.md`) or Install with Docker (`install-docker.md`)
3. First run (`first-run.md`)
4. Create users (`create-users.md`)
5. A tour of the admin (`../admin/tour.md`)
6. Edit and publish content (`../admin/content.md`)

Each item is a link with a few words on what it gets the reader.

The landing page, `docs/site/index.md`, is an intro for the whole site plus three "I want to..." paths as plain links: a site owner goes to the Start here path in `getting-started/index.md`, a theme developer to `themes/your-first-theme.md`, a server admin to `operate/deploy-systemd.md`. The section cards come from the layout. No `next:` key.

## Front matter

Every page starts with front matter: `key: value` lines between two `---` lines. It is not YAML: one key per line, no quotes needed, no lists, no nesting. Only these keys are allowed:

| Key | Required | Rules |
|---|---|---|
| `title` | yes | The page heading (the h1). Guides: verb phrase. Reference: noun phrase. Sentence case. |
| `nav_title` | no | The label in the sidebar, breadcrumbs, cards, the previous and next links and other pages' Next steps. Defaults to `title`. Under 24 characters, because the sidebar is narrow. |
| `description` | yes | Card text and meta description. One plain sentence, under 160 characters. See below. |
| `next` | guides only | 2 to 3 targets for the Next steps block. Required on every guide. Not allowed on reference pages, section `index.md` files or the landing page. |

The manifest can name a page with a title (`{"slug": "settings-users-email", "title": "Settings reference"}`), but that title is only a fallback: the sidebar uses the page's `nav_title`, then its `title`. So set the sidebar label with `nav_title`, and keep it the same as any title the manifest gives. Only the plan owner edits `manifest.json`.

### The `next` key

The value is 2 to 3 relative paths to `.md` files, separated by commas, on one line. Paths resolve exactly like links in the body: relative to the file you are editing, so a page in the same section is `name.md` and a page in another section is `../<section>/<name>.md`. No `#fragment`, no absolute URLs, no titles. Every target must be a page in the manifest.

```md
---
title: Change colours and fonts
nav_title: Colours and fonts
description: Change your site's colours and fonts by editing one settings file.
next: preview-and-dev.md, packaging.md
---
```

A target in another section:

```md
next: reverse-proxy-tls.md, ../updates/backups-and-rollback.md
```

The tool renders the targets, in the order given, as a `Next steps` heading and a list of links at the end of the guide. A `## Next steps` heading in the body is a lint error, because the tool owns that block.

## Descriptions (card text)

The `description` is what a reader sees on the card in the section index and on `/docs`, and what search engines show. It has to work on its own, next to the title, without the page.

- One sentence, under 160 characters, plain text: no Markdown, no backticks, no links.
- Guides: start with the task, as a verb. "Add a new block type to your theme that editors can place on any page."
- Reference: say what is listed. "Every flag of the update command, with its default and what it changes."
- Use the reader's words, not ours. No jargon from the list below, even in reference descriptions where a plain word exists.
- Do not repeat the title. Add what the title leaves out: the result, the scope or the audience.
- Section `index.md` descriptions follow the same rules.

| Bad | Why | Good |
|---|---|---|
| The data-hm marker contract between a theme and the visual editor, design tokens, layout parts, builder pages and the islands CSP. | a list of internal names | What a theme must keep so the visual editor can show and edit its pages. |
| Download the hatamoto binary, give it a Postgres database and start serving a site on port 8090. | fine, but "binary" is jargon for this reader | Download Hatamoto, connect it to a Postgres database and start your site. |

## Openings: a bad and a good example

The opening decides whether the reader keeps going. It tells them what they will do and what they get, in their words.

Bad (the old `getting-started/install-binary.md`):

> Hatamoto is a single statically linked Go binary. It needs one thing from you: a Postgres database it can connect to. Everything else, including the migrations, the server secret and a starter home page, it creates on its own.

Why it fails: "statically linked", "Go binary", "migrations" and "server secret" are four unexplained terms in three sentences, and the reader still does not know what they will have at the end.

Good:

> In this guide you download Hatamoto, connect it to a Postgres database and start it. When you finish, your site answers on port 8090 and the server prints a setup code, which you use in the next guide to create your owner account.

Bad (the old `themes/overview.md`):

> Hatamoto renders the public site itself. The Go binary reads stored pages, builds a view and renders it through an `html/template` theme. The result is a complete HTML document with a small, stable set of `data-hm-*` markers, so the admin canvas can map every visible piece of text back to the JSON field it came from.

Why it fails: it opens with internals (the Go package, editor markers) that a web developer new to Hatamoto does not need yet, and it never says what the reader can do with a theme.

Good:

> A theme decides how your site looks: the HTML of every page, its CSS and its images. This page shows the files a theme is made of and how Hatamoto picks the template for each page, so you know where to make a change.

## Explaining terms

### The gloss rule

The first time a term from the lists below appears on a page, explain it in plain words in the same sentence, and bold that first use:

> Hatamoto calls a **webhook**, a web address you choose, for each event you pick, such as a page being published.

After that, use the term alone. Rules:

- One gloss per page, at first use. Not in a later paragraph, not in a footnote.
- A link is not a gloss. You may link the term to the reference page that defines it, but the sentence must still explain it.
- Never open a guide with a listed term. Open with what the reader gets.
- Bold only the first use of a glossed term. Do not bold for emphasis elsewhere; use plain words ("Important:" at the start of a sentence) if something needs weight.
- The suggested glosses below are starting points. Check each against the code and the page's context before you use it.

### Avoid in guides

In guides, use the plain alternative. These terms may appear on reference pages.

| Avoid | Use instead |
|---|---|
| statically linked, Go binary | one program file, the `hatamoto` program |
| server-rendered | the server builds each page |
| `html/template`, Go templates (in admin and getting started) | the theme's template files |
| htmx, islands | (leave out; say what the screen does) |
| WebAssembly, wasm, Extism, wazero | outside `plugins`: leave out. In `plugins` guides, gloss "WebAssembly" once in the overview and do not name the runtime libraries |
| `hm_call` | the host API (glossed) |
| fail open | if the plugin fails, the page still loads without it |
| idempotent | running it again changes nothing |
| expand-only, contract marker, n-1, `rollback_floor` | the oldest version you can roll back to (link to the rules page) |
| golden files | (leave out of guides) |
| health gate | a check that the new version starts and answers |
| rollout bucket | (leave out; say "not every site gets an update at the same moment") |
| halted, yanked | withdrawn |
| channel manifest | the list of current releases |
| replica | (operate reference only) |
| CSP, CSRF, HSTS | outside `operate`: leave out or say what it protects against |
| kv | the plugin's key-value store, glossed as "simple storage of named values" |
| SQL guard | (reference only) |
| canvas | the preview of the page in the editor |
| `data-hm-*` markers | (reference only; `themes/builder-contract`) |
| node kind, inspector | the block types, the settings panel |
| entity schema, `settings` object | content type, its settings |

### Explain on first use

These terms are fine in guides once glossed.

| Term | Suggested gloss |
|---|---|
| Postgres, database address (`DATABASE_URL`) | the database Hatamoto stores everything in; the address tells Hatamoto where it is and how to sign in |
| environment variable | a named setting you give a program when it starts, outside its files |
| migration | a step that updates the database structure to match the new version |
| server secret | a long random value the server uses to sign links and protect stored credentials; it must stay the same across restarts |
| reverse proxy | a web server in front of Hatamoto that handles HTTPS and passes requests on |
| TLS | the encryption behind HTTPS |
| systemd | the service manager on most Linux servers, which starts programs at boot and restarts them when they stop |
| Docker volume | storage that survives when the container is replaced |
| compose file | a file that tells Docker which containers to start together |
| setup code | a one-time code the server prints to its log so only someone with access to the server can create the first owner |
| entity | a piece of content: a page, a post, a person or any other item |
| content type | the kind of content, which decides its fields and its address |
| taxonomy | a way to group content, such as categories or tags |
| slug | the short name in a page's address, such as `hello` in `/blog/hello` |
| URL pattern | the rule that builds a page's address from its slug and type |
| role (owner, editor) | what an account may do: an owner can do everything, an editor works on content |
| API key, scope | a secret string a program uses to act on your site; the scope limits what it may do |
| webhook, signature | a web address Hatamoto calls when something happens; the signature proves the call came from your site |
| hook: action, filter | a point where a plugin joins in: an action runs when something happens, a filter changes a value before it is used |
| manifest | the file that describes a plugin or theme: its name, version and what it needs |
| package (`.hmp`), signing | one file that holds a whole theme or plugin; signing proves who made it and that it was not changed |
| trusted key | a public key your server accepts signatures from |
| registry | a place to publish and download packages |
| sandbox, permission | the closed space a plugin runs in, where it can do only what its permissions allow |
| section, schema | a block an editor can place on a page; its schema lists the fields the editor fills in |
| design token | a named value, such as a colour or a font size, that the theme reuses everywhere |
| layout, partial | the template for a whole page; a smaller template reused inside layouts |
| JSON-LD, schema.org | a block of machine-readable data in a page that tells search engines what the page is about, in the shared schema.org vocabulary |
| `robots.txt`, `llms.txt` | files at the root of your site that tell crawlers and AI tools what they may use |
| sitemap | a file that lists your pages for search engines |
| MCP | the Model Context Protocol, a standard way for AI tools to read and change your site |
| crawler | a program that fetches web pages automatically, for a search engine or an AI company |
| 301 redirect, wildcard rule | a permanent forward from an old address to a new one; a wildcard rule forwards a whole group of addresses at once |
| supervisor | the process that starts the server, watches it and switches to a new version after an update |
| update agent | the part of the server that checks for and downloads new releases |
| update channel, pin | the stream of releases you follow; pinning stays on one version |
| rollback | going back to the previous version |
| cron | a schedule that runs a job at fixed times |

## Voice and wording

- Write to the reader as "you". Present tense, active voice: "The server prints a code", not "A code will be printed".
- One idea per sentence. Aim for under 25 words.
- Say what to do, then why, in that order.
- Plain words: "use", not "utilise"; "start", not "initialise"; "about", not "approximately".
- No marketing or judgement words: powerful, seamless, simple, simply, just, easy, obviously, of course, robust.
- No em or en dashes. Use a comma, a colon, parentheses or a new sentence. Write ranges as "1 to 5".
- Sentence case for titles and headings. Headings are short and specific: "Find the setup code", not "Setup".
- Name screens and buttons as they appear, in bold. Write a path through the admin with `>`: **Settings > Redirects**. Give the address too when it helps: `<admin>/settings/redirects`.
- Write the admin address as `<admin>/...` inside a code span, never as `/admin`: every install has its own private address. Never put `<admin>` outside a code span, because Markdown reads it as an HTML tag and it disappears. Add one blockquote callout on the first page use, before the first address, with this text:

  > Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

  The old `/admin` stays only where it is the subject: the "Old admin addresses" redirect tables, the `--admin-url` flag of `backup restore`, checks that pin `HATAMOTO_ADMIN_PATH=/admin` (the n-1 smoke set, QA), source paths such as `internal/admin`, and relative links to `admin/*.md` pages.
- Hosts and emails use `example.com`: `https://example.com`, `you@example.com`. Example paths have no language or region segment: `/blog/hello`, not `/en/blog/hello`.
- No internal slice, phase or ticket labels, and none of the words the repository's neutral check bans (`scripts/check-neutral.sh`). The lint catches both.
- Do not promise what the code does not do, and do not describe plans or future features.
- British spelling, as in the rest of the docs: colour, behaviour, licence (noun).

## Commands, code and examples

- Every guide has one complete example the reader can copy and run as written. Prefer one worked example over a list of options.
- Fenced code blocks always have a language: `bash` for commands, `text` for output, `json`, `yaml`, `html`, `css`, `go` as fits.
- No prompt characters (`$`, `#`) in front of commands, so they paste cleanly. Comments in a `bash` block start with `#` on their own line.
- Show the expected output in a separate `text` block right after the command.
- Placeholders look `<like-this>` and are explained once, in the sentence before the block. Never abbreviate a command with `...`.
- Realistic names: `my-theme`, `hello-plugin`, `editor@example.com`.
- Say where a command runs (on the server, inside the container, on your laptop) and as which user, when it matters.
- A command that needs `DATABASE_URL` or another variable says so before the block.

## Links

- Link to other pages with relative `.md` paths. Same section: `[First run](first-run.md)`. Other section: `[Hardening](../operate/hardening.md)`. The tool rewrites them to site addresses.
- Link to other sections by page, never by `#anchor`. Headings move between pages during rewrites, so cross-page anchors break. Use an anchor only for a heading on the same page: `[Check it worked](#check-it-worked)`.
- The link text says where it goes: the page title or a short description. Never "here" or "this page".
- Link a glossed term to its reference page at its first use, and do not link the same page again in the same section of text.
- External links are absolute `https` addresses.
- A reference page links to the guide that uses it in its opening; a guide links to the reference page that holds its details, in the steps where the detail matters or in `next:`.

## Markdown the publisher accepts

The tool rejects anything else with a line number (`README.md` has the full rules):

- The body starts at `##`. No `#` heading in the body; the title is the h1. Headings `##` to `####` only.
- Paragraphs, bold, italic, inline code, links, images with an `https` or relative source.
- Bullet and numbered lists, nested by two-space indentation.
- Fenced code blocks. No indented code blocks.
- Pipe tables, blockquotes, horizontal rules.
- No raw HTML of any kind, no footnotes, no definition lists.
- Heading ids are added when the page is rendered, from the heading text. The ids `toc`, `top` and `main-content` are taken by the layout, so do not use a heading whose text would become one of them.

Run the lint before you hand in:

```bash
make docs-publish ARGS=--lint
```

## Facts and verification

- Check every command, flag, environment variable, path, default, limit, error message and screen name against the code before you write it down. The old page is a list of what to cover, not a source of truth.
- When the old page and the code disagree, the code wins. Write what the code does, and mention the fix in your note.
- Run every new guide end to end on a real or local install. A guide that was not run is not done.
- Do not invent features, limits or messages. If you cannot find a fact in the code, leave it out and report it.
- In your final note (not on the page), list every fact you checked as a table:

```text
fact | file:symbol
default port is 8090 | backend/internal/config/config.go:Load (API_PORT)
user create defaults to role owner | backend/app/user.go (flag "role")
```

## Moving detail between pages

Nothing is dropped. When detail leaves a guide, it goes to a reference page, and the guide links to that page. When you move a heading, fix every link to it in your own section, and check the whole tree with the lint.

Some detail moves between sections. The receiving writer owns the destination and makes sure every fact exists there; the source writer only replaces the moved text with a link. Read the old text with `git show origin/main:docs/site/<path>`.

| Detail | From (source) | To (receiver) |
|---|---|---|
| "Serve versus supervise", "Other commands" | `getting-started/install-binary` | `operate/cli-reference` |
| create-users "Flags", "Examples" | `getting-started/create-users` | `operate/cli-reference` |
| install-docker "What the image does", "Volumes" | `getting-started/install-docker` | `operate/deploy-docker` |
| admin redirect "Resolution order", "Wildcards", "Redirects Hatamoto writes", "Old admin addresses" | `admin/media-menus-redirects` | `migrate-contribute/redirect-rules` |
| redirects-after-migration rules | `migrate-contribute/redirects-after-migration` | `migrate-contribute/redirect-rules` |
| first-run "What happens on first boot" detail | `getting-started/first-run` | `getting-started/requirements-and-secrets` |

Only edit files in your own section. If you need a change in another section or in `manifest.json`, report it.

## A worked example guide

A short guide that follows every rule above. Real guides are longer (500 to 900 words); this one shows the shape.

````md
---
title: Add an editor from the command line
nav_title: Add an editor
description: Give a colleague an editor account from the server's terminal, without email.
next: ../admin/tour.md, ../admin/content.md
---

In this guide you create an account for a colleague who will write and publish content, using one command on the server. At the end they can sign in to the admin with a temporary password.

## Before you start

- A shell on the server that runs Hatamoto, as the same user that runs the service.
- The **database address**, the `DATABASE_URL` setting that tells Hatamoto where its Postgres database is. The command talks to the database directly, so the server does not need to be running.
- Your colleague's email address.

## Steps

1. Load the service's settings into your shell, so `DATABASE_URL` is set.
2. Create the account with the `editor` **role**, the role that can work on content but not change settings or users:

   ```bash
   hatamoto user create --email sam@example.com --name "Sam Editor" --role editor
   ```

   The command prints the account and a temporary password, once:

   ```text
   created editor user sam@example.com
   temporary password: <24 random characters>
   ```

3. Send the password to Sam through a private channel. It is not stored anywhere you can read it again.

## Check it worked

Sam opens `<admin>/login`, signs in with the email and the temporary password, and changes the password from the admin.

If the command says the email already exists, the account is already there: reset its password with `hatamoto user reset-password --email sam@example.com` instead.
````

What makes it work: the title is the task, the opening says what the reader gets, two terms are glossed at first use, every step is one action, the output is shown, the check is something Sam can see, the most common failure has a fix, and the Next steps live in `next:`.

## Before you hand in

- [ ] The page type matches its manifest list (`guides` or `reference`).
- [ ] The opening says what the reader does and gets, with no internals.
- [ ] Guide: `## Before you start`, `## Steps`, `## Check it worked`, in that order, and no other H2.
- [ ] Guide: 500 to 900 words, and `next:` with 2 to 3 targets that exist. No `## Next steps` heading.
- [ ] Reference: no `next:`; the opening links to the guide that uses it.
- [ ] Every term from the jargon lists is glossed at first use or replaced.
- [ ] `description` is one plain sentence under 160 characters; `nav_title` is under 24 characters.
- [ ] Links to other sections point at pages, not anchors.
- [ ] Every fact is checked against the code, and your note has the `fact | file:symbol` table.
- [ ] Nothing from the old page was lost: it is on this page or on the page your note names.
- [ ] `make docs-publish ARGS=--lint` passes.
