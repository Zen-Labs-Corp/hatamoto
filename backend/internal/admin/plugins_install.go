package admin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/permtext"
	"hatamoto.dev/sdk"
)

// maxUploadBytes bounds an uploaded package (the format's own limit).
const maxUploadBytes = 64 << 20

// mountPluginInstall registers the install, update, enable and uninstall
// routes of the Plugins page (owner only).
func (a *Admin) mountPluginInstall(owner *httpx.Group) {
	a.mountPluginUpdate(owner)
	owner.HandleFunc("POST /plugins/fetch", a.handlePluginFetch)
	owner.HandleFunc("GET /plugins/upload", a.handleUploadPage)
	owner.HandleFunc("POST /plugins/upload", a.handlePackageUpload)
	owner.HandleFunc("POST /plugins/install", a.handlePluginInstall)
	owner.HandleFunc("POST /theme/themes/install", a.handleThemeInstallConfirmed)
	owner.HandleFunc("POST /plugins/{name}/enable", a.handlePluginEnable)
	owner.HandleFunc("POST /plugins/{name}/disable", a.handlePluginEnable)
	owner.HandleFunc("GET /plugins/{name}/uninstall", a.handlePluginUninstallForm)
	owner.HandleFunc("POST /plugins/{name}/uninstall", a.handlePluginUninstall)
}

func (a *Admin) packageAdmin(w http.ResponseWriter, r *http.Request) (PackageAdmin, bool) {
	if a.deps.Growth.Packages == nil {
		a.unavailable(w, r, "Packages")
		return nil, false
	}
	return a.deps.Growth.Packages, true
}

// refused answers an install that failed: a Refusal is the owner's to read (422
// with the reason), anything else is a server error.
func (a *Admin) refused(w http.ResponseWriter, r *http.Request, err error, back, backLabel string) {
	var ref *Refusal
	if errors.As(err, &ref) {
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "Not installed", ref.Msg, back, backLabel)
		return
	}
	a.logger().Error("package operation failed", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The package could not be installed.")
}

// ---- the permission prompt ----

// promptStatement is one row of the "It will be able to" list.
type promptStatement struct {
	permtext.Statement
	Icon string
	New  bool // requested by an update and not granted before
}

// promptScripts is the one permission the owner must switch on: site.scripts
// is never granted by the install button alone.
type promptScripts struct{ On bool }

type promptData struct {
	Staged   string
	Sum      PackageSummary
	Can      []promptStatement
	Never    []promptStatement
	Grant    []string // posted as hidden permission fields: every listed permission except site.scripts and the refused ones
	Refused  string   // plain words for the requested permissions policy will not grant, "" when none
	Scripts  *promptScripts
	Unsigned bool
	Title    string     // "Install <Name> <version>?"
	Chips    []ui.Badge // the signature, first-party and replaced-version badges beside the title
}

// statementIcon is the glyph of a statement kind.
func statementIcon(k permtext.Kind) string {
	switch k {
	case permtext.KindReview:
		return "layers"
	case permtext.KindNo:
		return "x"
	}
	return "check"
}

func promptStatements(in []permtext.Statement, newTitles map[string]bool) []promptStatement {
	out := make([]promptStatement, len(in))
	for i, st := range in {
		out[i] = promptStatement{Statement: st, Icon: statementIcon(st.Kind), New: newTitles[st.Title]}
	}
	return out
}

// handlePluginFetch downloads a registry plugin and shows the permission prompt.
func (a *Admin) handlePluginFetch(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	data, err := pk.Fetch(r.Context(), "plugin", r.PostFormValue("name"))
	if err != nil {
		a.refused(w, r, err, a.adminPath+"/plugins", "Back to plugins")
		return
	}
	a.prompt(w, r, pk, data, "registry", true)
}

// handleUploadPage is the no-JS page of the Upload package dialog.
func (a *Admin) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	d := struct {
		AllowUnsigned bool
		Theme         bool
	}{pk.AllowUnsigned(r.Context()), r.URL.Query().Get("for") == "theme"}
	v := a.NewView(w, r, "Upload package")
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Upload package"}}
	if d.Theme { // the no-JS twin of the dialog on the Themes page
		v.Title = "Upload theme"
		v.Section, v.Tab = "themes", "themes"
		v.Crumbs = a.areaCrumbs("themes", Crumb{Label: "Upload theme"})
	}
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/upload", v)
}

