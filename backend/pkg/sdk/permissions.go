package sdk

import "slices"

// Permission names a host capability a plugin may be granted. A plugin lists
// the ones it needs in plugin.json; the owner grants them (a subset is fine);
// a host function called without its grant fails with ErrCapabilityDenied and
// the plugin carries on.
type Permission = string

// The capability set. Names are part of the SDK's compatibility surface.
const (
	PermEntitiesRead   Permission = "entities.read"
	PermEntitiesWrite  Permission = "entities.write"
	PermTermsRead      Permission = "terms.read"
	PermTermsWrite     Permission = "terms.write"
	PermMenusRead      Permission = "menus.read"
	PermMenusWrite     Permission = "menus.write"
	PermMediaRead      Permission = "media.read"
	PermMediaWrite     Permission = "media.write"
	PermRedirectsRead  Permission = "redirects.read"
	PermRedirectsWrite Permission = "redirects.write"
	PermFormsRead      Permission = "forms.read"
	PermSettings       Permission = "settings"
	PermLog            Permission = "log"
	PermMailSend       Permission = "mail.send"
	PermHTTPFetch      Permission = "http.fetch"
	// PermHTTPFetchAny lets http.fetch call any public address (allowed_hosts
	// "*"), for plugins that call URLs the owner types in. It is separate from
	// PermHTTPFetch so the permission prompt can warn about it, and an update
	// that newly asks for it is reviewed again.
	PermHTTPFetchAny Permission = "http.fetch.any"
	PermKV           Permission = "kv"
	PermDB           Permission = "db"
	// PermBuilderData lets the plugin answer builder.data requests for the
	// builder_sources it declares. Without the grant the filter is not
	// registered.
	PermBuilderData Permission = "builder.data"
	// PermBuilderIsland is required of a package whose widgets ship an
	// island.js. The script runs on the site's public pages with the
	// visitor's privileges, so the owner grants it explicitly; without the
	// grant the widget still renders but its island is not served.
	PermBuilderIsland Permission = "builder.island"
	// PermSiteScripts is required for a plugin's browser JavaScript to run on
	// the public site: its assets/plugin.js is linked on pages, and the
	// script-src tags of its render.head filter are emitted, only while
	// granted. That script runs with the visitor's privileges, including a
	// signed-in admin's, so the owner grants it explicitly. plugin.css needs
	// no grant.
	PermSiteScripts Permission = "site.scripts"
)

// Permissions lists every capability, in a stable order.
var Permissions = []Permission{
	PermEntitiesRead, PermEntitiesWrite, PermTermsRead, PermTermsWrite,
	PermMenusRead, PermMenusWrite, PermMediaRead, PermMediaWrite,
	PermRedirectsRead, PermRedirectsWrite, PermFormsRead,
	PermSettings, PermLog, PermMailSend, PermHTTPFetch, PermHTTPFetchAny, PermKV, PermDB,
	PermBuilderData, PermBuilderIsland, PermSiteScripts,
}

// ValidPermission reports whether p is a known capability.
func ValidPermission(p Permission) bool { return slices.Contains(Permissions, p) }
