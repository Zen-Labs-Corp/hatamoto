# Comments

> **Being built.** No release has comments yet. This page grows as each piece lands.
> The design and its reasons: [ADR 0005](adr/0005-comments-in-core.md).

Comments are off on every site until an owner turns them on.

## Enable comments

Only an owner can turn comments on, in Settings > Comments. Turning them on takes two warnings that comments invite spam: a notice on the page, then a confirmation where the owner ticks "I understand this will invite spam" before the button works. The switch is recorded in the audit log. Editors can open the page but see the switch and the settings read-only.

Once comments are on, the page shows the trust levels, limits, sort order and captcha settings, and a per-type and per-entry control appears on the schema and entry screens to open, close or hide comments there.

Comments cannot be turned on until the site address is set (Settings > General, or `PUBLIC_BASE_URL`): the settings page says so and links there. If comments are already on and the address is removed, Settings > Comments and the Overview card warn, the server logs it once at start, and comments can still be turned off.

## Moderation

Owners and editors moderate every comment. Every comment with a link and every first comment waits in the queue.

The Comments screen in the sidebar lists comments in five tabs: Pending, Approved, Spam, Trash and Dead. Search by text and filter by entry, commenter trust or "has links". A held comment shows why it was held, for example a link or a first comment. The sidebar item shows the number of comments waiting, and shows once comments are on or any comment exists.

Each row can be approved, replied to as the site, edited, marked as spam or trashed. Approving a comment from a new commenter can also vouch for them, which raises their trust level. Marking spam can teach the site to catch the same text, address or links again. Select several rows to approve, mark as spam, trash or delete them together; deleting removes a comment and its replies for good and asks first.

Under Settings > Comments there are two more pages. Commenters lists the people who commented: search them, vouch for or block one, export what is stored about them, sign them out everywhere or erase them. Sign out all devices ends the commenter's sign-in on every device; the trust level stays. Erasing has one behaviour: comments without replies are deleted and the others are made anonymous. Webhooks and plugins hear of the deleted ones that were once approved (`comment.deleted`), not of the anonymised ones. Blocklists holds the email and link domains that mark a comment as spam, which owners and editors can edit, and the spam texts, addresses and networks learned from Mark as spam, which can only be cleared.

**Dead comments.** A plugin that created a content type can mark comments on it dead (and restore them). A dead comment is kept for good: the retention job never purges it, its replies stay under it, and it is left out of every comment count. Visitors see a placeholder ("[dead]" in the default theme) instead of the name and text. The author still reads their own comment, and so does a visitor the same plugin flagged to show dead comments; a page read that way is never cached for others. Dead comments cannot be edited, and no reply notification is sent for them. The Dead tab lists them with the plugin that marked them; each row can be restored (it becomes approved), marked as spam, trashed or deleted, and the bulk bar offers the same. Staff mark nothing dead themselves.

**Order.** Besides newest and oldest first, a content type can order top-level comments by rank: higher rank first, then newest. A plugin that owns the type sets the ranks; the site-wide setting is not changed by it. Replies stay in the order of their thread.

The Overview shows a Comments card with the newest waiting comments and a link to the queue; it is left out while comments are off and nothing is waiting.

## Community policy (per content type)

A content type can switch comments to a community policy. It is owner-only, under Settings > Comments > Content types, and turning it on takes a warning: "Are you sure? With the community policy on, comments with links and first comments on this content type are posted at once, without review. Spam checks, blocked commenters, duplicates, captcha and surge mode still hold comments." The owner ticks "I understand comments on this type will not wait for review" before the button works. Turning it off needs no confirmation. Editors see the panel read-only, and every change is recorded in the audit log as `comments.policy`.

On a type with the policy on, comments with links and first comments from new commenters are posted at once. Spam checks (including the scores plugins add with `comment.check`), blocked commenters, duplicate bodies, captcha, surge mode and slow mode still hold comments. A commenter who edits an approved comment into one with a link keeps it approved; an edit that the content checks hold still goes back to the queue.

