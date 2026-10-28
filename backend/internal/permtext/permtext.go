// Package permtext holds the plain-English wording for plugin permissions,
// shared by the admin install prompt, the public plugin directory and the
// static registry page. It is host UI copy, not plugin API.
package permtext

import "hatamoto.dev/sdk"

// Text describes one permission for a human reader.
type Text struct {
	// Help is the full sentence shown next to the permission name.
	Help string
	// Short is the badge label of a risky permission; empty otherwise.
	Short string
	// Warn marks permissions that deserve a visible warning.
	Warn bool
}

var texts = map[string]Text{
	sdk.PermEntitiesRead:   {Help: "Read pages, posts and other content"},
	sdk.PermEntitiesWrite:  {Help: "Create and change content"},
	sdk.PermTermsRead:      {Help: "Read categories and tags"},
	sdk.PermTermsWrite:     {Help: "Create and change categories and tags"},
	sdk.PermMenusRead:      {Help: "Read menus"},
	sdk.PermMenusWrite:     {Help: "Change menus"},
	sdk.PermMediaRead:      {Help: "Read the media library"},
	sdk.PermMediaWrite:     {Help: "Upload and change media"},
	sdk.PermRedirectsRead:  {Help: "Read redirects"},
	sdk.PermRedirectsWrite: {Help: "Create and change redirects"},
	sdk.PermFormsRead:      {Help: "Hear about form submissions"},
	sdk.PermSettings:       {Help: "Store its own settings"},
	sdk.PermLog:            {Help: "Write to the server log"},
	sdk.PermMailSend:       {Help: "Send email"},
	sdk.PermHTTPFetch:      {Help: "Call other web addresses it lists"},
	sdk.PermHTTPFetchAny: {
		Help:  "WARNING: send requests to ANY public web address, including addresses typed into its settings. Treat it as a way out to the internet from this server.",
		Short: "Any web address.", Warn: true,
	},
	sdk.PermKV:          {Help: "Keep small values of its own"},
	sdk.PermDB:          {Help: "Keep its own data tables"},
	sdk.PermBuilderData: {Help: "Offer data sources to the page builder"},
	sdk.PermSiteScripts: {
		Help:  "Runs its JavaScript on every public page, with full access to the site, including signed-in admins. Without it the plugin's script is not loaded.",
		Short: "JavaScript on every public page.", Warn: true,
	},
	sdk.PermBuilderIsland: {
		Help:  "WARNING: runs JavaScript on your site's pages, with the same access as the pages themselves. Without it the widgets still render, but their script is not served.",
		Short: "Runs JavaScript on your site's pages.", Warn: true,
	},
}

// Lookup returns the wording for a permission. A name this build does not
// know (a registry can be newer than the host) falls back to the raw name.
func Lookup(name string) Text {
	if t, ok := texts[name]; ok {
		return t
	}
	return Text{Help: name}
}

// Help returns the full sentence for a known permission and "" for a name this
// build does not know, so the admin shows the bare name instead of repeating it.
func Help(name string) string {
	return texts[name].Help
}
