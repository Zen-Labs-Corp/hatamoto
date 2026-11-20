# Comments

> **Being built.** No release has comments yet. This page grows as each piece lands.
> The design and its reasons: [ADR 0005](adr/0005-comments-in-core.md).

Comments are off on every site until an owner turns them on.

## Enable comments

Only an owner can turn comments on, in Settings > Comments, after two warnings that comments invite spam. Editors see the switch read-only. Not written yet.

## Moderation

Owners and editors moderate every comment. Every comment with a link and every first comment waits in the queue. Not written yet.

## Trust levels

`new`, `known`, `trusted` and `blocked`. The defaults are 3 approved comments and 7 days for `known`, and 15 comments and 30 days for `trusted`; both are editable. Links are held at every level, except in replies written by an owner or editor as the site. Not written yet.

## Themes

Comments are threaded, up to 5 levels by default. Themes can override the comment list, item and form partials. Posting needs no JavaScript. Not written yet.

## Privacy and retention

No raw IP address is stored, only keyed hashes, which are pseudonymous personal data. A truncated user agent is stored with each comment. There is no website field. Spam and trash are purged after 30 days by default, and unconfirmed comments after 24 hours.. The owner can export or erase (delete or anonymise) everything held for an email address, including the IP-hash block that marking one of its comments as spam added to the blocklist. Network (/24) blocks are shared by everyone on that network and are never removed by an erase. Not written yet.

## Plugins

Plugins can add to a comment's spam score through a hook that never sees the IP address or the email. Not written yet.

## For developers

This part ships the data model and services only. Nothing is wired to the server yet, so none of this is visible on a site. The rules behind the design are in [ADR 0005](adr/0005-comments-in-core.md).

### Tables

- `hatamoto_comments`: one row per comment, in a tree (parent, path, depth), with the raw and the sanitised body, status, spam score and verdict. Plugins reference a comment by `hatamoto_comments(id)`.
- `hatamoto_commenters`: one row per email address: trust level, approval counts, dates and a token version.
- `hatamoto_comment_settings`: one row, `enabled` is false until the owner turns comments on, plus a JSON `settings` column.
- `hatamoto_comment_entities`: per page: `comment_count`, `pending_count` and `mode` (`open`, `closed` or `hidden`; NULL means inherit).
- `hatamoto_comment_blocks`: blocked values by kind (email domain, link domain, hashes), seeded with disposable email domains.
- `hatamoto_comment_form_tokens`: form tokens already used, so each works once.

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

The default mode for a content type is `entity_schemas.settings.comments.mode`; a page overrides it with its own `mode`. Three numbers are fixed in code, not settings: `EditWindow` (15 minutes), `DraftTTL` (24 hours) and the 90-day spam quiet period a trusted commenter needs.

### internal/comment

`comment.Service` methods: `Store`, `Confirm`, `Approve`, `MarkSpam`, `Trash`, `Delete`, `Edit`, `Thread`, `Counts`, `StatusCounts`, `Queue`, `Vouch`, `Block`, `RevokeDevices`, `ExportByEmail`, `EraseByEmail`, `Settings`, `SaveSettings`, `EntityMode`, `SetEntityMode`, `Commenter`, `MatchBlocks`, `Blocks`, `AddBlock`, `RemoveBlock`, `ConsumeFormToken`.

`Store` takes a draft that is already formatted and scored. The package does not import `internal/spam` or `internal/commentfmt`; the caller maps their results onto the draft.

### Observer and Checker

An `Observer` hears `OnCreated`, `OnConfirmed`, `OnApproved`, `OnSpam`, `OnDeleted`, `OnUpdated` and `OnSettingsChanged`. `OnUpdated(before, after)` covers what the others do not: every `Edit` and every move to pending (an unapproval), so a cache that shows a published comment can always see it change. Events fire after the commit and never fail a write.

A `Checker` adds to the spam decision. Its input is the body, the link domains and the hashes, never the IP address or the email. If it returns an error or times out, the comment is held.

### internal/commentfmt

`Render` turns the raw body into safe HTML and returns a `Result` with `HTML`, `HasLinks`, `LinkDomains` and `BodyHash`. Every link gets `rel="ugc nofollow noopener"`.

### internal/spam

`CommentSubject` gives the subject key for a comment form token, and `Hold` is a comment-only decision that forms never get. `Score`, `ScoreInput` and `Signal` sort a submission into three buckets: allow, hold or spam. Form behaviour is unchanged.

### Identity

Package `internal/comment/identity` confirms the commenter's email address and keeps the commenter signed in on that device. The flow:

1. The public part stores the comment as a draft and calls `RequestConfirmation`.
2. A mail goes out with the site name, the page title and a link. It never contains the commenter's name or the comment text.
3. The link opens a page with one Confirm button. A GET never confirms anything, because mail scanners and link previews fetch links.
4. A same-origin POST consumes the link, calls `comment.Service.Confirm`, sets the `hm_c` cookie and redirects to `#comment-<id>` on the page.

The link works once and for 24 hours. Only its SHA-256 is stored, in `hatamoto_comment_confirmations`. At most 3 mails go to one address, 10 to one `ip_hash` and 30 to one `net_hash` per 24 hours. Over the limit the visitor sees the same page as before and nothing is sent.

`hm_c` holds the commenter id and a token version. Trust stays in `hatamoto_commenters`, so a lost cookie costs one more confirmation and keeps the trust level. `RevokeDevices`, `Block` and a spam mark bump `token_version`, which invalidates every cookie of that commenter.

Disposable email domains come from `hatamoto_comment_blocks` (kind `email_domain`, seeded, editable by the owner) and are refused with a neutral message.

The hourly job `comment_drafts_purge` deletes drafts with status `draft` older than 24 hours, old confirmation rows and commenters that only ever had drafts.

Set the site address before enabling comments: without it no confirmation mail is sent.

### Comment counts

Counts live in `hatamoto_comment_entities`, not on the page row, so a moderation action never purges the whole page cache. Until targeted cache invalidation lands, a listing page can show counts up to the page cache time (5 minutes) out of date.

### Server secret

Rotating `HATAMOTO_SECRET` orphans every hash and makes the sealed emails unreadable. Commenters start again at `new`. It also signs every commenter device out, because `hm_c` is keyed from the secret.
