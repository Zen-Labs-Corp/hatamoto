package admin

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/permtext"
)

// ErrPluginNotFound, ErrBadGrant and ErrCommunityGrant are what PluginAdmin.Grant answers.
var (
	ErrPluginNotFound = errors.New("plugin not found")
	ErrBadGrant       = errors.New("permission not requested by the plugin")
	// ErrCommunityGrant is a permission a community plugin may not be given.
	ErrCommunityGrant = errors.New("permission not allowed for a community plugin")
)

func (a *Admin) mountPlugins(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	both := authed.Group("", a.RequireRole("owner", "editor"))
	a.mountMoreSettings(owner)
	owner.HandleFunc("GET /plugins", a.handlePlugins)
	a.mountPluginInstall(owner)
	a.mountPluginBrowse(owner)
	a.mountPluginSecurity(owner)
	a.mountPluginUIScreens(authed)
	// "go" is a literal segment: it wins over {name} in the mux.
	both.HandleFunc("GET /plugins/go", a.handlePluginsGo)
	both.HandleFunc("GET /plugins/{name}", a.handlePluginRedirect)
	both.HandleFunc("GET /plugins/{name}/settings", a.handlePluginSettings)
	owner.HandleFunc("POST /plugins/{name}/settings", a.handlePluginSettingsSave)
	owner.HandleFunc("GET /plugins/{name}/permissions", a.handlePluginPermissions)
	owner.HandleFunc("POST /plugins/{name}/grant", a.handlePluginGrant)
}

// pluginURL is the admin path of a plugin's page ("settings", "permissions").
func (a *Admin) pluginURL(name, page string) string {
	return a.adminPath + "/plugins/" + url.PathEscape(name) + "/" + page
}

type permRow struct {
	Name, Help string
	Granted    bool
	// Refused rows are never allowed for this plugin: unticked and disabled.
	Refused bool
}

// legacyWebhooksPlugin is the former first-party webhooks plugin. Webhooks are core now
// (Settings > Webhooks); a row that is still installed gets a notice and a Remove link.
const legacyWebhooksPlugin = "webhooks"

// pluginRow is one line of the plugins list.
type pluginRow struct {
	Name, Description, Version, Href string
	State                            pluginState
	// Legacy marks the old webhooks plugin: "imported" for the project's own signed
	// package whose subscriptions core holds, "pending" for one where that is not
	// proven (no Remove), "other" for any other package of that name. RemoveHref is
	// the owner-only uninstall confirmation. Both are empty on every other row.
	Legacy, RemoveHref string
	// ScriptsBlocked: the plugin's browser JavaScript is stopped until the
	// owner grants site.scripts. AsksScripts says whether its manifest asks.
	ScriptsBlocked, AsksScripts bool
	// SectionsStopped: some of its sections or partials are not shown for the
	// same reason.
	SectionsStopped bool
	// Update is the newer version the registry offers; "" when none, or when the
	// row takes no update (legacy, blocked, uninstalling, not a package).
	Update string
	// UpdateNew are the permissions that update adds. With none the row has a
	// one-click Update; with some it has "Review update" into the permission prompt.
	UpdateNew []string
	// UpToDate: the registry lists this plugin and offers nothing newer.
	UpToDate bool
	// updatable: takesUpdate said yes, so the row may show update state.
	updatable bool
}

// CanUpdate is the one-click state: a newer version that adds no permissions.
func (r pluginRow) CanUpdate() bool { return r.Update != "" && len(r.UpdateNew) == 0 }

// takesUpdate says whether the list offers an update for p at all: only an
// installed package that policy lets run and that is not on its way out. The
// old webhooks plugin is core's now and never updated.
func takesUpdate(p PluginInfo) bool {
	return p.Package != nil && p.Name != legacyWebhooksPlugin && p.Package.Blocked == "" && !p.Package.Uninstalling
}

type pluginsPage struct {
	Plugins []pluginRow
	// UpdateAll is how many rows have a one-click update; the header offers
	// "Update all" when it is more than zero.
	UpdateAll int
	// Actions are the header's upload (opens the dialog) and registry links.
	Actions []ui.Button
	// Available are registry plugins that are not installed yet.
	Available     []RegistryEntry
	RegistryErr   string
	RegistryHost  string
	RegistryOff   bool
	CanInstall    bool
	AllowUnsigned bool
}

// pluginHead is what the plugin pages share: the plugin, its status and the
// Settings/Permissions tabs.
type pluginHead struct {
	Plugin PluginInfo
	State  pluginState
	Tabs   []ui.Tab
}