The policy also sets three numbers that replace the site values for that type only: reply depth (1 to 32, default 10), the edit window in minutes (1 to 1440, default 15) and slow mode in seconds (0 turns it off). Other types keep the site settings. A long edit window has a cost: a commenter's own page views are not served from the page cache for that whole window, so a 24 hour window on a busy type means many uncached page views. Built-in types cannot use the policy yet.

Each type can also choose its own thread order: Site default, Newest first or Oldest first. "By rank" is offered only while a plugin that holds `comments.moderate` for the type is installed (or when rank is already stored); the plugin applies the rank.

## Notifications

Owners and editors who have an email address get a mail when a comment waits for review. The "Email moderators" setting under Settings > Comments chooses how: a mail for each comment (at most one every 10 minutes per person, the rest in one summary), an hourly summary, or off. The mails go out from a job that runs every minute. Set the site address and outgoing email first, or nothing is sent.

Commenters can tick "Email me when someone replies". It only takes effect once their address is confirmed, either through the confirmation email or on a device that already confirmed it. When a reply to their comment is approved, they get a mail with the link to the page: at most one every 15 minutes per person. Replies that arrive in between wait until the 15 minutes are over: a single one then goes out as the normal reply mail, several together in one summary. Nobody is mailed about their own reply.

Every mail (the reply summary too) has a one-click unsubscribe link, also sent as a `List-Unsubscribe` header. A moderator who unsubscribed can turn mail back on from the same page.

When surge mode starts, every owner with an email address gets one mail (whatever the Email moderators setting), with a link to the pending queue. The next one comes only after the surge has ended: no server that can send the surge mail has reported a surge for 12 minutes (the 10 minute window plus the one minute between reports and the one minute a mail run may take). A server that cannot send (no outgoing email, no site address, or `comment_notify` switched off) does not report, so it does not keep the episode open. The mail holds no comment text and no commenter names, and has no unsubscribe link (it is a rare site alarm). It needs the site address and outgoing email, like the other mails.

## Trust levels

`new`, `known`, `trusted` and `blocked`. The defaults are 3 approved comments and 7 days for `known`, and 15 comments and 30 days for `trusted`; both are editable. Links are held at every level, except in replies written by an owner or editor as the site. Not written yet.

## Themes

On a page with comments on, the default theme shows the form first and the thread below it, newest first unless the owner chose oldest first. Replies nest up to 5 levels by default, 50 top-level comments make a page, and older pages are reached with `?cpage=N` links (those pages are `noindex`). Comments with a link, and a first comment from a new address, wait for review.

