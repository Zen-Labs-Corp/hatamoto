# ADR 0008: Cache purge hook

Status: accepted (2026-11-21)

## The rules in plain words

1. A purge is a notification sent after a change is saved. It never decides
   whether the change happens.
2. A purger that fails does not fail or undo the save. The error is logged and
   the call is tried again, up to 3 attempts in total.
3. A default install sends nothing. A purge is sent only when
   `HATAMOTO_PURGE_URL` is set or a compiled-in module registered a purger.
4. The open interface is small: one registered purger and one request type.
   Core does not know any CDN vendor.

## Context

A site served through a CDN or another edge cache keeps old copies of a page
until their lifetime ends. An operator wants the cache told when a page
changes. Core cannot reach every vendor's API, and should not carry one.

Two ways to say "this page changed" are offered. The no-code way is the
`HATAMOTO_PURGE_URL` webhook: core posts a JSON list of changed public URLs to
an address the operator sets. The code way is a purger registered by a module
compiled into a custom build, which can call a vendor API or do anything else.
Both may be on at the same time, and each receives every purge.

## Decision

The `hatamoto.dev/core/purge` package has:

- `Register(name, Purger)`, called from an `init` function before `app.Main`;
- `Request{URLs, All}`: the absolute URLs of changed public pages, or `All`
  when an owner used Clear cache;
- `Purger`, with a method that takes a `Request`.

Core calls the purger after the change is saved, in the background. Calls are
debounced (about 2 seconds, so several changes share one call), a `Request`
carries at most 500 URLs, and a failed call is tried up to 3 times in total.

`Register` panics when it is called after `app.Main` started, a second time,
with a nil purger, or with a name that does not match `^[a-z][a-z0-9-]{0,31}$`.
A wrong setup stops the program at start, not at the first purge.

The built-in webhook sends `POST` with the body
`{"urls":["https://example.com/a"],"all":false}`, an optional
`Authorization: Bearer` token from `HATAMOTO_PURGE_TOKEN`, no redirects, and a
10 second timeout. The address is fetched through the same outbound guard as
other operator-set addresses, so a private or loopback address needs
`HATAMOTO_PURGE_ALLOW_PRIVATE=true`.

### Failure semantics

A purger error is logged and retried. It never fails or undoes the save,
because the change is already committed when the purger runs. Other
registered implementations in core fail closed: if they cannot do their job,
the action they guard is refused. This hook reads that rule on purpose as
applying to the thing it guards. A purge is a notification after the commit,
so there is nothing left to refuse; failing the request would only tell the
author that a save which happened did not. The cost is that a failed purge can
leave an old copy at the edge until its lifetime ends, and the log shows it.

## Consequences

- The list holds a page's own URL and its archive page. Listing pages, feeds
  and the home page are not in it, writes made with the `hatamoto` command line
  tool send no purge, and settings, theme, media and forms changes send none.
  An operator who needs those uses the cache's lifetime or treats any purge as
  a purge of everything.
- With several servers, the server that made the change sends the request. The
  other servers send nothing for it.
- A purge that is queued when a process stops can be lost. The final flush has
  a short time limit.

## Non-goals

- Calling vendor CDN APIs from core.
- Choosing one server to send purges (leader election). The server that made
  the change sends its own.