// errNoUpload and errUploadTooLarge are what readUpload reports.
var (
	errNoUpload       = errors.New("no package file")
	errUploadTooLarge = errors.New("package file too large")
)

// maxUploadBody bounds a whole upload form: the package plus form overhead.
const maxUploadBody = maxUploadBytes + 1<<20

// limitUploadBody caps the body of the package upload form before anything
// reads it (the CSRF check reads the first field).
func (a *Admin) limitUploadBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == a.url(streamedUploadPath) {
			if r.ContentLength > maxUploadBody {
				http.Error(w, "A package can be at most 64 MB.", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
		}
		next.ServeHTTP(w, r)
	})
}

// readUpload streams the multipart body to its "package" file part (the token
// field before it was read by the CSRF check).
func readUpload(r *http.Request) ([]byte, error) {
	mr, err := uploadReader(r)
	if err != nil {
		return nil, errNoUpload
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return nil, errUploadTooLarge
			}
			return nil, errNoUpload
		}
		if part.FormName() != "package" || part.FileName() == "" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig), len(data) > maxUploadBytes:
			return nil, errUploadTooLarge
		case err != nil:
			return nil, errNoUpload
		}
		return data, nil
	}
}

// handlePackageUpload takes an uploaded .hmp: a plugin goes to the permission
// prompt; a theme is installed, after a confirmation page when it would
// replace an installed theme with an older one, another signer's, or one that
// loses sections the active theme's content uses.
func (a *Admin) handlePackageUpload(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	back, label := a.adminPath+"/plugins", "Back to plugins"
	data, err := readUpload(r)
	switch {
	case errors.Is(err, errUploadTooLarge):
		a.ErrorBack(w, r, http.StatusRequestEntityTooLarge, "File too large", "A package can be at most 64 MB.", back, label)
		return
	case err != nil:
		a.ErrorBack(w, r, http.StatusBadRequest, "No file", "Choose a .hmp package file to upload.", back, label)
		return
	}
	sum, err := pk.Inspect(r.Context(), data)
	if err != nil {
		a.refused(w, r, err, back, label)
		return
	}
	if sum.Kind == "theme" {
		a.installTheme(w, r, pk, data, "upload", sum)
		return
	}
	a.prompt(w, r, pk, data, "upload", false)
}