func (a *Admin) newPluginHead(p PluginInfo, tab string) pluginHead {
	return pluginHead{Plugin: p, State: pluginStatus(p), Tabs: []ui.Tab{
		{Label: "Settings", Href: a.pluginURL(p.Name, "settings"), Active: tab == "settings"},
		{Label: "Permissions", Href: a.pluginURL(p.Name, "permissions"), Active: tab == "permissions"},
	}}
}

func (a *Admin) pluginAdmin(w http.ResponseWriter, r *http.Request) (PluginAdmin, bool) {
	if a.deps.Growth.Plugins == nil {
		a.unavailable(w, r, "Plugins")
		return nil, false
	}
	return a.deps.Growth.Plugins, true
}

func (a *Admin) handlePlugins(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	d := pluginsPage{}
	for _, p := range pa.Plugins() {
		row := pluginRow{Name: p.Name, Description: p.Description, Version: p.Version, Href: a.pluginURL(p.Name, "settings"), State: pluginStatus(p)}
		if p.Name == legacyWebhooksPlugin {
			row.Legacy, row.RemoveHref = "other", a.pluginURL(p.Name, "uninstall")
			if p.Package != nil && p.Package.FirstParty {
				// Remove is offered only once core holds the subscriptions: removing the
				// plugin earlier would strand them (boot never imports a plugin with no package row).
				row.Legacy = "imported"
				if !p.Package.WebhooksImported {
					row.Legacy, row.RemoveHref = "pending", ""
				}
			}
		}
		if p.Package != nil {
			row.ScriptsBlocked, row.AsksScripts = p.Package.ScriptsBlocked, p.Package.AsksScripts
			row.SectionsStopped = len(p.Package.MarkupStopped) > 0
		}
		if takesUpdate(p) {
			row.updatable = true
			row.Update, row.UpdateNew = p.Package.Update, p.Package.UpdateNew
		}
		d.Plugins = append(d.Plugins, row)
	}
	if pk := a.deps.Growth.Packages; pk != nil {
		d.CanInstall, d.AllowUnsigned = true, pk.AllowUnsigned(r.Context())
		d.Actions = []ui.Button{{Label: "Upload package", Href: a.adminPath + "/plugins/upload", Variant: "primary", Icon: "upload", Attrs: map[string]string{"data-hm-open": "upload-pkg"}}, {Label: "Browse registry", Href: "#registry", Icon: "search"}}
		reg := pk.Registry(r.Context())
		d.RegistryErr, d.RegistryOff, d.RegistryHost = reg.Err, reg.Off, reg.Host
		listed := map[string]bool{}
		for _, e := range reg.Entries {
			if e.Kind != "plugin" {
				continue
			}
			if !e.Installed {
				d.Available = append(d.Available, e)
			} else if !e.Update {
				listed[e.Name] = true
			}
		}
		for i := range d.Plugins {
			row := &d.Plugins[i]
			row.UpToDate = row.updatable && listed[row.Name] && row.Update == "" && row.Legacy == ""
			if row.CanUpdate() {
				d.UpdateAll++
			}
		}
	}
	v := a.NewView(w, r, "Plugins")
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/plugins", v)
}

// pluginState is a plugin's status as the list, the plugin page and the
// subnav show it.
type pluginState struct {
	Badge  ui.Badge
	Active bool
	// Marker is "" when active, "Off" (disabled, uninstalling) or "Failed"
	// (failed, blocked): the subnav's muted note.
	Marker string
}

// pluginStatus derives the state once. Precedence: uninstalling, blocked,
// turned off, loaded, widget-only, failed.
func pluginStatus(p PluginInfo) pluginState {
	switch {
	case p.Package != nil && p.Package.Uninstalling:
		return pluginState{Badge: ui.Badge{Label: "Uninstalling", Kind: "danger"}, Marker: "Off"}
	case p.Package != nil && p.Package.Blocked != "":
		return pluginState{Badge: ui.Badge{Label: "Blocked", Kind: "danger"}, Marker: "Failed"}
	case p.Package != nil && !p.Package.Enabled:
		return pluginState{Badge: ui.Badge{Label: "Disabled", Kind: "neutral"}, Marker: "Off"}
	case p.Loaded:
		return pluginState{Badge: ui.Badge{Label: "Active", Kind: "success"}, Active: true}
	case p.Package != nil && p.Package.WidgetOnly:
		// No wasm to load: its widgets are registered, which is all it does.
		return pluginState{Badge: ui.Badge{Label: "Widgets", Kind: "neutral"}, Active: true}
	}
	return pluginState{Badge: ui.Badge{Label: "Failed", Kind: "danger"}, Marker: "Failed"}
}

func (a *Admin) findPlugin(pa PluginAdmin, name string) (PluginInfo, bool) {
	for _, p := range pa.Plugins() {
		if p.Name == name {
			return p, true
		}
	}
	return PluginInfo{}, false
}

