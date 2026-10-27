package admin

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// mountThemes registers the Themes page (it replaces the Next theme screen).
// Browsing, installing, activating and settings are owner only; previewing is
// also open to editors.
func (a *Admin) mountThemes(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("GET /theme/themes", a.handleThemes)
	owner.HandleFunc("POST /theme/themes/fetch", a.handleThemeFetch)
	owner.HandleFunc("GET /theme/colours", a.handleDesignForm)
	owner.HandleFunc("POST /theme/colours", a.handleDesignSave)
	owner.HandleFunc("GET /theme/design-system", a.handleTokensForm)
	owner.HandleFunc("POST /theme/design-system", a.handleTokensSave)
	owner.HandleFunc("POST /theme/design-system/reset", a.handleTokensReset)
	owner.HandleFunc("GET /theme/parts", a.handlePartsForm)
	owner.HandleFunc("POST /theme/parts", a.handlePartsSave)
	owner.HandleFunc("GET /theme/presets", a.handlePresetsForm)
	owner.HandleFunc("POST /theme/presets", a.handlePresetsSave)
	owner.HandleFunc("POST /theme/presets/new", a.handlePresetAdd)
	owner.HandleFunc("GET /theme/presets/{name}/delete", a.handlePresetDeleteForm)
	owner.HandleFunc("POST /theme/presets/{name}/delete", a.handlePresetDelete)
	owner.HandleFunc("GET /theme/themes/{name}/activate", a.handleThemeActivateForm)
	owner.HandleFunc("POST /theme/themes/{name}/activate", a.handleThemeActivate)
	owner.HandleFunc("GET /theme/themes/{name}/settings", a.handleThemeSettings)
	owner.HandleFunc("POST /theme/themes/{name}/settings", a.handleThemeSettingsSave)
	owner.HandleFunc("GET /theme/themes/{name}/uninstall", a.handleThemeUninstallForm)
	owner.HandleFunc("POST /theme/themes/{name}/uninstall", a.handleThemeUninstall)
	both := authed.Group("", a.RequireRole("owner", "editor"))
	both.HandleFunc("GET /preview/theme/{name}", a.handleThemePreview)
}

// themeCard is a theme with the colour strip of its card. Palette always has
// five blocks; Hex is empty when the theme has no colour data, and the block is
// then neutral.
type themeCard struct {
	ThemeInfo
	Palette []themeSwatch
}

type themeSwatch struct {
	Hex string
	X   int // left edge on the 100 wide strip
}

// themePalette is the strip of the live theme: the site's brand colours,
// which the live theme applies. A theme manifest carries no colours, so every
// other theme gets neutral blocks.
func themePalette(t ThemeInfo, cfg *siteconfig.Config) []themeSwatch {
	out := make([]themeSwatch, 5)
	for i := range out {
		out[i].X = i * 20
	}
	if !t.Active || cfg == nil {
		return out
	}
	f := designFrom(cfg)
	colours := []string{f.Primary, f.Secondary, f.Accent, f.Background, f.Text}
	for _, c := range colours {
		if !hexColour.MatchString(c) {
			return out // a half-filled strip would mislead
		}
	}
	for i, c := range colours {
		out[i].Hex = c
	}
	return out
}

type themesPage struct {
	Themes        []themeCard
	Available     []RegistryEntry
	RegistryErr   string
	RegistryHost  string
	RegistryOff   bool
	AllowUnsigned bool
	// Actions is the header's Upload theme button (opens the dialog).
	Actions []ui.Button
}

func (a *Admin) handleThemes(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	themes, err := pk.Themes(r.Context())
	if err != nil {
		a.logger().Error("list themes failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The themes could not be listed.")
		return
	}
	cfg, _ := a.loadSite(r.Context())
	cards := make([]themeCard, len(themes))
	for i, t := range themes {
		cards[i] = themeCard{ThemeInfo: t, Palette: themePalette(t, cfg)}
	}
	d := themesPage{Themes: cards, AllowUnsigned: pk.AllowUnsigned(r.Context()),
		Actions: []ui.Button{{Label: "Upload theme", Href: a.adminPath + "/plugins/upload?for=theme", Variant: "primary", Icon: "upload", Attrs: map[string]string{"data-hm-open": "upload-pkg"}}}}
	reg := pk.Registry(r.Context())
	d.RegistryErr, d.RegistryOff, d.RegistryHost = reg.Err, reg.Off, reg.Host
	for _, e := range reg.Entries {
		if e.Kind == "theme" && (!e.Installed || e.Update) {
			d.Available = append(d.Available, e)
		}
	}
	v := a.NewView(w, r, "Theme")
	v.Section, v.Tab = "themes", "themes"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/theme", v)
}