// installTheme installs a theme, or stages it behind the confirmation page.
func (a *Admin) installTheme(w http.ResponseWriter, r *http.Request, pk PackageAdmin, data []byte, source string, sum PackageSummary) {
	back, label := a.adminPath+"/theme/themes", "Back to themes"
	if sum.Problem != "" {
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "Not installed", sum.Problem, back, label)
		return
	}
	if sum.Confirm != nil {
		id, ok := a.staged.put(userFrom(r.Context()).ID, source, data)
		if !ok {
			a.ErrorBack(w, r, http.StatusTooManyRequests, "Too many pending installs", "Finish or cancel the installs you started, or wait a few minutes.", back, label)
			return
		}
		v := a.NewView(w, r, "Replace theme "+sum.Name)
		v.Crumbs = a.areaCrumbs("themes", Crumb{Label: sum.Name})
		v.Data = promptData{Staged: id, Sum: sum}
		a.Render(w, r, http.StatusOK, "settings/packageconfirm", v)
		return
	}
	if err := pk.Install(r.Context(), data, source, nil, false); err != nil {
		a.refused(w, r, err, back, label)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Theme "+sum.Name+" installed. Preview it, then activate it.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleThemeInstallConfirmed completes the theme confirmation page.
func (a *Admin) handleThemeInstallConfirmed(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	back, label := a.adminPath+"/theme/themes", "Back to themes"
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	st, found := a.staged.take(userFrom(r.Context()).ID, r.PostForm.Get("staged"))
	if !found {
		a.ErrorBack(w, r, http.StatusGone, "Install expired", "That install was started a while ago. Start it again.", back, label)
		return
	}
	if r.PostForm.Get("confirm") != "1" {
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "Tick the box to continue", "Replacing a theme this way needs your confirmation.", back, label)
		return
	}
	if err := pk.Install(r.Context(), st.data, st.source, nil, true); err != nil {
		a.refused(w, r, err, back, label)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Theme replaced. Preview it, then activate it.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *Admin) prompt(w http.ResponseWriter, r *http.Request, pk PackageAdmin, data []byte, source string, fromBrowse bool) {
	sum, err := pk.Inspect(r.Context(), data)
	if err != nil {
		a.refused(w, r, err, a.adminPath+"/plugins", "Back to plugins")
		return
	}
	id, ok := a.staged.put(userFrom(r.Context()).ID, source, data)
	if !ok {
		a.ErrorBack(w, r, http.StatusTooManyRequests, "Too many pending installs", "Finish or cancel the installs you started, or wait a few minutes.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	d := promptData{Staged: id, Sum: sum, Unsigned: !sum.Signed}
	var added []string
	perms := slices.DeleteFunc(slices.Clone(sum.Permissions), func(p string) bool { return slices.Contains(sum.Refused, p) })
	var refused []string
	for _, p := range sum.Refused {
		refused = append(refused, strings.TrimSuffix(permtext.Lookup(p).Short, "."))
	}
	d.Refused = strings.Join(refused, ", ")
	for _, p := range perms {
		if sum.Updating && !slices.Contains(sum.Granted, p) {
			added = append(added, p)
		}
	}
	newTitles := map[string]bool{}
	for _, st := range permtext.Group(added, false, false).Can {
		newTitles[st.Title] = true
	}
	g := permtext.Group(perms, sum.DeclaresRuns, sum.Proposes)
	d.Can, d.Never = promptStatements(g.Can, newTitles), promptStatements(g.Never, nil)
	// A package from another signer starts with nothing granted.
	reset := sum.Confirm != nil && (sum.Confirm.SignerChange || sum.Confirm.WidenedAccess)
	for _, p := range perms {
		if p == sdk.PermSiteScripts {
			// Never granted by the install button alone: a first install gets it
			// only by an explicit switch; an update keeps it only if it was granted.
			d.Scripts = &promptScripts{On: sum.Updating && slices.Contains(sum.Granted, p) && !reset}
			continue
		}
		d.Grant = append(d.Grant, p)
	}
	verb := "Install"
	if sum.Updating {
		verb = "Update"
	}
	d.Title = strings.TrimSpace(verb+" "+sum.Name+" "+sum.Version) + "?"
	if sum.Signed {
		chip := ui.Badge{Label: "Signed by " + sum.Signer, Kind: "success"}
		if sum.Problem != "" {
			chip = ui.Badge{Label: "Signed by " + sum.Signer, Plain: true}
		}
		d.Chips = append(d.Chips, chip)
	}
	if sum.FirstParty {
		d.Chips = append(d.Chips, ui.Badge{Label: "First-party", Plain: true, Icon: "star"})
	}
	if sum.Updating {
		d.Chips = append(d.Chips, ui.Badge{Label: "Replaces v" + sum.InstalledVersion, Plain: true})
	}
	v := a.NewView(w, r, verb+" "+sum.Name)
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: sum.Name}}
	if fromBrowse {
		v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: "Browse", Href: a.adminPath + "/plugins/browse"}, {Label: sum.Name}}
	}
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/pluginprompt", v)
}

// handlePluginInstall completes the prompt: the ticked permissions are granted.
func (a *Admin) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	back, label := a.adminPath+"/plugins", "Back to plugins"
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	st, found := a.staged.take(userFrom(r.Context()).ID, r.PostForm.Get("staged"))
	if !found {
		a.ErrorBack(w, r, http.StatusGone, "Install expired", "That install was started a while ago. Start it again.", back, label)
		return
	}
	grant := r.PostForm["permission"]
	if grant == nil {
		grant = []string{}
	}
	if err := pk.Install(r.Context(), st.data, st.source, grant, r.PostForm.Get("confirm") == "1"); err != nil {
		a.refused(w, r, err, back, label)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Plugin installed and running. No restart needed.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *Admin) handlePluginEnable(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	on := strings.HasSuffix(r.URL.Path, "/enable")
	if err := pk.SetEnabled(r.Context(), name, on); err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Plugin not found", "That plugin is not installed from a package.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	a.invalidate(r.Context())
	if on {
		a.SetFlash(w, r, "success", name+" is on.")
	} else {
		a.SetFlash(w, r, "success", name+" is off. Its data is kept.")
	}
	http.Redirect(w, r, a.pluginURL(name, "settings"), http.StatusSeeOther)
}

// ---- uninstall ----

