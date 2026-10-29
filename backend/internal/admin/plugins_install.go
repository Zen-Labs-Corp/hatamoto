package admin

import (
	"errors"
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
	owner.HandleFunc("POST /plugins/unsigned", a.handleAllowUnsigned)
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

type promptPerm struct {
	Name, Help string
	Short      string // badge label of a risky permission
	Ticked     bool
	New        bool // requested by an update and not granted before
	Warn       bool // reaches far beyond the plugin's own data; shown as a warning
}

type promptData struct {
	Staged   string
	Sum      PackageSummary
	Perms    []promptPerm
	Unsigned bool
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
	a.prompt(w, r, pk, data, "registry")
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
	a.prompt(w, r, pk, data, "upload")
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

func (a *Admin) prompt(w http.ResponseWriter, r *http.Request, pk PackageAdmin, data []byte, source string) {
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
	for _, p := range sum.Permissions {
		granted := slices.Contains(sum.Granted, p)
		// A package from another signer starts with nothing granted.
		reset := sum.Confirm != nil && (sum.Confirm.SignerChange || sum.Confirm.WidenedAccess)
		// site.scripts is never ticked for the owner: a first install gets it
		// only by an explicit tick; an update keeps it only if it was granted.
		ticked := (!sum.Updating || granted) && !reset
		if p == sdk.PermSiteScripts {
			ticked = sum.Updating && granted && !reset
		}
		t := permtext.Lookup(p)
		d.Perms = append(d.Perms, promptPerm{Name: p, Help: permtext.Help(p), Ticked: ticked, New: sum.Updating && !granted && !reset, Warn: t.Warn, Short: t.Short})
	}
	v := a.NewView(w, r, "Install "+sum.Name)
	v.Crumbs = []Crumb{{Label: "Plugins", Href: a.adminPath + "/plugins"}, {Label: sum.Name}}
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

// handleAllowUnsigned switches the unsigned-package opt-in. Turning it on needs
// an explicit acknowledgement: such a package runs whatever its author wrote.
func (a *Admin) handleAllowUnsigned(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	on := r.PostFormValue("allow") == "1"
	if on && r.PostFormValue("understand") != "1" {
		a.ErrorBack(w, r, http.StatusUnprocessableEntity, "Tick the box to continue", "Unsigned packages can only be allowed after you confirm that you understand the risk.", a.adminPath+"/plugins#unsigned", "Back to plugins")
		return
	}
	if err := pk.SetAllowUnsigned(r.Context(), on, nil, ""); err != nil {
		a.logger().Error("set allow unsigned failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The setting could not be saved.")
		return
	}
	msg := "Unsigned packages are no longer accepted."
	if on {
		msg = "Unsigned packages are now accepted. Only install code you trust."
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.adminPath+"/plugins#unsigned", http.StatusSeeOther)
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
	v.Data = uninstallData{Name: r.PathValue("name"), DataOptions: uninstallDataOptions()}
	a.Render(w, r, http.StatusOK, "settings/pluginuninstall", v)
}

func (a *Admin) handlePluginUninstall(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	drop := r.PostFormValue("drop_data") == "1"
	if err := pk.Uninstall(r.Context(), "plugin", name, drop); err != nil {
		a.logger().Error("uninstall plugin failed", "plugin", name, "err", err)
		a.ErrorBack(w, r, http.StatusInternalServerError, "Not uninstalled", "The plugin could not be removed. It may not be installed from a package.", a.adminPath+"/plugins", "Back to plugins")
		return
	}
	a.invalidate(r.Context())
	msg := name + " was uninstalled. Its data was kept; installing it again brings it back."
	if drop {
		msg = name + " was uninstalled and its data was deleted. Your pages and content were not touched."
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.adminPath+"/plugins", http.StatusSeeOther)
}
