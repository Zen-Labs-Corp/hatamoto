# ADR 0005: Comments in core

Status: accepted (2026-11-20), amended (2026-11)

Reverses: the [`docs/ROADMAP.md`](../ROADMAP.md) section 8 line "Comments,
e-commerce, ..." (comments only; the rest of that line stays out of scope).
Section 8 "visitor profiles" stays out of scope too: the commenter record
described below is not one (Decision 4).

## The rules in plain words

1. Comments are off on every install, new or upgraded. Only an owner turns them
   on, after two warnings that comments invite spam.
2. A commenter is a confirmed email address. No accounts, passwords, social
   login, profile pages or website field. A content type with the community
   policy on is the one opt-in exception: there a commenter can sign in without
   writing a comment and keeps one name a plugin assigns (Decision 9).
3. Every comment with a link, and every comment from a new commenter, waits for
   a moderator. Owners and editors moderate everything. The community policy,
   which only an owner turns on for a content type after a warning, is the one
   opt-in exception: it skips the link hold and the new-commenter hold; every
   other hold stays (Decision 9).
4. No raw IP address is stored. Core keeps keyed hashes of the address and its
   network only.
5. Core's spam protection runs on the site. Core sends no comment data, hash or
   signature to any outside service and uses no shared, federated or hosted
   spam list. A `comment.check` plugin the owner installs may call one; core
   never hands it the raw IP or the email.
6. No JavaScript is needed to read or post a comment.
7. Spam and blocked submissions look posted to their sender and are shown to
   nobody else.

## Context

[`docs/ROADMAP.md`](../ROADMAP.md) section 8 put comments out of scope. Site
owners still ask for them, and a plugin cannot build them well (Decision 2).
Comments are also the most spam-prone feature a site can have, so spam
protection is the main design problem here, not an add-on.

This ADR records the decision before any comment code lands. Nothing in this
ADR is available in a release yet.

## Decision drivers

- Off by default. A site that never asks for comments pays nothing and exposes
  nothing.
- Spam gets no visible payoff and no signal to adapt to.
- Least personal data. Keep only what moderation needs.
- No JavaScript on the public side.
- Reuse the core subsystems that already exist. Do not fork them.

## Decision

### 1. Off by default, owner-only switch, warned twice

A migration leaves comments disabled on every existing site, and new sites start
disabled. Only an owner can turn the site switch on. Editors see it read-only.
Settings > Comments shows an always-visible warning while comments are off.
Flipping the switch opens a second, separate confirmation that the owner must
tick before the confirm button works. Every change of the site switch writes an
audit log entry. Per-schema and per-entity controls (open, closed, hidden) only
appear once the site switch is on. While the site switch is off, nothing
renders and the submit route answers 404.

### 2. Why core and not a plugin

A plugin cannot meet the needs below without changes that would make the
plugin API a second core.

- **Render cache.** A `render.page` plugin keeps pages out of the page cache
  unless it declares `page_paths` ([`PLUGIN_API.md`](../PLUGIN_API.md)). Comments
  would make every page with a comment list uncached. Core keeps pages cached
  and invalidates only the entity that changed.
- **Client IP and the spam pipeline.** Plugins never see the client address.
  Core has `internal/spam` (honeypot, timing token, captcha, rules) and the
  rate limiter in `internal/middleware`. Spam checks need both.
- **DB pool.** A plugin gets 2 database connections. A comment list and the
  submit path need the main pool.
- **Email.** Confirmation and notification mail uses the core mail settings,
  `internal/email` and the email log.
- **Admin design system.** Moderation is an admin screen built from the
  existing components, not a second admin.
- **Backup and export.** Comments belong in the backup and in the export, with
  the rest of the site.
- **Webhooks.** Comment events use the same signed delivery as other events.
- **MCP.** The MCP server can read and moderate comments through API-key
  scopes, like the other MCP tools.
- **WordPress import.** The importer can bring existing comments across.

### 3. Identity: a confirmed email and a device cookie

A visitor writes a name, an email and a comment. The comment is stored as a
draft that only the writing device can see. If the device carries a valid
cookie for that email, the draft moves straight to the trust rules. Otherwise
core sends one confirmation email with the site name, the page title and a
single-use link that expires after 24 hours. The email contains no text the
commenter wrote, not even the name, so the form cannot be used to send spam to
strangers.

