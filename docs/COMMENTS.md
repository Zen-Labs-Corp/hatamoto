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

No raw IP address is stored, only keyed hashes, which are pseudonymous personal data. A truncated user agent is stored with each comment. There is no website field. Spam and trash are purged after 30 days by default, and unconfirmed comments after 24 hours. The owner can export or erase (delete or anonymise) everything held for an email address. Not written yet.

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
| `device_cookie_days` | 90 |
| `purge_days` | 30 |

The default mode for a content type is `entity_schemas.settings.comments.mode`; a page overrides it with its own `mode`. Three numbers are fixed in code, not settings: `EditWindow` (15 minutes), `DraftTTL` (24 hours) and the 90-day spam quiet period a trusted commenter needs.

### internal/comment

`comment.Service` methods: `Store`, `Confirm`, `Approve`, `MarkSpam`, `Trash`, `Delete`, `Edit`, `Thread`, `Counts`, `StatusCounts`, `Queue`, `Vouch`, `Block`, `RevokeDevices`, `ExportByEmail`, `EraseByEmail`, `Settings`, `SaveSettings`, `EntityMode`, `SetEntityMode`, `Commenter`, `MatchBlocks`, `Blocks`, `AddBlock`, `RemoveBlock`, `ConsumeFormToken`.

`Store` takes a draft that is already formatted and scored. The package does not import `internal/spam` or `internal/commentfmt`; the caller maps their results onto the draft.

### Observer and Checker

An `Observer` hears `OnCreated`, `OnConfirmed`, `OnApproved`, `OnSpam`, `OnDeleted` and `OnSettingsChanged`. Events fire after the commit and never fail a write.

A `Checker` adds to the spam decision. Its input is the body, the link domains and the hashes, never the IP address or the email. If it returns an error or times out, the comment is held.

### internal/commentfmt

`Render` turns the raw body into safe HTML and returns a `Result` with `HTML`, `HasLinks`, `LinkDomains` and `BodyHash`. Every link gets `rel="ugc nofollow noopener"`.

### internal/spam

`CommentSubject` gives the subject key for a comment form token, and `Hold` is a comment-only decision that forms never get. `Score`, `ScoreInput` and `Signal` sort a submission into three buckets: allow, hold or spam. Form behaviour is unchanged.

### Comment counts

Counts live in `hatamoto_comment_entities`, not on the page row, so a moderation action never purges the whole page cache. Until targeted cache invalidation lands, a listing page can show counts up to the page cache time (5 minutes) out of date.

### Server secret

Rotating `HATAMOTO_SECRET` orphans every hash and makes the sealed emails unreadable. Commenters start again at `new`.
