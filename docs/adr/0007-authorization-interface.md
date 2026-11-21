# ADR 0007: Authorization interface

Status: accepted (2026-11-21)

## The rules in plain words

1. A new public Go package, `hatamoto.dev/core/authz`, answers "may this
   subject do this action on this object?". A wrapper module can register its
   own answer from an `init` function.
2. With nothing registered the answers are exactly today's: owners may do
   everything, editors what they can do now, and API keys what their scopes
   and their creator's role allow.
3. A registered authorizer replaces the default. It can call the default to
   keep the answers it does not care about.
4. An error from a registered authorizer denies the action.
5. Registration happens once, before the server starts. After that it is
   frozen.
6. This change adds the interface only. No route, screen or tool asks it yet.

## Context

Access rules are spread over route groups, handler checks, API key scopes and
plugin manifests. Each check names the roles `owner` and `editor` directly, so
a site that needs other rules has to patch core. Moving every check onto one
question is a large change; the question and its default answers have to be
settled and public first, so the moves can follow in small steps without
changing behaviour.

## Decision

- **Shape.** `Can(ctx, Subject, Action, Object) (bool, error)`. `Subject`,
  `Object` and `Action` are plain strings and slices defined in the public
  package; no internal type appears in a signature. They carry only what
  today's rules need. A field added later keeps existing code compiling; a
  field removed does not, so none is added without a use.
- **Catalogue.** Actions are `<area>.<verb>` strings exported as constants
  and listed by `Actions()`. A test keeps a checked-in list of every gate in
  core mapped to an action, so a new route group cannot skip the catalogue.
- **Default.** A table of rules per action: owner, editor, and the API key
  scope. The scope rules are copied into the package, so core packages can
  call it later without an import cycle; a test compares them with the
  originals.
- **Registration.** Same pattern as the edition package: a state package
  under `internal`, `Register` callable from `init`, frozen by `app.Main`,
  panics on nil, typed-nil, a second call and a call after the freeze.
- **Replace, not wrap.** One composition rule (all must agree, or any may
  allow) would suit some modules and not others. Replacing, plus an exposed
  `Default`, lets a module do either.
- **Fail closed.** An error is a denial. A panic is not recovered.
- **Not authorization.** The password recheck, the plugin manifest role lists,
  the key route allowlist and the last-owner rules stay where they are.

## Consequences

- The `Subject`, `Object` and `Action` shapes and the catalogue strings become
  a public contract; changing or removing one is a breaking change.
- Later changes move the admin routes, the REST routes, the MCP tools and the
  inline checks onto `Can`, one surface at a time, each with a test that the
  default answers do not change.
- A module can narrow or widen what a role may do without touching core.