Clicking the link confirms the draft and sets a signed, `HttpOnly`, `Secure`,
`SameSite=Lax` cookie. Trust lives on the server, keyed by the email hash. The
cookie only proves that this device already confirmed this email. If the
cookie is lost or the visitor changes device, the next comment sends a fresh
link, and one click restores the old trust level. This also blocks
impersonation: typing someone else's email sends a link to that real inbox and
nothing else. Blocking a commenter, or signing out all their devices, revokes
every cookie at once.

Confirmation emails are limited per email address and per network hash, so the
form cannot flood an inbox. Over the limit the visitor sees the normal "check
your email" page and nothing is sent. Unconfirmed drafts are purged after 24
hours. Disposable email domains are rejected with a neutral message.

### 4. The commenter record is not a visitor profile

ROADMAP section 8 keeps visitor profiles out of scope, and this ADR does not
change that. The `commenters` table is a moderation record. It holds a hash of
the email, the email encrypted for notifications and erasure, the trust level,
approval counts, a few dates and a token version. It has no profile page, no
tracking, no browsing history and nothing beyond what moderation needs. The
owner can export or erase everything held for an email address. There is no
website or author URL field anywhere.

### 5. Trust levels and moderation

| Level | Reached when | Effect |
|---|---|---|
| `new` | first comment | every comment is held |
| `known` | 3 approved comments, the first at least 7 days ago, no spam marks | link-free comments are approved automatically |
| `trusted` | 15 approved comments, the first at least 30 days ago, no recent spam mark | as `known`, plus no slow mode; still held during a surge |
| `blocked` | one spam mark or a manual block | shadow hold (Decision 7) |

The four numbers are editable. Links are held at every level, except in
replies written by an owner or editor as the site. A moderator can vouch for a
commenter or block one in a single click, and a spam mark drops any level to
`blocked`. Owners and editors moderate everything. The Comments screen shows
why each comment was held, so a moderator never has to guess.

### 6. Privacy: hashes, not addresses

Core never stores a raw IP address. It stores `ip_hash` and `net_hash`, which
are keyed HMACs of the address and of its network (/24 for IPv4, /64 for IPv6)
made with the server secret. They are enough for rate limits and blocklists.
They are pseudonymous personal data, not anonymous. Without the server secret
they cannot be linked to an address, but anyone who has the secret can recover
IPv4 addresses by brute force, and backups include the secret. Treat the hashes
and backups as personal data. Each comment also stores a truncated user agent,
for moderation.

Retention is short and automatic. Unconfirmed drafts are purged after 24
hours. Spam and trash are purged after a number of days, 30 by default.
Used form tokens are purged when they expire. A visitor's comments are
deleted or anonymised with the rest of their record when the owner erases an
email address.

### 7. Spam protection stays local

Checks run cheapest first, in core, with no outside service:

1. Site and entity gates, including site access modes.
2. A surge breaker: above a site-wide rate, new comments are held whatever the
   trust level, and the owner is told once.
3. Rate limits per IP hash, network hash, email hash and page, plus a slow
   mode per commenter per page.
4. Single-use form tokens with a minimum dwell time, bound to the page.
5. Form field names that rotate on every page view, so a replayed form fails.
6. A honeypot field, CSRF and `Sec-Fetch-Site` checks.
7. Optional captcha, size and format limits, content rules and blocklists, and
   a duplicate-body check.
8. A link check that holds any comment with a link.
9. A score that lands in one of three buckets: allow, hold or spam.

Bots that fail get the same success page a human gets. Spam and blocked
submissions are visible only to their sender, through their own device cookie,
and are never marked as held. Bots get no signal to adapt to.

A `comment.check` plugin hook can add to the score. It receives the body, link
domains and hashes, never the raw IP or the email, and it has a timeout. On a
timeout or an error the comment is held. A plugin the owner installs may call an
outside spam service within its own network grants, and the owner is then
responsible for telling visitors. Core itself has no shared, federated or
hosted spam list and no data-sharing contract with a spam service. Local rules
and the hook are the whole core design.

### 8. Threads, format and rendering