// handleThemeFetch installs (or updates) a registry theme. A theme asks for no
// permissions, so there is no prompt; it stays inactive until activated.
func (a *Admin) handleThemeFetch(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PostFormValue("name")
	data, err := pk.Fetch(r.Context(), "theme", name)
	var sum PackageSummary
	if err == nil {
		sum, err = pk.Inspect(r.Context(), data)
	}
	if err != nil {
		a.refused(w, r, err, a.adminPath+"/theme/themes", "Back to themes")
		return
	}
	a.installTheme(w, r, pk, data, "registry", sum)
}

// handleThemePreview sends the signed-in owner or editor to the public site
// rendered with the theme.
func (a *Admin) handleThemePreview(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, pk.PreviewURL(r.PathValue("name")), http.StatusSeeOther)
}

// ---- activation ----

type activateData struct {
	Name    string
	Builtin bool
	Missing []MissingSection
}

func (a *Admin) handleThemeActivateForm(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	missing, err := pk.MissingSections(r.Context(), name)
	if err != nil {
		a.ErrorBack(w, r, http.StatusNotFound, "Theme not found", "That theme is not installed.", a.adminPath+"/theme/themes", "Back to themes")
		return
	}
	v := a.NewView(w, r, "Activate "+name)
	v.Crumbs = a.areaCrumbs("themes", Crumb{Label: name})
	v.Data = activateData{Name: name, Builtin: name == "default", Missing: missing}
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "settings/themeactivate", v)
}

func (a *Admin) handleThemeActivate(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := pk.Activate(r.Context(), name); err != nil {
		a.refused(w, r, err, a.adminPath+"/theme/themes", "Back to themes")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Theme "+name+" is now live.")
	http.Redirect(w, r, a.adminPath+"/theme/themes", http.StatusSeeOther)
}

func (a *Admin) handleThemeUninstallForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.packageAdmin(w, r); !ok {
		return
	}
	name := r.PathValue("name")
	a.renderConfirm(w, r, confirmData{Title: "Uninstall " + name + "?", Message: "The theme and its settings are removed. Your content is not touched. An active theme cannot be removed: activate another one first.",
		Post: a.adminPath + "/theme/themes/" + name + "/uninstall", Label: "Uninstall", Back: a.adminPath + "/theme/themes", Danger: true})
}

func (a *Admin) handleThemeUninstall(w http.ResponseWriter, r *http.Request) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := pk.Uninstall(r.Context(), "theme", name, false); err != nil {
		if errors.Is(err, ErrThemeActive) {
			a.ErrorBack(w, r, http.StatusConflict, "Theme is active", "Activate another theme first, then uninstall this one.", a.adminPath+"/theme/themes", "Back to themes")
			return
		}
		a.logger().Error("uninstall theme failed", "theme", name, "err", err)
		a.ErrorBack(w, r, http.StatusNotFound, "Not uninstalled", "That theme is not installed.", a.adminPath+"/theme/themes", "Back to themes")
		return
	}
	a.SetFlash(w, r, "success", "Theme "+name+" was uninstalled.")
	http.Redirect(w, r, a.adminPath+"/theme/themes", http.StatusSeeOther)
}

// ---- theme settings (a schemaform from the theme's settings_schema) ----

type themeSettingsData struct {
	Name    string
	Form    template.HTML
	Summary []ui.FieldError
}

func (a *Admin) renderThemeSettings(w http.ResponseWriter, r *http.Request, status int, name string, schema []byte, values map[string]any, errs schemaform.Errors) {
	form, err := schemaform.Render(schema, values, schemaform.Options{Errors: errs})
	if err != nil {
		a.logger().Error("render theme settings form failed", "theme", name, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings form of this theme could not be built.")
		return
	}
	d := themeSettingsData{Name: name, Form: form}
	for path, msgs := range errs {
		for _, m := range msgs {
			d.Summary = append(d.Summary, ui.FieldError{ID: strings.ReplaceAll(path, ".", "-"), Label: schemaform.Humanize(path[strings.LastIndex(path, ".")+1:]), Message: m})
		}
	}
	v := a.NewView(w, r, "Settings of "+name)
	v.Crumbs = a.areaCrumbs("themes", Crumb{Label: name})
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "settings/themesettings", v)
}

