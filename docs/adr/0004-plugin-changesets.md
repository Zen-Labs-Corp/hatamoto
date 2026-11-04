# ADR 0004: Transactional changesets and phase-isolated plugin runs

Status: proposed (2026-11-02)

Amends: [ADR 0002](0002-single-binary.md) (the WordPress importer row of the
feature table: the importer leaves core and becomes a first-party plugin built
on this design).

Breaks: the plugin contract in [`docs/PLUGIN_API.md`](../PLUGIN_API.md). This
ships as core 1.1.0 and SDK 0.17.

## The rules in plain words

1. A plugin never changes the live site. It prepares a changeset; the owner
   previews the site with it, then publishes or discards it. A published
   changeset can be undone as a whole.
2. A changeset nobody publishes or discards within 72 hours is deleted, with its
   staged files.
3. Risky changes (links to new sites, redirects, hidden characters, deletions,
   slug changes) are listed one by one and approved one by one. The only
   exception: redirects may be approved as a group when every target is a page
   in the same changeset.
4. Site-wide things (site settings, URL patterns, home page, menu locations)
   can only be proposed. Each proposal is approved on its own, after the owner
   signs in again.
5. A plugin can change only what it created. A human edit to the body, slug,
   parent, template or SEO fields makes the object the owner's.
6. The part of a run that reaches the internet cannot see the site, and the
   part that sees the site cannot reach the internet.
7. There is no way to write to the live site directly: no legacy function, no
   compatibility mode, no switch.
8. Settings that weaken these rules exist only on the server, never in the
   admin.
9. Every plugin gets the same limits, first-party plugins included.
10. Background updates either keep data in the plugin's own storage and show it
    through template functions, or use a standing approval limited to numbers,
    yes/no, dates and fixed choices on the plugin's own objects. Any plugin
    update voids the approval.
11. Tools that change the owner's own content are core features and use the
    same preview, diff and undo.
12. Hatamoto stays non-headless in this release; changesets, risk checks and
    plugin output stay structured data so a content API can come later.

## Context

Plugins can write to the live site today, and they can do it from anywhere: hooks
(`entity.before_save`, `entity.created`, ...), public and admin routes, and
cron jobs. They do it with `entities.create`, `entities.update`,
`entities.set_status`, `entities.delete`, `terms.save`, `terms.delete`,
`menus.save`, `menus.add_item`, `menus.delete_item`, `media.upload`,
`redirects.create` and `redirects.delete`. The owner grants the permission once,
and from then on every write goes straight to production with no human in the
loop. That is ambient authority, and it is the model that made mass compromise
routine for other CMSs:

- **The owner is the security boundary, and most owners cannot be.** A
  permission prompt asks a non-technical owner to judge risk they cannot judge.
  They click Allow, and they update everything at once.
- **Supply chain.** A compromised plugin version, a stolen signing key or a
  poisoned registry entry inherits every write the previous version was granted.
- **Bulk imports of untrusted content.** Moving the WordPress importer out of
  core means a plugin must be able to create thousands of entities, media files,
  menus and redirects from a source the owner does not control (a remote site or
  a WXR file of up to 512 MB). With direct writes that plugin is the most
  dangerous one on the site.
- **Exfiltration.** A plugin that can read private content (drafts, form
  submissions) and reach the network can send that content anywhere it can
  reach, including the "source site" an owner was talked into importing from.

Core already sandboxes plugin code (Extism on wazero, no filesystem, guarded
network, its own database role). This ADR does the same for plugin *effects*.

## Decision drivers

1. **Owner authority.** No plugin code changes live site content without a
   person having seen the change, except inside a narrow, typed policy the owner
   approved in core's own UI.
2. **Network and state separation.** Code that can reach the network cannot read
   site state, and code that can read site state cannot reach the network,
   within one run. The one carve-out (SDK 0.18): the fetch phase may read what
   an anonymous visitor can already read (section 2).
3. **Questions owners can answer.** Replace "grant `entities.write`?" with
   "does this preview of your site look right?".
4. **Deterministic sandboxing.** Every run step has a time limit, a memory
   ceiling and a storage quota, enforced by the host.
5. **One path.** No legacy route around the new model, not even behind a flag.

## Decision

### 1. Content writes happen only inside runs, and every run ends in a changeset