// handlePluginGrant replaces the plugin's granted permissions with the ticked
// ones (only permissions its manifest asks for are accepted).
func (a *Admin) handlePluginGrant(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if _, found := a.findPlugin(pa, name); !found {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	perms := r.PostForm["permission"]
	if perms == nil {
		perms = []string{}
	}
	switch err := pa.Grant(r.Context(), name, perms); {
	case errors.Is(err, ErrCommunityGrant):
		a.ErrorBack(w, r, http.StatusBadRequest, "Permission not allowed",
			"Plugins from the community registry cannot run scripts on your site or add builder islands. Save the permissions again without it.",
			a.pluginURL(name, "permissions"), "Back to permissions")
		return
	case errors.Is(err, ErrBadGrant):
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That plugin does not ask for one of those permissions.")
		return
	case errors.Is(err, ErrPluginNotFound):
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed.", a.adminPath+"/plugins", "Back to plugins")
		return
	case err != nil:
		a.logger().Error("grant plugin permissions failed", "plugin", name, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The permissions could not be saved.")
		return
	}
	a.SetFlash(w, r, "success", "Permissions saved for "+name+".")
	http.Redirect(w, r, a.pluginURL(name, "permissions"), http.StatusSeeOther)
}

// ---- plugin page (settings tab) ----

// pluginPageData is the Settings tab. An editor gets only the UI frame: Owner
// is false and the form, header actions and tabs are left out.
type pluginPageData struct {
	pluginHead
	Owner     bool
	HasSchema bool
	Form      template.HTML
	UI        *pluginUIData
	Inactive  bool
	Summary   []ui.FieldError
}

// renderPlugin renders the Settings tab of p for the signed-in role. values
// are the form's current values (read only for an owner and a plugin with a
// settings schema).
func (a *Admin) renderPlugin(w http.ResponseWriter, r *http.Request, pa PluginAdmin, status int, p PluginInfo, values map[string]any, errs schemaform.Errors, openPath string) {
	role := userFrom(r.Context()).Role
	d := pluginPageData{pluginHead: a.newPluginHead(p, "settings"), Owner: role == "owner", UI: a.pluginUIFor(r, p, role), Inactive: !p.Loaded && (p.Package == nil || !p.Package.WidgetOnly)}
	title := p.Name + " settings"
	if !d.Owner && d.UI != nil {
		title = d.UI.Title
	}
	if d.Owner && len(p.SettingsSchema) > 0 {
		secrets, err := pa.StoredSecrets(r.Context(), p.Name)
		if err != nil {
			a.logger().Error("load plugin secrets state failed", "plugin", p.Name, "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
			return
		}
		form, err := schemaform.Render(p.SettingsSchema, values, schemaform.Options{Errors: errs, OpenPath: openPath, Secrets: secrets})
		if err != nil {
			a.logger().Error("render plugin settings form failed", "plugin", p.Name, "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings form of this plugin could not be built.")
			return
		}
		d.HasSchema, d.Form = true, form
	}
	for path, msgs := range errs {
		for _, m := range msgs {
			d.Summary = append(d.Summary, ui.FieldError{ID: strings.ReplaceAll(path, ".", "-"), Label: schemaform.Humanize(path[strings.LastIndex(path, ".")+1:]), Message: m})
		}
	}
	v := a.NewView(w, r, title)
	if d.Owner {
		v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: p.Name}}
	} else {
		v.Crumbs = []Crumb{{Label: title}}
	}
	v.Data = d
	a.Render(w, r, status, "plugins/plugin", v)
}

// handlePluginsGo is the no-JS plugin select: it sends the owner or editor to
// the chosen plugin (an empty value is the list). The target is built from an
// installed plugin's name, never from the query, so an unknown value lands on
// the role's home.
func (a *Admin) handlePluginsGo(w http.ResponseWriter, r *http.Request) {
	role := userFrom(r.Context()).Role
	to := a.adminPath
	if role == "owner" {
		to = a.adminPath + "/plugins"
	}
	if pa := a.deps.Growth.Plugins; pa != nil {
		if p, found := a.findPlugin(pa, r.URL.Query().Get("plugin")); found && (role == "owner" || uiAllows(p, role)) {
			to = a.pluginURL(p.Name, "settings")
		}
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// handlePluginRedirect sends /admin/plugins/{name} to the plugin's Settings tab.
func (a *Admin) handlePluginRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, a.pluginURL(r.PathValue("name"), "settings"), http.StatusFound)
}

// pluginPermsData is the Permissions tab.
type pluginPermsData struct {
	pluginHead
	Perms        []permRow
	GrantedCount int
	// Askable counts the permissions that can be ticked (Perms minus Refused).
	Askable   int
	HooksText string
	// Granted is the "N of M allowed" gauge; nil when the plugin asks for nothing.
	Granted *ui.Progress
}

func (a *Admin) handlePluginPermissions(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	p, found := a.findPlugin(pa, r.PathValue("name"))
	if !found {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	d := pluginPermsData{pluginHead: a.newPluginHead(p, "permissions"), HooksText: strings.Join(p.Hooks, ", ")}
	for _, perm := range p.Permissions {
		refused := slices.Contains(p.Refused, perm)
		g := !refused && slices.Contains(p.Granted, perm)
		d.Perms = append(d.Perms, permRow{Name: perm, Help: permtext.Help(perm), Granted: g, Refused: refused})
		switch {
		case refused:
		case g:
			d.GrantedCount++
			d.Askable++
		default:
			d.Askable++
		}
	}
	if n := d.Askable; n > 0 {
		d.Granted = &ui.Progress{Label: "Permissions allowed", Kind: "neutral", Value: fmt.Sprintf("%d of %d", d.GrantedCount, n), Pct: float64(d.GrantedCount) / float64(n) * 100}
	}
	v := a.NewView(w, r, p.Name+" permissions")
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: p.Name, Href: a.pluginURL(p.Name, "settings")}, {Label: "Permissions"}}
	v.Data = d
	a.Render(w, r, http.StatusOK, "plugins/permissions", v)
}

