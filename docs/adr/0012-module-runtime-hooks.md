# ADR 0012: Module runtime hooks (routes, jobs, mail)

Status: accepted (2026-11-22)

## The rules in plain words

1. A compiled-in module can mount an HTTP handler under its own public prefix
   (`module/route`), run a recurring function once per interval across all
   servers sharing the database (`module/job`), and send mail to the owners or
   to one user through core's mail path (`module/mail`).
2. These are three more extension points in the family of
   [ADR 0007](0007-module-extension-points.md): registered from an `init`
   function, frozen by `app.Main`, a panic on a bad or late registration, and
   no change at all when nothing is registered.
3. A mounted route is not authenticated by core. The module authenticates its
   own callers. Core adds only a rate limit and a body cap.
4. A job runs at most once per interval across servers and restarts, and is
   skipped, never run twice, when core cannot tell whether another server has
   the run.
5. Module mail is core mail: it is sent, logged and suppressed by the same
   code, including staging mode.
6. Module hooks write no audit rows. A module that wants an event calls
   `audit.Record` itself ([ADR 0009](0009-audit-sink.md)).

## Context

ADR 0007 gave a wrapper module commands, migrations, secrets and a service, and
left routes, mail and jobs out of scope. A module that receives webhooks, sends
a periodic report or warns an owner has had to start its own listener, its own
timer and its own SMTP client. That duplicates core's rate limiting, its once
per install scheduling and its email log, and it bypasses staging mode.

## Decision

State lives in `internal/modhost`, one file per hook, with a thin public
package for each, like the hooks of ADR 0007.

### Routes: `module/route`

`route.Register(route.Mount{Module, Prefix, Handler, MaxBody})`.

- **Prefix.** Lower case, starting and ending with `/`, for example
  `/example/` or `/example/hooks/`. A prefix must not overlap another module's
  (equal, or one inside the other).
- **Reserved.** The first segment must not be one core uses: the admin and API
  paths, the plugin, media, form, health and MCP paths, every path the admin
  address check refuses, and the redirect-owned paths. `/` is refused. Under
  `/auth/` a module may mount `/auth/<name>/` for any `<name>` core does not
  serve; core serves `/auth/login` and `/auth/register` (legacy sign-in
  redirects) and `/auth/` itself, which stay refused. A test fails if core adds
  another `/auth/` route that is not on that list.
- **Boot checks.** `hatamoto serve` exits non-zero when a prefix is under the
  admin address, under or over `PUBLIC_API_PREFIX`, or on a scanner probe path
  such as `/wp-admin/`. The `/auth/` probe rule is lifted for registered module
  prefixes only. A prefix is also refused as a later admin address.
- **Dispatch.** A module prefix is served before the site access gate, locale
  handling and redirect rules, like the admin and plugin pages. The admin
  address is always checked first and can never be shadowed.
- **Limits.** Core's sign-in rate limiter applies per client address (5
  requests a second, burst 10), shared by all module mounts. The request body
  is capped at `MaxBody` (default 1 MiB, at most 32 MiB); a read past the cap
  fails with `*http.MaxBytesError`. Methods: GET (and HEAD), POST, PUT, PATCH
  and DELETE.
- **Failed migration.** A module whose migrations failed is not mounted.

### Jobs: `module/job`

`job.Register(module, name, every, run)`; `every` is at least one minute. The
job id is `module:<module>:<name>` and appears on the Jobs screen as
"Module: <module> > <name>". It cannot be started from the admin.

- **Claim.** Each server polls a claim row in the table
  `hatamoto_job_claims` (one row per job id), using the database clock. The
  poll period is a tenth of `every`, at most five minutes and at least one
  second, with jitter. Taking the claim is one conditional upsert; the server
  that wins runs the job, the others skip silently, with no error log and no
  Jobs screen entry.
- **Guarantee.** A claim lasts `every`, so a job is claimed at most once per
  `every`, across servers and restarts, and runs at most `every` plus one poll
  period plus jitter apart. The claim is taken when the run starts, not when it
  ends: a run longer than `every` can overlap the next claim on another
  server. Write jobs to be safe to repeat.
- **Fail closed.** If the claim query fails, the job does not run and the
  failure is recorded for the Jobs screen. A panic in module code is recovered
  and recorded as a failed run.
- **Where it does not run.** Where the worker is off, in drain mode, when
  `WORKER_DISABLE_JOBS` lists the job id, and when the module's migrations
  failed. Staging mode does not stop module jobs.

### Mail: `module/mail`

`mail.ToOwners(ctx, module, msg)` and `mail.ToUser(ctx, module, userID, msg)`.

- **One path.** Every message goes through the same function as core mail. It
  is written to the email log with the kind `module` and the module name, the
  body is not kept, and it is dropped and logged as suppressed when mail is not
  configured, is disabled or the site is in staging mode. A suppressed send
  returns nil.
- **Recipients.** `ToOwners` sends one mail to each active owner (an owner
  whose own access has ended is skipped) and tries all of them; the failures
  are joined. `ToUser` takes the user id as a string and sends whatever the
  user's access state, so a notice about an access that has ended still
  arrives. An unknown user is `ErrUnknownUser`.
- **Availability.** Mail is installed in `serve` only; elsewhere the calls
  return `ErrNoMailer`.

### Audit and staging

Module hooks write no audit rows. A module may call `audit.Record` itself when
it holds a transaction. Staging mode stops module mail (rule 5) but not module
routes or jobs: they are the module's own code, and a module's own outbound
calls are not stopped by core.

## Trust model

Same as ADR 0007: a module is fully trusted, compiled-in code. Routes carry no
core authentication, so a module that mounts a handler is responsible for
checking who calls it, for example by verifying a signature.

## Consequences

- The three public packages are a contract (see "Go module extension points"
  in `docs/PLUGIN_API.md`): names and behaviour are frozen until the next major
  release.
- The new table `hatamoto_job_claims` is created by an expand-only migration.
- The email log screen shows module mail under the label "Other".
- Wasm plugin routes and the admin pages of modules ([ADR 0008](0008-module-admin-pages.md))
  are unchanged.