Three partials make the section: `comments-list`, `comments-item` and `comments-form`. A theme overrides any one of them by shipping a file of the same name under `partials/`; the others keep the default. The styles are in the asset `comments.css`, which the default section loads only on pages with comments. The page that opens from the confirmation email is the layout `comment-confirm`, so it matches the site. How to write these is in [THEMES.md](THEMES.md#comments).

Posting needs no JavaScript: the form is a plain `POST` answered with a redirect back to the page, to the new comment (`#comment-<id>`) or, when the comment waits for the confirmation email or review, to a notice. A theme that loads htmx gets the form and the new comment as a fragment instead of a reload.

A visitor who has posted sees their own comments that are not public yet, marked as theirs, and their approved comments can be edited or deleted for 15 minutes. Nobody else sees them.

Pages with an open form are not cached by browsers or CDNs (see [Page cache](THEMES.md#page-cache)): the form carries a token that works once.

## Static mode

With static hosting on (see [STATIC_EXPORT.md](STATIC_EXPORT.md)), the thread is static HTML. The form is not: it loads live from `GET /-/comments/form/{entityID}` on the live origin (for example `https://live.example.net`). That route answers CORS for the static origin (`https://static.example.net`) only, sets no cookies, and is `private, no-store`. Each load carries a fresh token that is bound to the visitor's network and works once.

- The core loader `hm-live.js` swaps the placeholder on the static page for that form. Without JavaScript, or if the load fails, a link in the placeholder opens the form on the live origin.
- After posting, the visitor returns to the static page. A new comment shows there after the next export; approving a comment triggers one.
- Editing and deleting one's own comment stay on the live origin.
- Forms with a captcha need the provider's script on the static page. This is a known gap.
- Themes keep the `data-hm-cform` contract; nothing changes for theme authors.

## Privacy and retention

No raw IP address is stored, only keyed hashes (`ip_hash` and `net_hash`), which are pseudonymous personal data. A truncated user agent is stored with each comment. There is no website field.

**Purging.** The daily retention job deletes spam and trash `purge_days` (default 30) after their last status change (`updated_at`). One that still has replies is not deleted: its text, name, hashes and user agent are blanked so the thread stays intact. The hourly job removes unconfirmed drafts after 24 hours and used or expired form tokens. The daily job also prunes reply-notification rows and moderator-notification rows of comments that are no longer held.

**Export.** The owner can export everything stored for an email address: the address, trust level, approved count and dates of the commenter, and for each of their comments its ids, status, name, text, `ip_hash`, `net_hash`, the user agent, the reply opt-in, the spam score, verdict and reasons, the locale and the dates. Fields computed from the body (`body_html`, `body_hash`, link data, path and depth) and internal references (the stored address hash and sealed copy, the approving user) are left out.

**Erase.** Erasing has one behaviour: comments without replies are deleted, the others are made anonymous, and the commenter is removed. IP-hash blocks that marking one of their comments as spam added are removed. Network (/24) blocks are shared by everyone on that network and are kept.

**Email log.** Confirmation, reply and reply-summary mails to commenters are logged with a masked address (only the first letter of the name, the first letter of the domain and its last label are kept, like `a***@e***.com`) and no body, so an erase can not match them and does not need to. Moderator mails and the surge mail go to staff addresses and their bodies are not stored either. These rows age out with the email log retention setting.

**Limits.** Erase does not reach backups: older copies keep the data until the backups rotate.

## Plugins

Plugins that hold `comments.moderate` can mark comments dead, restore them and rank them on their own content type, and hear of changes through `comment.updated` and `comment.deleted` (see the plugin API). Plugins can add to a comment's spam score through a hook that never sees the IP address or the email. Not written yet.

## For developers

The rules behind the design are in [ADR 0005](adr/0005-comments-in-core.md).

### Tables

- `hatamoto_comments`: one row per comment, in a tree (parent, path, depth), with the raw and the sanitised body, status, spam score and verdict. Plugins reference a comment by `hatamoto_comments(id)`.
- `hatamoto_commenters`: one row per email address: trust level, approval counts, dates and a token version.
- `hatamoto_comment_settings`: one row, `enabled` is false until the owner turns comments on, plus a JSON `settings` column.
- `hatamoto_comment_entities`: per page: `comment_count`, `pending_count` and `mode` (`open`, `closed` or `hidden`; NULL means inherit).
- `hatamoto_comment_blocks`: blocked values by kind (email domain, link domain, hashes), seeded with disposable email domains.
- `hatamoto_comment_form_tokens`: form tokens already used, so each works once.
- `hatamoto_comment_moderator_notify`: per moderator, the mute flag and the last mail time.
- `hatamoto_comment_notify_queue`: comments waiting to be mailed to moderators.
- `hatamoto_comment_reply_queue`: approved replies waiting to be mailed to the parent's author; sent rows also hold the 15-minute limit per person (no separate table).

`hatamoto_comments.notify_replies` holds the reply opt-in of that comment. It only takes effect once the commenter's address is confirmed.

There is no author URL column and no raw IP column: only `ip_hash` and `net_hash`, which are pseudonymous personal data, never anonymous. The email is stored as an HMAC (to look a commenter up) plus a sealed copy (to read it back).

### Settings

The JSON keys in `hatamoto_comment_settings.settings`, with their defaults:

| Key | Default |
|---|---|
| trust thresholds | 3 / 7 / 15 / 30 |
| `auto_close_days` | 30 |
| `max_depth` | 5 |
| `page_size` | 50 |
| sort | newest |
| `slow_mode_seconds` | 120 |
| captcha | false |
| `device_cookie_days` | 90 (how long the `hm_c` commenter cookie lasts) |
| `purge_days` | 30 |
| `notify_mode` | `per_comment` (or `digest`, `off`) |

The default mode for a content type is `entity_schemas.settings.comments.mode`; a page overrides it with its own `mode`. The same `settings.comments` object holds `order` (`newest`, `oldest` or `rank`; a plugin applies `rank`) and `policy` (`enabled`, `max_depth`, `edit_window_minutes`, `slow_mode_seconds`). A bad stored value reads as off or the field default, never as an error, and no migration is involved. `comment.MergeSchemaComments` is the write helper: it rewrites `order` and `policy` and keeps every other key. `Service.EntityConfig` returns the mode, order and policy of an entity in one query; `EntityMode` wraps it. With the policy off, every number is the site value or the fixed one below. Four numbers are fixed in code, not settings: `EditWindow` (15 minutes, unless the type's policy sets another), `DraftTTL` (24 hours), the 90-day spam quiet period a trusted commenter needs and the 15-minute reply-mail window.

### internal/comment

`comment.Service` methods: `Store`, `Confirm`, `Approve`, `MarkSpam`, `Trash`, `Delete`, `Edit`, `Get`, `Thread`, `Counts`, `HasNonPublic`, `ViewerComments`, `HasEditable`, `EditableComments`, `DuplicateSince`, `PurgeFormTokens`, `StatusCounts`, `Queue`, `Vouch`, `Block`, `RevokeDevices`, `ExportByEmail`, `EraseByEmail`, `Settings`, `SaveSettings`, `EntityMode`, `SetEntityMode`, `Commenter`, `MatchBlocks`, `Blocks`, `AddBlock`, `RemoveBlock`, `ConsumeFormToken`, `PurgeSpamTrash`, `QueueModeration`, `ClaimModeration`, `ClearModeration`, `SetModeratorMuted`, `QueueReply`, `ClaimReplyBatches`, `PruneModerationQueue`, `PruneReplyQueue`, `UnsubscribeReplies`, `NoteSurge`.

The job `comment_notify` runs every minute (switch it off with `comment_notify` in `WORKER_DISABLE_JOBS`; that also stops the surge mail from that server). Spam and trash are purged inside `retention_purge`. The unsubscribe link is `/-/comments/unsubscribe/<token>`: a GET shows a button and a POST unsubscribes (RFC 8058 one-click). Reply mail is limited per commenter with a fixed 15-minute window (a constant, not a setting); several replicas can run the job, each commenter is claimed under a Postgres advisory lock.

`Store` takes a draft that is already formatted and scored. The package does not import `internal/spam` or `internal/commentfmt`; the caller maps their results onto the draft.

`Importer` writes comments from another system (the WordPress import) in one transaction per page: historical dates and statuses, threading clamped to the max depth, commenters hashed and sealed like live ones, trust capped at known. It fires no observer. `Resync` runs after a bulk load of comment rows (the site import): it moves the comment sequence past the loaded rows and rebuilds the per-page counts. Neither changes the site switch or a page's mode.

### Observer and Checker

An `Observer` hears `OnCreated`, `OnConfirmed`, `OnApproved`, `OnSpam`, `OnDeleted`, `OnUpdated` and `OnSettingsChanged`. `OnUpdated(before, after)` covers what the others do not: every `Edit` and every move to pending (an unapproval), so a cache that shows a published comment can always see it change. Events fire after the commit and never fail a write.

A `Checker` adds to the spam decision. Its input is the body, the link domains and the hashes, never the IP address or the email. If it returns an error or times out, the comment is held.

### internal/commentfmt

`Render` turns the raw body into safe HTML and returns a `Result` with `HTML`, `HasLinks`, `LinkDomains` and `BodyHash`. Every link gets `rel="ugc nofollow noopener"`.

### internal/spam

`CommentSubject` gives the subject key for a comment form token, and `Hold` is a comment-only decision that forms never get. `Score`, `ScoreInput` and `Signal` sort a submission into three buckets: allow, hold or spam. Form behaviour is unchanged.

### Public form

Routes, all under the reserved `/-/` segment (the word `comments` itself is not reserved, so no page path changes):

- `POST /-/comments/{entityID}` posts a comment.
- `POST /-/comments/edit/{commentID}` edits one (the field is `body`).
- `POST /-/comments/delete/{commentID}` moves one to the trash.
- `GET` and `POST /-/comments/confirm/{token}` is the confirmation link, see Identity.

All of them answer `404` while the site switch is off, sit behind the site-access gate (maintenance, coming-soon and password modes refuse them), are limited per IP network and are sent `Cache-Control: no-store`. A post answers `303` to the page: `#comment-<id>` when posted, `?cnotice=check_email#hm-comments` when it waits for the email, `?cnotice=posted#hm-comments` when it was dropped (see below) and `?cerror=closed#hm-comments` when the page is closed. The exception is an error the visitor can fix (`body_empty`, `body_long`, `name`, `email`, `email_domain`): that answers `200` with the page itself, the error at `#hm-comment-form` and the name, email, comment, reply opt-in and reply target as typed, under a fresh token and new field names. It is sent `private, no-cache, no-store`. The typed values are only in that response body, never in a URL or a cookie. With `HX-Request: true` the same form comes back as a fragment. `?cerror=<code>` remains a code a `GET` can read, as a fallback when the page cannot be rendered. A dropped post never gets its values back.

**Token and field names.** Each view of a page with an open form gets a token (`hm_ct`) that is bound to the page and to a hash of the visitor's network (the /24 for IPv4, the /64 for IPv6), so an address change inside that network keeps the form working. It works once and expires after 4 hours. A form opened before an upgrade that changes the binding answers like a dropped post once. The names of the body, name, email and honeypot fields are derived from the token, so they change on every view. A token used or expired answers like a success and stores nothing. The used tokens are kept in `hatamoto_comment_form_tokens` and purged hourly by the job `comment_drafts_purge`.

**Submit order.** Cheapest checks first; everything that rejects a bot answers with the same redirect a held comment gets, except mistakes a person can fix:

1. Site switch on and the page open. Closed or auto-closed pages store nothing.
2. Surge breaker: above a site-wide rate every new comment is held as pending, whatever the trust level. The start of a surge is logged, and the owners are emailed once per surge (state shared between servers in `hatamoto_digest_state` under the name `comment_surge`).
3. Single-use token.
4. Fields are read only under the rotated names.
5. Rate limits per IP hash, network hash, email hash and page. They live in memory, per process, and reset on a restart.
6. Honeypot filled.
7. `Sec-Fetch-Site` other than `same-origin`, or an `Origin` that is `null` or another host.
8. Captcha, only when the owner enabled it.
9. Size limits (5000 characters of body, 80 of name) and a valid email address.
10. Formatting, then the blocklists (email domain, link domains, hashes).
11. A duplicate body posted recently on the site.
12. Link check: any link holds the comment as pending.
13. Spam score into allow, hold or spam.
14. The `comment.check` hook (`comment.Checker`) adds to the decision. It has a 500 ms timeout; a timeout or an error holds the comment.

Then trust decides: a trusted commenter is approved, others wait, and slow mode (`slow_mode_seconds`, trusted commenters exempt) holds a commenter's next comment on the same page.

**Shadow hold.** A comment scored as spam or blocked is stored but looks posted to its sender: it appears through their own device cookie and nowhere else, and nothing tells them it is held. Visitors without the cookie never see it.

**Admins.** An owner or editor signed in to the admin posts as the site: no email, no confirmation, no trust or slow mode, stored approved with the account as author.

**Edit and delete.** The commenter can edit or delete their own comment for `comment.EditWindow` (15 minutes) through the signed `hm_c` cookie; ownership never comes from the form. An edit that adds a link moves the comment back to pending. Delete moves it to the trash. A request from another site, without the cookie, for another commenter's comment, past the window, or for a trashed comment is answered `403`; an unknown id is `404`. A bad edit goes back to the page with `?cerror=body_empty` or `body_long`.

**Cache.** A page with an open form is cached as a template and filled per request, so it is sent `private, no-cache, no-store`. A visitor with own comments that are not public yet, or still editable, gets an uncached page. A cookie that is not validly signed is ignored and gets the cached page. Details: [THEMES.md](THEMES.md#page-cache).

### Identity

Package `internal/comment/identity` confirms the commenter's email address and keeps the commenter signed in on that device. The flow:

1. The public part stores the comment as a draft and calls `RequestConfirmation`.
2. A mail goes out with the site name, the page title and a link. It never contains the commenter's name or the comment text.
3. The link, `/-/comments/confirm/<token>`, opens a page with one Confirm button, rendered with the theme's `comment-confirm` layout (the plain built-in page when no theme is active). A GET never confirms anything, because mail scanners and link previews fetch links.
4. A same-origin POST consumes the link, calls `comment.Service.Confirm`, sets the `hm_c` cookie and redirects to `#comment-<id>` on the page.

The link works once and for 24 hours. Only its SHA-256 is stored, in `hatamoto_comment_confirmations`. At most 3 mails go to one address, 10 to one `ip_hash` and 30 to one `net_hash` per 24 hours. Over the limit the visitor sees the same page as before and nothing is sent.

`hm_c` holds the commenter id and a token version. Trust stays in `hatamoto_commenters`, so a lost cookie costs one more confirmation and keeps the trust level. `RevokeDevices`, `Block` and a spam mark bump `token_version`, which invalidates every cookie of that commenter.

Disposable email domains come from `hatamoto_comment_blocks` (kind `email_domain`, seeded, editable by the owner) and are refused with a neutral message.

The hourly job `comment_drafts_purge` deletes drafts with status `draft` older than 24 hours, old confirmation rows and commenters that only ever had drafts.

Set the site address before enabling comments: without it no confirmation mail is sent.

### Comment counts

Counts live in `hatamoto_comment_entities`, not on the page row, so a moderation action never purges the whole page cache. A comment change drops only the cached copies of that one page. Listings do not show counts, so they are not dropped; the count is rendered only on the page itself.

### Load

The test `TestCommentLoadIntegration` (`internal/server`, build tag `integration`) seeds one post with 500 approved comments in a tree 5 levels deep (100 threads of 5) and renders it through the real handler. One run on a development machine with a local Postgres 16, 100 renders each:

| Page | p50 | p95 | Allocations | HTML |
|---|---|---|---|---|
| Page 1, rendered from scratch | 84 ms | 89 ms | 30,000 | 165 KB |
| Page 2, rendered from scratch | 84 ms | 90 ms | 30,000 | 164 KB |
| Page 1, from the page cache | 0.4 ms | 1.9 ms | | |

Each uncached render runs exactly one thread query. With a stream of one approval every 10 seconds and 100 reads in between, the post renders once per approval and a page of another post never re-renders. The test logs these numbers and asserts only the query and render counts, never a time.

### Server secret

Rotating `HATAMOTO_SECRET` orphans every hash and makes the sealed emails unreadable. Commenters start again at `new`. It also signs every commenter device out, because `hm_c` is keyed from the secret. Exports and backups record a check value of the secret, and importing or restoring under a different secret is refused unless `--force` is given, which drops the commenter emails (see [EXPORT.md](EXPORT.md) and [BACKUPS.md](BACKUPS.md)).