func (a *Admin) pluginWithSettings(w http.ResponseWriter, r *http.Request) (PluginAdmin, PluginInfo, bool) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return nil, PluginInfo{}, false
	}
	p, found := a.findPlugin(pa, r.PathValue("name"))
	if !found || len(p.SettingsSchema) == 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "No settings", "That plugin is not installed or has no settings.", a.adminPath+"/plugins", "Back to plugins")
		return nil, PluginInfo{}, false
	}
	return pa, p, true
}

// handlePluginSettings shows a plugin's Settings tab: for the owner every
// installed plugin, for an editor only a plugin whose UI admits them (anything
// else answers like an unknown plugin).
func (a *Admin) handlePluginSettings(w http.ResponseWriter, r *http.Request) {
	pa, ok := a.pluginAdmin(w, r)
	if !ok {
		return
	}
	role := userFrom(r.Context()).Role
	p, found := a.findPlugin(pa, r.PathValue("name"))
	if !found || (role != "owner" && !uiAllows(p, role)) {
		if role == "owner" {
			a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed, or it is not available to you.", a.adminPath+"/plugins", "Back to plugins")
			return
		}
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed, or it is not available to you.", a.adminPath, "Back to the admin")
		return
	}
	var values map[string]any
	if role == "owner" && len(p.SettingsSchema) > 0 {
		var err error
		if values, err = pa.Settings(r.Context(), p.Name); err != nil {
			a.logger().Error("load plugin settings failed", "plugin", p.Name, "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
			return
		}
	}
	a.renderPlugin(w, r, pa, http.StatusOK, p, values, nil, "")
}

func (a *Admin) handlePluginSettingsSave(w http.ResponseWriter, r *http.Request) {
	pa, p, ok := a.pluginWithSettings(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	prev, err := pa.Settings(r.Context(), p.Name)
	if err != nil {
		a.logger().Error("load plugin settings failed", "plugin", p.Name, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	values, errs := schemaform.Decode(p.SettingsSchema, r.PostForm, prev, schemaform.Options{})
	// A ticked "clear" box removes a write-only value (null); an empty field
	// keeps it. Typed secrets are never sent back to the page.
	if root, perr := schemaform.ParseSchema(p.SettingsSchema); perr == nil {
		for _, prop := range root.Props {
			if prop.Node.WriteOnly && r.PostForm.Get(prop.Name+"__clear") == "1" {
				values[prop.Name] = nil
			}
		}
	}
	if schemaform.Structural(r.PostForm) {
		a.renderPlugin(w, r, pa, http.StatusOK, p, values, nil, "")
		return
	}
	// No separate schema check here: write-only secrets are posted empty, and
	// SaveSettings restores them from the stored settings before it validates.
	if len(errs) == 0 {
		errs, err = pa.SaveSettings(r.Context(), p.Name, values)
		if err != nil {
			a.logger().Error("save plugin settings failed", "plugin", p.Name, "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		a.renderPlugin(w, r, pa, http.StatusUnprocessableEntity, p, values, errs, "")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Settings saved for "+p.Name+".")
	http.Redirect(w, r, a.pluginURL(p.Name, "settings"), http.StatusSeeOther)
}