A **run** is started by the owner in core's admin UI (or, for background
updates, by a standing approval: section 6). A plugin advances a run through
the export `on_run_step`; the host calls it repeatedly with
`{ "run_id", "phase", "cursor" }`, and each call is a normal hook call with its
own deadline (10 s by default). Long work is many short steps, never a long call.

Everything a run writes lands in the run's **changeset**: staged entity
upserts and deletes, terms, bylines, plugin-owned menus, redirects, staged
media, and proposals for singletons. Nothing in a changeset is visible on the
public site. The owner previews the site with the changeset applied, then
**publishes** it (one database transaction) or **discards** it. A published
changeset can be **reverted** as a whole.

A plugin declares its runs in a `runs` block of `plugin.json`: for each run its
`name`, `title`, `inputs`, `stages` and `proposes` (the object kinds the run may
propose). The install scan report and the registry review read this block. A run
that proposes a kind it did not declare is refused.

Hooks, routes and cron cannot write content. They keep the plugin's own data
(`kv`, `db`), logging, mail and the read functions.

### 2. Two phases per run, sharing no state

| | Fetch phase | Transform phase |
|---|---|---|
| Network | `http.fetch` to the run's hosts only (the source origin and the media hosts the owner confirmed on the scan report; this replaces the manifest's `allowed_hosts` for the run) | none |
| Reads | `upload.read` (the file the owner uploaded through core's UI), `run.info` | `buffer.get`, `buffer.list`, `site.info`, published entity reads, `entities.slug_taken` |
| Writes | `buffer.put` (write-only staging buffer, quota per run), `media.import_url` (staged media) | `changeset.*` |

| Plugin `kv`/`db` | not available | not available |

SDK 0.18 lets the fetch phase read what an anonymous visitor can read, and nothing more: entities and translations that pass the public visibility rule (published, not a variant, in a locale the site serves, not a hidden person or a layout block) and the media files a public page uses, at most 200 reads per step. Drafts, scheduled content, terms, menus, redirects, forms and unused library files stay closed to it. Data an anonymous visitor can already fetch is not a secret a fetch-phase `http.fetch` can leak.

SDK 0.18 adds `changeset.media.alt` to the transform writes: a run that declares `proposes: ["media_alt"]` stages an alt text change on media the owner uploaded. It is an ordinary reviewed item, not a singleton proposal, and it changes no other field. Every alt text change is high risk: the review lists each one as old text and new text and the owner decides on it. Publish applies it and revert restores the old text.

The phases share nothing but the run's buffer, which flows one way: fetch writes
it, transform reads it. The plugin's own `kv` and `db` are unavailable in both
phases, so the transform phase cannot leave data for the next run's fetch phase to
send out. A re-sync re-fetches the source; any cursor the fetch phase needs
(such as "modified after") is computed by core from the previous published run,
never written by the plugin.

### 3. Ownership

Every entity, term, byline, menu, redirect and media item carries an **origin**:
`owner` or `plugin:<name>`. A changeset may update or delete only objects whose
origin is the plugin's own. Objects are matched by the plugin's stable
`source_key` (for example a WordPress post id), and core keeps that mapping, so
re-runs update and never duplicate. A plugin that tries to change an object
whose ownership has passed to the owner gets the host error `not_owned`.

- **A human edit hands the object to the owner.** Changes to the body, slug,
  parent, template or SEO fields set the origin to `owner`; the plugin can no
  longer touch it. Metadata edits do not transfer ownership: tags, categories and
  bulk edits of metadata (for example tagging 500 imported posts) leave the
  origin unchanged.
- **The owner can hand it back.** "Let the plugin track this again" sets the
  origin back to `plugin:<name>` for chosen objects.
- **Singletons cannot be owned by plugins**: the site settings, URL patterns, the
  home page, menu locations, the 404 page, robots and templates. A plugin can only
  *propose* a change to them (`changeset.propose`), and each proposal is signed
  off on its own.
- **Core and human paths always win.** A staged slug or redirect source that
  matches a live entity path, a core route or a reserved prefix is refused when it
  is staged. A clash with a draft or private slug is not reported to the plugin
  (`entities.slug_taken` sees published, public entities only, so it cannot be
  used to probe confidential titles); core settles it at publish with a suffix.

### 4. Merge

Each staged update records the **base version** of the object it was computed
from. At publish, an object whose live version moved since then (someone edited
it while the changeset waited) becomes a **conflict**: it is listed and skipped,
never overwritten. The rest of the changeset publishes.

### 5. Preview and risk-weighted diff

- **Isolated preview origin.** The preview renders on a separate origin that
  carries no admin cookies and has no storage shared with the admin, inside a
  sandboxed frame with a strict CSP, reusing the existing editor preview
  rules (no plugin, island or theme script; one-time frame tickets bound to the
  session). Content a plugin stages has already passed the entity service's HTML
  sanitiser, so the preview is a second wall, not the first.
- **Risk-weighted diff.** Changes are classed by risk, not counted:

  | Class | Examples | Approval |
  |---|---|---|
  | Low | plain text in a body, a title, an image alt | grouped, approved with the changeset |
  | High | a new external host in a link, a changed link target or query parameter (affiliate ids), an embed or iframe, zero-width or bidirectional control characters, a redirect, a slug change, a delete | itemised, each ticked (redirects may be approved as a group only when every target is a page in the same changeset) |
  | Critical | any singleton proposal (site settings, URL patterns, home page, menu location) | itemised, step-up verification |

  A changeset that contains any high or critical item cannot be approved with a
  single "Publish all". Each high or critical item is approved on its own.

### 6. Background updates

Two layers, in this order of preference:

1. **Data, not content.** A plugin keeps changing values (exchange rates, stock
   levels) in its own `db`/`kv`, updated by cron, and pages show them through
   template functions (`{{ plugin_<name>_<func> }}`) and page-builder data
   sources (`builder.data`). Core escapes the output as plain text, so it cannot
   carry markup, links or scripts. No content is written, so no review is needed.
2. **Standing approvals**, when the value must live in content (a product's
   price field). The manifest declares a policy; core shows it in plain words
   ("May update the Price and In stock fields of Products this plugin created,
   up to 200 changes an hour"), and the owner approves it once in core's UI with
   step-up verification. A cron-started run then publishes its changeset
   automatically **only if every item is inside the policy**:
   - typed scalar fields only: number, boolean, date or enum. HTML, links, slugs
     and free text never publish automatically;
   - objects the plugin owns, within the rate limit;
   - no item classed high or critical.

   Anything outside the policy waits for review like any other changeset.
   Automatic publishes are logged, revertible as a run, and listed in a daily
   digest.

### 7. Invariants

These are binding; tests pin each one.

1. **No direct writes.** No host function writes live content outside a
   published changeset. There is no compatibility shim and no environment flag
   that restores direct writes.
2. **Standing approval invalidation.** Any plugin version change, any change to
   its manifest, and any change to a schema a policy names immediately voids every
   standing approval of that plugin. Re-approval needs an authenticated owner
   session with step-up verification.
3. **Changeset and blob TTL.** A changeset not published or discarded within
   **72 hours** expires. Expiry, discard and run failure delete the changeset,
   the run's buffer and every staged media record and blob tagged with its
   `run_id` that was not attached by a published changeset. A sweeper removes
   anything a crash left behind.
4. **Phase isolation.** No host function is available in both phases if it reads
   site state in one and reaches the network in the other; no plugin `kv`/`db` in
   either phase. The exception is the public content reads (`entities.get`,
   `entities.list`, `entities.translations`, `media.get`, `media.list`): in the
   fetch phase the host narrows them to what an anonymous visitor sees, using
   the same predicates as public rendering.
5. **Ownership.** A changeset never updates or deletes an object of another
   origin, and never assigns a singleton. The two exceptions are an alt text change on
   media the owner uploaded, and `translation_of`, which records the translation
   group on the source entity (a one-member group counts as no group, and revert
   restores only the staged entity).
6. **Operator-only switches.** Settings that weaken these rules (trusted signing
   keys, raised limits) exist only in the server's environment or config, never
   in the web admin. Core refuses them when the config file is writable by the
   service user.

### 8. Runtime specification (host-level DoS protection)

These exist today and stay binding for run steps:

- **Memory.** `limits.memory_pages` caps each linear memory: default 256 pages
  (16 MiB), range 64 to 1024. The guest module and Extism's kernel module each
  get the cap, so one instance uses at most about twice `memory_pages`.
  `TestMemoryCeilingIsTwiceMaxPages` and `TestConcurrentMemoryBombsBounded` pin
  it. Run steps do not raise it.
- **Time.** Every call runs under a context deadline and Extism's
  `CloseOnContextDone`, so a guest in a loop is stopped at its deadline (wazero
  has no fuel metering; the deadline is the mechanism). Waiting for an instance
  slot and starting a cold instance are bounded separately; a failing hook trips
  the circuit breaker.
- **Concurrency.** `limits.max_instances` (default 4, at most 16) bounds live
  instances per plugin; at most one run per plugin is active at a time.
- **Network.** `http.fetch` and `media.import_url` share one client. It resolves
  the host itself, checks every answer against the private, loopback,
  link-local, CGNAT and reserved ranges, and dials the checked IP, never the name
  again, re-checking every redirect hop. DNS cannot rebind between check and
  connect.
- **Input.** Host function arguments are decoded strictly (an unknown field is
  `invalid_argument`), capped at 1 MiB per request and 2 MiB per result, with a
  nesting depth limit (core 1.1.0 adds it to any decoder that lacks one). No host
  function takes a filesystem path.

### 9. Host functions

**Removed in SDK 0.17:** `entities.create`, `entities.update`,
`entities.set_status`, `entities.delete`, `terms.save`, `terms.delete`,
`menus.save`, `menus.add_item`, `menus.delete_item`, `media.upload`,
`redirects.create`, `redirects.delete`, and the permissions `entities.write`,
`terms.write`, `menus.write`, `media.write`, `redirects.write`.

**Unchanged:** `permissions`, `log`, `settings.get`, `kv.*`, `db.*`, the read
functions of entities, terms, menus, media, redirects and forms, `mail.send`,
and `http.fetch` against the manifest's `allowed_hosts` outside runs.

**New:**

| Function | Phase | Notes |
|---|---|---|
| `run.info` | both | run id, phase, the parameters the owner entered |
| `run.checkpoint {cursor}` | both | the host passes it back on the next step |
| `run.progress {done, total, message}` | both | shown in core's run view |
| `run.finish {summary}`, `run.fail {reason}` | both | end the phase or the run |
| `upload.read {upload_id, offset, len}` | fetch | chunked read of an owner upload |
| `buffer.put {key, data}` | fetch | write-only, quota per run |
| `media.import_url {url}` | fetch | the host downloads into staged media; returns a handle |
| `entities.get`, `entities.list`, `entities.translations` | both | in fetch only what an anonymous visitor sees; at most 200 content reads per step |
| `media.get`, `media.list` | both | in fetch only files a public page uses |
| `buffer.get`, `buffer.list` | transform | read-only |
| `site.info` | transform | locales, URL patterns, title, time zone; read-only, no secrets |
| `entities.slug_taken {schema, slug, locale}` | transform | published, public entities only |
| `changeset.entity.upsert {source_key, schema, slug, parent_key?, locale, name, attributes, seo}` | transform | idempotent on `source_key` |
| `changeset.term.upsert`, `changeset.person.upsert` | transform | bylines only, never user accounts |
| `changeset.media.attach {handle, alt}` | transform | attaches staged media |
| `changeset.menu.upsert` | transform | the plugin's own menus, never a location |
| `changeset.redirect.upsert` | transform | same-site target; the source may not be a live or core path |
| `changeset.delete {source_key}` | transform | the plugin's own objects only |
| `changeset.propose {kind, ...}` | transform | `site_settings` (allow-list: title, tagline, time zone, language), `url_patterns`, `home_page`, `menu_location` |

Never exposed: user accounts and authentication, mail configuration, the public
URL, templates, head tags and scripts, route precedence, core tables, and
content writes outside a run.

### 10. Breaking change and migration

- SDK 0.17 removes the write functions and permissions above and adds the run,
  buffer and changeset functions. Core 1.1.0 refuses to load a plugin whose
  manifest requests a removed permission, with a message naming the replacement.
- No first-party plugin uses the removed functions today; the WordPress importer
  is the first plugin written for the new model.
- Standing approvals and changesets need new core tables; that migration is part
  of core 1.1.0.

### 11. Data-level boundaries (headless-ready, not headless)

Hatamoto stays a single binary that renders its own public site
([ADR 0002](0002-single-binary.md), ROADMAP decision 2: no headless mode). This
ADR adds no public content API. It does keep every boundary at the level of
structured data, so a content API can be added later (a future ADR) without
changing how plugins work:

- **Changesets hold structured data**: entity fields, attributes, terms,
  redirects and proposals as typed values, never rendered pages.
- **Risk checks run on that data**, not on rendered HTML: link hosts and query
  parameters, embeds, control characters, slugs and singletons are found in the
  stored fields. The same checks would apply to content served through an API.
- **Preview is defined as "render the changeset's data"**. In this release core's
  renderer is the only implementation. The changeset, its diff and its approval
  state are not tied to it.
- **Plugin output to pages is typed data.** Every surface this ADR adds returns
  typed values only. Existing surfaces already do: template functions return
  plain text that core escapes, `jsonld.enrich` returns objects, `render.page`
  returns section blocks validated against their schemas, `builder.data`
  returns data. The one exception is template packs (`partials/`, `sections/`):
  they stay trusted template markup, checked as today, and they render only
  through core's renderer.

### 12. Content-improving tools are core features in this release

A plugin cannot propose edits to objects it does not own. Tools that change
the owner's own content (search and replace, bulk fixes, broken-link repair)
are core features. They build a changeset with the owner as origin and go
through the same preview, risk-weighted diff and publish as plugin runs.

**Delegated runs are deferred.** In such a run the owner picks objects and a
plugin proposes edits to them. Most of the tools that would want it (rewriting
text, generating alt text, checking links) need to read the owner's content
and reach the network in the same task. That combination is the exfiltration
path section 2 closes. Allowing it needs its own consent model (which objects,
sent to which host, for one run), and that is left to a later ADR. It fits
this design as a new run scope with no change to the host functions above.

Alt text runs under SDK 0.18 are not that combination: the fetch phase sees only
public content and public media (nothing the visitor could not already read),
the transform phase cannot reach the network, and every alt text change is a
reviewed item the owner decides on. They need no consent model of their own.

## Out of scope: separate threat surface

**REST API keys and the MCP server** write to the site directly, by design: they
are how external tools publish content. They do not pass the changeset preview
gate, and they do not share the Wasm guest sandbox model. They authenticate with
service tokens that the owner creates and revokes, are scoped by role, and are
audited, and they cannot reach plugin routes. Their threats (token theft, an
over-scoped key, a compromised external tool) are real but different, and
they need their own design, not this one.

**A public content API and headless front ends** are out of scope (section
11). A future ADR decides them; this one only keeps the boundaries at the data
level so that decision does not reopen plugin design.

**Supply chain hardening** (separate signing keys for packages and for the
registry index, an append-only public release log, installs and updates that
fail closed when inclusion cannot be proven, permission sets in the signed index
bound to each package digest, reproducible plugin builds) is decided in a
separate ADR. Today's package signature already covers a digest of every file,
`plugin.json` and `plugin.wasm` included, so the manifest cannot be changed
under a signed binary.

## Consequences

**Positive**

- A plugin, compromised or not, cannot change the live site without an owner
  having seen the change, or without the change fitting a typed policy the owner
  approved.
- Importing untrusted content cannot leak private site data: the code that
  talks to the source never sees the site.
- Owners decide on previews, which they can judge, instead of permissions, which
  they cannot.
- Every plugin change is attributable to a run and revertible as a whole.

**Negative**

- A large core change: origins on every content table, changesets, merge,
  the preview origin, the diff engine, standing approvals, and SDK 0.17.
- A minor release of core and the SDK, a one-time owner exception. Third-party plugins that write content
  must be rewritten.
- Imports take effect only after an owner reviews them, and plugins can no longer
  change content in the background except through Layer 1 or a standing
  approval.

## Implementation order

1. This ADR.
2. Core 1.1.0: run engine and changesets, origins and merge, the preview origin
   and risk-weighted diff, standing approvals, removal of direct writes.
3. Supply chain ADR and its implementation.
4. The first-party WordPress import plugin on the new API.
5. WordPress removed from core: the importer, its admin wizard and CLI, the
   import tables, the `import.completed` event, legacy WordPress redirect
   patterns and `wp-compat.css`.
