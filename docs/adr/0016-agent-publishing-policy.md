# ADR 0016: Agent publishing policy

Status: accepted (2026-11-24)

## The rules in plain words

1. A wrapper module may decide, per API key, whether a change an agent makes to
   live content goes through, waits for a person's review, or is refused.
2. People are not asked. Roles and the review step govern what people do.
3. A build that registers no policy asks no key anything and behaves as before.
4. A policy that fails refuses the change. It is never let through on a
   doubtful answer.
5. The site's own review rule still applies. The stricter of the two wins.

## Context

An API key lets an agent write through REST and MCP. The key carries the scopes
and the role of the person who made it, which says what the key may do but not
whether a given change should go live unattended. A site may want some keys
limited to drafts, some held for a person's look, some shut for a time. The core
should not hard-code those rules. A wrapper module that builds its own binary
around `app.Main` (as it does for [ADR 0011](0011-review-policy.md)) can supply
them.

## Decision

### 1. The open interface

The public package `hatamoto.dev/core/agentpolicy` has:

- `Request`: the key (`KeyID`, `KeyName`), the surface (`Via`, `api` or `mcp`),
  the `Action` and the `Entity` it touches (`ID`, `Schema`, `Locale`, `Slug`,
  `Status`). All fields are plain strings.
- `Policy`: `Decide(ctx, Request) (Decision, error)`.
- `Decision`, built with `Allow()`, `RequireReview(reason)` or `Deny(reason)`.
  The zero `Decision` is invalid.
- `Register(Policy)`: called from an `init` function. It panics on a second
  call, with a nil policy, or after `app.Main` has frozen the registration.
- `Changes(ctx, keyID, since)`: what a key changed, read from the audit log.

`Request` and `Entity` may gain fields; build them with keyed literals.
`Policy` will not gain methods.

### 2. When it is asked

Only for a write made with an API key that publishes, schedules, restores or
edits live content (`publish`, `schedule`, `restore`, `edit`), on REST and MCP,
and for the REST link insert, which edits the source entry directly. Moving an
entry under another parent is an edit. Drafts are not asked.

### 3. The three outcomes

- `Allow`: the change goes on to the site's own review rule. A policy's Allow
  never lets a key skip it.
- `RequireReview`: the change is staged for a person, even when the site's
  review switch is off. The REST API answers 202 and MCP returns
  `pending_review`, as for any staged change. On a path that cannot stage (the
  link insert) it is a refusal.
- `Deny`: nothing is written. REST answers 403 with the reason, MCP returns an
  error with it. The reason is trimmed, kept to one line and capped at 200
  characters. One audit row, `agent_policy.deny`, records the refusal with the
  key id and result `denied`.

Stricter wins: Deny over RequireReview over Allow, and the site's review rule is
asked first.

### 4. Who may decide a staged change

A staged agent change records its requester as `api-key:<key id>`, not the
person who made the key. Nobody decides their own change, so with the creator as
requester a site with one owner could never approve its own key's work. The
creator, and anyone else who may decide review changes, can approve it. The
requester text is stable across key renames.

A key needs the `write:entities` scope and a creator whose role may request
review. A key made by a Contributor may therefore stage a publish of its own
entry, as a Contributor may.

### 5. Failure

A policy that returns an error, panics or returns an invalid decision refuses
the change (fail closed) and stages nothing. The refusal is audited as
`agent_policy.deny` with outcome `error`. The registration cannot change after
start. Hot paths should not wait on the policy: it is called on the request
path of every such write, so cache what is slow.

### 6. Changes

`Changes` returns the audit rows written for one key: its entity writes, its
refusals and the review requests of its staged changes
(`changeset.edit_submit`). Oldest first, at most 1000 per call; page by calling
again with the last row's time. A change a person approved is attributed to
that person, not to the key. It works in serve only; elsewhere it returns
`ErrUnavailable`. It reads the audit table and nothing else.

## Not covered

Creating drafts, unpublishing, archiving, media, menus, redirects, terms and
settings are not asked in this release. The set of actions can grow.

## Consequences

- A standard build is unchanged: nothing registered means no policy call, no
  extra query and no extra audit row.
- There is no database change.
- Staged agent changes are decided where review changes are decided. Until the
  review screens ship there is no screen for them.
- A broken policy blocks an agent's writes instead of opening them.