Comments are threaded to a depth of 5 by default (a setting; 1 means flat). The
public page shows the form first and the thread below it, newest first by
default, with paging. Comments are plain text with a small safe subset:
paragraphs, italic, bold, code, quotes and autolinked URLs. There is no raw
HTML. Text is sanitised on write and escaped on output. Approved links render
with `rel="ugc nofollow noopener"`. The commenter can edit or delete for 15
minutes through the device cookie, and an edit that adds a link sends the
comment back to moderation. Themes can override the comment list, item and form
partials. The form is plain HTML with a POST and a redirect, so posting needs no
JavaScript. htmx is only an optional enhancement where a theme already loads
it.

### 9. Plugins and community content types (amended 2026-11)

Votes, karma, scores and profiles stay out of core: a plugin keeps them in its
own database, keyed to commenter keys and comment ids. Core adds the following,
each with the invariant it keeps.

**9.1 Commenter key.** A plugin that may read comments sees a pseudonymous key
for each commenter: an HMAC of the commenter id, the plugin name and the server
secret. It is never the email and never a stored hash, and it is never sent to
webhooks. Each plugin has its own key space, so two plugins cannot link a
commenter.

**9.2 Plugin moderation.** A plugin the owner granted `comments.moderate` can
mark a comment dead, restore it, set its rank and set a viewer's show-dead
flag, only for comments on the plugin's own `entity_schema`. Every call is
audited and rate limited. There is no API that creates a comment: comments are
posted only through core's form.

**9.3 Dead state.** Dead is a sixth comment status. A dead comment is kept
forever and never purged automatically. The public thread hides it unless the
viewer's show-dead flag is set or the viewer is its author. Replies of a dead
comment stay visible.

**9.4 Rank.** Each comment has a stored `rank` that the plugin writes in
batches. When a schema's comment order is `rank`, core orders each sibling
level by it, in Go and bounded by the page.

**9.5 Community policy.** Only an owner turns it on, per content type, after a
warning. It skips the link hold and the new-commenter hold, allows a depth of
up to 32 levels, and sets the edit window and slow mode per schema. Every other
hold stays: spam verdict, duplicate, blocked commenter, captcha and surge. It
is stored as `settings.comments.policy` on the entity schema.

**9.6 Standalone sign-in and sign-out.** A commenter can sign in with an email
link and a device cookie without a draft comment, and can sign out.

**9.7 Locked author name.** On a policy schema the form's name field is hidden
and filled through the `comment.draft` filter. There are no renames.

**9.8 Plugin-created entries.** A plugin granted `entities.write.own` can
create entries of the one content type it declares; that is specified in
[`docs/PLUGIN_API.md`](../PLUGIN_API.md). Comments on those entries stay core.

## Out of scope

- SSO, OAuth, social login and passkeys for commenters.
- A website or author URL field.
- Votes, karma, scores, password accounts and public profiles in core (a plugin
  keeps them; core stores only the rank a plugin writes, Decision 9).
- Reactions, emoji, rich media and mentions.
- A plugin API that creates comments: comments are posted only through core's
  form (plugins may moderate comments on their own content type, Decision 9).
- Any shared or federated spam list, hosted spam service or data-sharing
  contract.

## Consequences

**Positive**

- Pages stay cached. Only the page whose comments changed is invalidated.
- The client address never leaves core and is never stored raw.
- One admin, one design system. Export, backup, webhooks, MCP and the importer
  cover comments.
- Sites that do not want comments see no change.

**Negative**

- More code and more attack surface in every install, even where comments stay
  off. Off by default is the mitigation.
- Spam fixes ship as core releases. A site cannot wait for a plugin update from
  somebody else.
- Core now hosts public user content: stored XSS risk, takedown requests, and
  GDPR export and erasure for commenters.

## Implementation order

1. This ADR.
2. Targeted render-cache invalidation and per-key cache generations, the
   comment text format and the core spam primitives. These and step 3 do not
   depend on each other and can go in any order; rendering needs the cache part.
3. The data model and service. The migration leaves comments off.
4. Identity, admin, rendering, the submit pipeline, integrations (webhooks,
   MCP), import and export, and notifications, in parallel where possible.
5. End-to-end tests and the final [`docs/COMMENTS.md`](../COMMENTS.md).
