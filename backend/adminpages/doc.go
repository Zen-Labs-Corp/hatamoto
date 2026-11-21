// Package adminpages lets a compiled-in module add owner-only pages to the
// Settings area of the admin and show banners in the admin shell.
//
// A module that wraps app.Main registers from an init function. Registration
// is frozen when app.Main starts; later calls panic.
//
// # Pages
//
// A page appears in the Settings navigation under "System" and is served at
// <admin>/settings/modules/<ID>, with routes relative to that URL. Only
// owners can reach it: other roles get core's "Not allowed" page. Core
// mounts every route behind its own session, CSRF and two-factor checks, so
// a POST needs the token that {{template "hm/csrf" $}} renders in a form. A
// Sudo route also needs a recently confirmed password.
//
// GET routes must not change state: core lets a navigation GET through without
// a CSRF token, and the admin may prerender links, so use POST to change
// anything. Sudo only asks for the password. A typed-phrase confirmation is
// enforced only when the handler calls Context.CheckDanger and refuses to act
// when it returns false. Set DangerConfirm.CSRF from Context.CSRF(), or the
// confirm form posts without a token and is refused.
//
// Templates are pages/<name>.html files in Page.Templates. Each starts with
// {{template "layouts/shell" .}} and defines "main". They are parsed like
// core's own pages and may use only core's hm/* partials and CSS classes; the
// handler's data is available as .Data. Context.Render(status, name, title,
// data) renders pages/<name>.html. Use the designcheck subpackage in a test to
// check the templates against core's design rules. Flash after a successful
// Render is lost, because the response is already written: call Flash before
// Redirect.
//
// A handler that returns an error or panics, a handler that returns nil
// without rendering, redirecting or calling Error, an unknown page and a template
// that fails to parse or execute all render core's error page with status 500.
//
// # Banners
//
// A BannerFunc returns banners that owners see at the top of every admin
// page. Kind is "danger", "warning" or "info". Banners cannot be dismissed.
// Set Label to link text that makes sense out of context (for example "Review
// export errors"), because several banners can show on one page. A provider
// must honour its ctx and return when it is done: core stops waiting after a
// short deadline but cannot stop the function.
// Href is relative to the admin prefix and starts with "/". An error, a panic,
// an invalid banner or a slow provider shows nothing from that provider and
// never blocks the page.
//
// Context's method set is frozen: modules may fake it in their own tests, so
// new capabilities arrive as new optional interfaces or functions, never as new
// methods on Context in a minor release.
//
// The types here are aliases of core's internal types. The package is part of
// core's public API: see docs/adr/0008-module-admin-pages.md for the compatibility rules.
package adminpages