func (a *Admin) themeSchema(w http.ResponseWriter, r *http.Request) (PackageAdmin, string, []byte, bool) {
	pk, ok := a.packageAdmin(w, r)
	if !ok {
		return nil, "", nil, false
	}
	name := r.PathValue("name")
	schema := pk.ThemeSettingsSchema(r.Context(), name)
	if len(schema) == 0 {
		a.ErrorBack(w, r, http.StatusNotFound, "No settings", "That theme is not installed or has no settings.", a.adminPath+"/theme/themes", "Back to themes")
		return nil, "", nil, false
	}
	return pk, name, schema, true
}

func (a *Admin) handleThemeSettings(w http.ResponseWriter, r *http.Request) {
	pk, name, schema, ok := a.themeSchema(w, r)
	if !ok {
		return
	}
	values, err := pk.ThemeSettings(r.Context(), name)
	if err != nil {
		a.logger().Error("load theme settings failed", "theme", name, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	a.renderThemeSettings(w, r, http.StatusOK, name, schema, values, nil)
}

func (a *Admin) handleThemeSettingsSave(w http.ResponseWriter, r *http.Request) {
	pk, name, schema, ok := a.themeSchema(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	prev, err := pk.ThemeSettings(r.Context(), name)
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	values, errs := schemaform.Decode(schema, r.PostForm, prev, schemaform.Options{})
	if schemaform.Structural(r.PostForm) {
		a.renderThemeSettings(w, r, http.StatusOK, name, schema, values, nil)
		return
	}
	if len(errs) == 0 {
		if errs, err = pk.SaveThemeSettings(r.Context(), name, values); err != nil {
			a.logger().Error("save theme settings failed", "theme", name, "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be saved.")
			return
		}
	}
	if len(errs) > 0 {
		a.renderThemeSettings(w, r, http.StatusUnprocessableEntity, name, schema, values, errs)
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Settings saved for "+name+".")
	http.Redirect(w, r, a.adminPath+"/theme/themes/"+name+"/settings", http.StatusSeeOther)
}

// ---- design: colours, fonts, corner radius ----

type designForm struct {
	Primary, Secondary, Accent, Background, Text string
	Font, HeadingFont                            string
	RadiusSm, RadiusMd, RadiusLg                 string
}

type designData struct {
	Form    designForm
	Errs    map[string]string
	Summary []ui.FieldError
}

var (
	fontPattern   = regexp.MustCompile(`^[A-Za-z0-9 ,'"._-]{1,200}$`)
	radiusPattern = regexp.MustCompile(`^\d{1,3}(\.\d{1,2})?(px|rem|em)$`)
)

func designFrom(cfg *siteconfig.Config) designForm {
	t := siteconfig.DefaultThemeConfig()
	if len(cfg.ThemeExtra) > 0 {
		_ = json.Unmarshal(cfg.ThemeExtra, &t)
	}
	if hexColour.MatchString(cfg.PrimaryColor) {
		t.Colors.Primary = cfg.PrimaryColor
	}
	return designForm{
		Primary: t.Colors.Primary, Secondary: t.Colors.Secondary, Accent: t.Colors.Accent, Background: t.Colors.Background, Text: t.Colors.Text,
		Font: t.Typography.FontFamily, HeadingFont: t.Typography.HeadingFamily,
		RadiusSm: t.BorderRadius.Sm, RadiusMd: t.BorderRadius.Md, RadiusLg: t.BorderRadius.Lg,
	}
}

// fetchMode reports a same-origin JS save (the builder's Site styles drawer): the same validation,
// but the answer is 204 or a JSON 422 and no flash is set, because the page is not reloaded.
func fetchMode(r *http.Request) bool { return r.Header.Get("X-Hm-Fetch") == "1" }

func (f *designForm) validate() map[string]string {
	errs := map[string]string{}
	for id, p := range map[string]*string{"primary": &f.Primary, "secondary": &f.Secondary, "accent": &f.Accent, "background": &f.Background, "text": &f.Text} {
		*p = strings.TrimSpace(*p)
		if !hexColour.MatchString(*p) {
			errs[id] = "Choose a colour"
		}
	}
	for id, p := range map[string]*string{"font": &f.Font, "heading_font": &f.HeadingFont} {
		*p = strings.TrimSpace(*p)
		if !fontPattern.MatchString(*p) {
			errs[id] = "Use letters, numbers, spaces and commas, for example: Inter, sans-serif"
		}
	}
	for id, p := range map[string]*string{"radius_sm": &f.RadiusSm, "radius_md": &f.RadiusMd, "radius_lg": &f.RadiusLg} {
		*p = strings.TrimSpace(*p)
		if !radiusPattern.MatchString(*p) {
			errs[id] = "Use a size like 8px or 0.5rem"
		}
	}
	return errs
}

var designLabels = []struct{ id, label string }{
	{"primary", "Brand colour"}, {"secondary", "Secondary colour (for themes that use it)"}, {"accent", "Theme accent (for themes that use it)"}, {"background", "Background colour"}, {"text", "Text colour"},
	{"font", "Body font"}, {"heading_font", "Heading font"}, {"radius_sm", "Small corners"}, {"radius_md", "Medium corners"}, {"radius_lg", "Large corners"},
}

func (a *Admin) renderDesign(w http.ResponseWriter, r *http.Request, status int, f designForm, errs map[string]string) {
	d := designData{Form: f, Errs: errs}
	for _, k := range designLabels {
		if m := errs[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	v := a.NewView(w, r, "Colours and fonts")
	v.Section, v.Tab = "colours", "colours"
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "settings/themedesign", v)
}

func (a *Admin) handleDesignForm(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	a.renderDesign(w, r, http.StatusOK, designFrom(cfg), nil)
}

func (a *Admin) handleDesignSave(w http.ResponseWriter, r *http.Request) {
	f := designForm{
		Primary: r.PostFormValue("primary"), Secondary: r.PostFormValue("secondary"), Accent: r.PostFormValue("accent"),
		Background: r.PostFormValue("background"), Text: r.PostFormValue("text"),
		Font: r.PostFormValue("font"), HeadingFont: r.PostFormValue("heading_font"),
		RadiusSm: r.PostFormValue("radius_sm"), RadiusMd: r.PostFormValue("radius_md"), RadiusLg: r.PostFormValue("radius_lg"),
	}
	if fetchMode(r) {
		// A JS save posts only the fields it shows: the others keep their stored value (no lost update).
		cfg, err := a.loadSite(r.Context())
		if err != nil {
			a.fetchOrError(w, r, http.StatusInternalServerError, "The settings could not be loaded.")
			return
		}
		f = designFrom(cfg)
		for id, p := range map[string]*string{"primary": &f.Primary, "secondary": &f.Secondary, "accent": &f.Accent, "background": &f.Background, "text": &f.Text,
			"font": &f.Font, "heading_font": &f.HeadingFont, "radius_sm": &f.RadiusSm, "radius_md": &f.RadiusMd, "radius_lg": &f.RadiusLg} {
			if _, ok := r.PostForm[id]; ok {
				*p = r.PostForm.Get(id)
			}
		}
	}
	if errs := f.validate(); len(errs) > 0 {
		if fetchMode(r) {
			a.writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
			return
		}
		a.renderDesign(w, r, http.StatusUnprocessableEntity, f, errs)
		return
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	// Merge into the stored theme object: keys this form does not own
	// (header button, social links) are kept; the primary colour lives only in
	// its own column.
	merged := map[string]json.RawMessage{}
	_ = json.Unmarshal(cfg.ThemeExtra, &merged)
	set := func(key string, v any) {
		b, _ := json.Marshal(v)
		merged[key] = b
	}
	set("colors", map[string]string{"secondary": f.Secondary, "accent": f.Accent, "background": f.Background, "text": f.Text})
	set("typography", map[string]string{"fontFamily": f.Font, "headingFamily": f.HeadingFont})
	set("borderRadius", map[string]string{"sm": f.RadiusSm, "md": f.RadiusMd, "lg": f.RadiusLg})
	raw, _ := json.Marshal(merged)
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: raw, PrimaryColor: &f.Primary}); err != nil {
		a.logger().Error("save design failed", "err", err)
		a.fetchOrError(w, r, http.StatusInternalServerError, "The design could not be saved.")
		return
	}
	a.invalidate(r.Context())
	if fetchMode(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.SetFlash(w, r, "success", "Colours and fonts saved.")
	http.Redirect(w, r, a.adminPath+"/theme/colours", http.StatusSeeOther)
}

// fetchError answers a fetch-mode failure as JSON {code,error} and reports whether it did; the HTML path keeps the error page.
func (a *Admin) fetchError(w http.ResponseWriter, r *http.Request, status int, message string) bool {
	if !fetchMode(r) {
		return false
	}
	code := map[int]string{http.StatusForbidden: "forbidden", http.StatusInternalServerError: "internal"}[status]
	a.writeJSON(w, status, map[string]string{"code": code, "error": message})
	return true
}

func (a *Admin) fetchOrError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if !a.fetchError(w, r, status, message) {
		a.Error(w, r, status, "Something went wrong", message)
	}
}
