package sdk

import (
	"maps"
	"slices"
)

// Permission names a host capability a plugin may be granted. A plugin lists
// the ones it needs in plugin.json; the owner grants them (a subset is fine);
// a host function called without its grant fails with ErrCapabilityDenied and
// the plugin carries on.
type Permission = string

// The capability set. Names are part of the SDK's compatibility surface.
const (
	PermEntitiesRead  Permission = "entities.read"
	PermTermsRead     Permission = "terms.read"
	PermMenusRead     Permission = "menus.read"
	PermMediaRead     Permission = "media.read"
	PermRedirectsRead Permission = "redirects.read"
	PermFormsRead     Permission = "forms.read"
	PermSettings      Permission = "settings"
	PermLog           Permission = "log"
	PermMailSend      Permission = "mail.send"
	PermHTTPFetch     Permission = "http.fetch"
	// PermHTTPFetchAny lets http.fetch call any public address (allowed_hosts
	// "*"), for plugins that call URLs the owner types in. It is separate from
	// PermHTTPFetch so the permission prompt can warn about it, and an update
	// that newly asks for it is reviewed again.
	PermHTTPFetchAny Permission = "http.fetch.any"
	// PermPackagesInspect lets a plugin ask core to download an https package
	// and report what it contains and whether its signature holds (SDK 0.21.0).
	PermPackagesInspect Permission = "packages.inspect"
	PermKV              Permission = "kv"
	PermDB              Permission = "db"
	// PermBuilderData lets the plugin answer builder.data requests for the
	// builder_sources it declares. Without the grant the filter is not
	// registered.
	PermBuilderData Permission = "builder.data"
	// PermEntitiesFilter (SDK 0.23.0) lets the plugin's entity.before_save
	// filter run. It has no host function: without the grant the filter is not
	// registered.
	PermEntitiesFilter Permission = "entities.filter"
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

	// PermUICamera (SDK 0.22.2) is the owner's grant for the camera on the
	// plugin's admin page. A manifest asks for it with ui.allow ["camera"],
	// never in permissions (it is not in Permissions), and the owner ticks it
	// separately. It is stored with the other grants.
	PermUICamera Permission = "ui.allow:camera"

	// PermCommentsRead lets a plugin read approved comments, hear about
	// comment.created|approved|spam|deleted and register comment.check. From
	// SDK 0.27 it also receives commenter_key on comment payloads.
	PermCommentsRead Permission = "comments.read"

	// PermClientKey (SDK 0.27.0) delivers per-plugin pseudonymous keys of the
	// visitor's address and network (addr_key, net_key on PageContext and
	// HTTPRequest; addr_key, area_key on CommentCheck), never the address.
	PermClientKey Permission = "client.key"

	// PermEntitiesWriteOwn (SDK 0.27.0) lets entities.create|update|delete write
	// entries of the one schema the manifest declares in entity_schema; they are
	// published without review. A manifest asking for it must declare entity_schema.
	PermEntitiesWriteOwn Permission = "entities.write.own"

	// PermCommentsModerate (SDK 0.27.0) lets comments.mark_dead|restore|set_rank|
	// set_viewer act on comments of the plugin's entity_schema only, and makes
	// include_dead count on reads. There is no comment create function.
	PermCommentsModerate Permission = "comments.moderate"
)

// Permissions lists every capability, in a stable order.
var Permissions = []Permission{
	PermEntitiesRead, PermTermsRead, PermMenusRead, PermMediaRead,
	PermRedirectsRead, PermFormsRead,
	PermSettings, PermLog, PermMailSend, PermHTTPFetch, PermHTTPFetchAny, PermKV, PermDB,
	PermBuilderData, PermBuilderIsland, PermSiteScripts,
	PermPackagesInspect, PermEntitiesFilter, PermCommentsRead,
	PermClientKey, PermEntitiesWriteOwn, PermCommentsModerate,
}

// ValidPermission reports whether p is a known capability.
func ValidPermission(p Permission) bool { return slices.Contains(Permissions, p) }

// ValidGrant reports whether p may be stored as a grant: a capability or
// PermUICamera.
func ValidGrant(p Permission) bool { return ValidPermission(p) || p == PermUICamera }

// removedPermissions are the write permissions that went away in SDK 0.17.0,
// each with the changeset function that replaces it. Content is written by a
// run through a changeset the owner reviews, never directly.
var removedPermissions = map[Permission]string{
	"entities.write":  FnChangesetEntityUpsert,
	"terms.write":     FnChangesetTermUpsert,
	"menus.write":     FnChangesetMenuUpsert,
	"media.write":     FnChangesetMediaAttach,
	"redirects.write": FnChangesetRedirectUpsert,
}

// RemovedPermission reports whether p is one of the write permissions SDK
// 0.17.0 removed, as opposed to a name that was never valid.
func RemovedPermission(p Permission) bool {
	_, gone := removedPermissions[p]
	return gone
}

// RemovedPermissions lists every permission this SDK refuses as removed,
// sorted. A release record carries the list so an updater can tell which
// removals a target release adds.
func RemovedPermissions() []Permission {
	return slices.Sorted(maps.Keys(removedPermissions))
}
