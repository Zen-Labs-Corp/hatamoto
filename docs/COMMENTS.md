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