type uninstallData struct {
	Name        string
	DataOptions []ui.Option
	// Types are the content types the plugin installed; the switch to remove
	// them shows only when there are some.
	Types []ContentTypeUse
	// Danger is the typed confirmation shown before content types are removed.
	Danger *ui.DangerConfirm
}

// uninstallDanger is the typed confirmation for removing a plugin's content
// types and their entries: the plugin name, repeated back by the owner.
func (a *Admin) uninstallDanger(r *http.Request, name string, drop bool, types []ContentTypeUse) ui.DangerConfirm {
	entries := 0
	for _, t := range types {
		entries += t.Entries
	}
	dataVal := "0"
	if drop {
		dataVal = "1"
	}
	return ui.DangerConfirm{
		ID:     "dc-plugin-uninstall",
		Title:  "Delete content types and their content?",
		Phrase: name,
		Prompt: "To remove them, type " + name + " below.",
		Action: a.adminPath + "/plugins/" + name + "/uninstall",
		Back:   a.adminPath + "/plugins/" + name + "/uninstall",
		Label:  "Uninstall and delete content",
		CSRF:   csrfToken(r.Context()),
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: fmt.Sprintf("%d entries of %d content types are deleted for good, including entries you wrote yourself. Webhooks and other subscribers are told about each deletion.", entries, len(types))}}},
			{Parts: []ui.DangerPart{{Text: "This cannot be undone."}}},
		},
		Hidden: []ui.ConfirmHidden{{Name: "drop_data", Value: dataVal}, {Name: "drop_content_types", Value: "1"}},
	}
}

// uninstallDataOptions are the two answers to what happens to the plugin's
// data; keeping it is the default.
func uninstallDataOptions() []ui.Option {
	return []ui.Option{
		{Value: "0", Label: "Keep its data. The plugin's own database schema and stored values stay, so installing it again brings everything back.", Selected: true},
		{Value: "1", Label: "Delete its data too. Drops the plugin's own database schema, its database role and its stored values and settings. This cannot be undone."},
	}
}

func (a *Admin) handlePluginUninstallForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.packageAdmin(w, r); !ok {
		return
	}
	v := a.NewView(w, r, "Uninstall "+r.PathValue("name"))
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: r.PathValue("name")}}
	pk, _ := a.packageAdmin(w, r)
	v.Data = uninstallData{Name: r.PathValue("name"), DataOptions: uninstallDataOptions(), Types: pk.ContentTypes(r.Context(), r.PathValue("name"))}
	a.Render(w, r, http.StatusOK, "settings/pluginuninstall", v)
}

func (a *Admin) handlePluginUninstall(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	drop := r.PostFormValue("drop_data") == "1"
	dropTypes := r.PostFormValue("drop_content_types") == "1"
	if dropTypes {
		// Deleting content is irreversible: step-up auth, then the typed name.
		if sess := sessionFrom(r.Context()); sess == nil || !a.sudo.store.Active(sess.ID) {
			a.toSudo(w, r)
			return
		}
		types := pk.ContentTypes(r.Context(), name)
		if len(types) > 0 {
			dc := a.uninstallDanger(r, name, drop, types)
			if checked, ok := CheckDangerPhrase(r, dc); !ok {
				// The first visit (no phrase typed yet) is not an error.
				status := http.StatusUnprocessableEntity
				if r.PostFormValue("confirm_phrase") == "" {
					status, checked.Error = http.StatusOK, ""
				}
				v := a.NewView(w, r, "Uninstall "+name)
				v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: name}}
				v.Data = uninstallData{Name: name, Danger: &checked}
				a.Render(w, r, status, "settings/pluginuninstall", v)
				return
			}
		}
	}
	if err := pk.Uninstall(r.Context(), "plugin", name, drop, dropTypes); err != nil {
		a.logger().Error("uninstall plugin failed", "plugin", name, "err", err)
		a.ErrorBack(w, r, http.StatusInternalServerError, "Not uninstalled", "The plugin could not be removed. It may not be installed from a package.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	a.invalidate(r.Context())
	msg := name + " was uninstalled. Its data was kept; installing it again brings it back."
	if drop {
		msg = name + " was uninstalled and its data was deleted. Your pages and content were not touched."
	}
	if dropTypes {
		msg += " The content types it installed and their content were removed."
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.adminPath+"/plugins", http.StatusSeeOther)
}
