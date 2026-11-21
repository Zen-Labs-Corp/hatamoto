# ADR 0006: Module extension points

Status: accepted (2026-11-21)

## Context

A compiled-in module is a Go program that wraps `app.Main` and adds behaviour
to a core build. It registers what it adds from an `init` function. Core
freezes the registrations when `app.Main` starts, so a registration after that
point panics instead of changing a running server. A registration that is
invalid (bad ID, duplicate, nil handler) panics at registration, so a mistake
shows at boot and not on a visitor's request.

Each extension point is a small public package, and those packages are a
contract in the same way as the plugin API: a breaking change needs a major
release. The public package is a thin facade over an `internal/...state`
package that holds the registry; core's own code reads the registry, and
modules can only write to it through the facade. A module never gets a handle
on core internals: it gets an interface (for example `Context`) that only core
implements. Modules still write their own fakes of it in unit tests, so the
method set of `Context` is frozen: new capabilities arrive as new optional
interfaces or functions, never as new methods on `Context` in a minor release.

A module also fails closed. Whatever a module does wrong at run time (an error,
a panic, a broken template, a slow provider) must end in core's own error page
or in the module's piece being left out, never in a plain 500 or a half page.

Each extension point has its own section below; later parts append theirs.

## Module admin pages

### Decision

The public package `hatamoto.dev/core/adminpages` lets a module add owner-only
pages to the Settings area of the admin and show banners in the admin shell.
`adminpages.Register(Page)` and `adminpages.RegisterBanner(name, fn)` write to
`internal/adminpagestate`.

1. **Own package over a state package.** The facade holds only type aliases and
   two functions. The registry (mutex, frozen flag, `Reset` for tests) lives in
   `internal/adminpagestate`, copying how `edition` sits over
   `internal/editionstate`. `app.Main` freezes it next to the edition registry.
2. **Per Admin, through `Deps`.** The server copies the frozen registry into
   `admin.Deps`. Each `admin.New`, including the rebuild after an admin address
   change, derives its own Settings area from it. The package-level Settings
   registry is never changed, so tests that run in parallel and the default
   registry stay valid, and an install with no module renders the same bytes as
   before.
3. **Namespaced URLs and IDs.** A page with ID `x` is served at
   `<admin>/settings/modules/x`; its section and tab ID is `module-x`. Core can
   add any `/settings/<name>` later without colliding with a module, so only
   two modules can collide, and that is checked at registration.
4. **Owners only.** Every module route is mounted on the owner group, so it
   gets the session, role, CSRF, two-factor and header guards of core's own
   pages. A route marked `Sudo` also needs a recent password confirmation, and
   `Context.CheckDanger` checks a typed phrase on the server. There is no role
   field in this version.
5. **Placement.** Pages are appended after core's sections in the "System"
   group, in registration order. An empty or unknown icon becomes `puzzle`,
   because an unknown icon is a template error that would break every Settings
   page.
6. **Templates and core partials only.** A module ships an `fs.FS` of
   `pages/<name>.html`. The pages are parsed like core's, from core's layouts
   and `hm/*` partials, with core's template functions. There is no module CSS
   or JavaScript. Radio cards and every other component are the ones core
   already has.
7. **Fail closed.** A handler error or panic, an unknown page and a template
   that does not parse or execute render core's error page with status 500. A
   banner provider that errors, panics, returns an invalid banner or takes more
   than 300 ms shows nothing, and the page renders without it.
8. **Banners in the shell.** Banners (`danger`, `warning`, `info`) show to owners
   in the notices block above every page. They cannot be dismissed. A link is
   relative to the admin prefix; core joins the prefix and drops a link that is
   not a path inside the admin.
9. **A public design check.** `hatamoto.dev/core/adminpages/designcheck.Check`
   runs core's template rules (UI-01, UI-02, UI-03, UI-05, UI-07, UI-08, UI-09,
   UI-10) and one rule for modules (MOD-01: no bespoke class, no inline style) on
   a module's templates. It sits in its own subpackage, so the lint code is not
   linked into a binary that does not import it. Core's allowlist does not apply.

### Alternatives rejected

- **Changing the global Settings registry at registration.** It is shared by
  every Admin and every test in the process, so one module would change the
  output of unrelated Admins and of parallel tests.
- **A Go component tree instead of templates.** It would make every `hm/*`
  partial a public Go API. Templates keep one source of truth for markup and
  let the design check read what is rendered.
- **Module assets (CSS and JavaScript).** They would need their own cache,
  integrity and content-security handling, and let a module drift from the
  admin's look. A module that needs a component core lacks asks core for it.

### Consequences

- The public API in `hatamoto.dev/core/adminpages` is frozen at the next major
  release after it ships. Fields can be added to `Page`, `Route` and `Banner`
  so non-owner pages, assets or a placement choice can come later without a
  break. The method set of `Context` is frozen; new capabilities are new
  optional interfaces or functions.
- A module's templates are checked in the module's own tests, not at runtime.
