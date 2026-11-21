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
// Templates are pages/<name>.html files in Page.Templates. Each starts with
// {{template "layouts/shell" .}} and defines "main". They are parsed like
// core's own pages and may use only core's hm/* partials and CSS classes; the
// handler's data is available as .Data. Context.Render(status, name, title,
// data) renders pages/<name>.html. Use the designcheck subpackage in a test to
// check the templates against core's design rules.
//
// A handler that returns an error or panics, a handler that returns nil
// without rendering, redirecting or calling Error, an unknown page and a template
// that fails to parse or execute all render core's error page with status 500.
//
// # Banners
//
// A BannerFunc returns banners that owners see at the top of every admin
// page. Kind is "danger", "warning" or "info". Banners cannot be dismissed.
// Href is relative to the admin prefix and starts with "/". An error, a panic,
// an invalid banner or a slow provider shows nothing from that provider and
// never blocks the page.
//
// Context's method set is frozen: modules may fake it in their own tests, so
// new capabilities arrive as new optional interfaces or functions, never as new
// methods on Context in a minor release.
//
// The types here are aliases of core's internal types. The package is part of
// core's public API: see docs/PLUGIN_API.md for the compatibility rules.
package adminpages
