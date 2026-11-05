package admin

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// brandView is what the sign-in page and the shell show of the admin branding.
type brandView struct {
	// Name is the admin name, else the company name, else the instance name.
	Name        string
	LogoURL     string
	ManagedText string
	ManagedURL  string
	// CSS is the accent override for the head's nonce'd <style>; empty without
	// an accent. It is built only from validated #rrggbb values.
	CSS template.CSS
	// Accent is true when CSS is set (the bare layout then colours its button).
	Accent bool
}

// brandFrom fills the brand from the site config. name is the existing site
// name chain (company name or fallback); tokensCSS is the admin tokens.css.
func brandFrom(cfg *siteconfig.Config, name, tokensCSS string) brandView {
	b := brandView{Name: name}
	if cfg == nil {
		return b
	}
	ab := cfg.AdminBrand
	if ab.Name != "" {
		b.Name = ab.Name
	}
	b.LogoURL, b.ManagedText, b.ManagedURL = ab.LogoURL, ab.ManagedText, ab.ManagedURL
	if b.ManagedText == "" {
		b.ManagedURL = ""
	}
	if ab.Accent != "" {
		if css := accentCSS(tokensCSS, ab.Accent); css != "" {
			b.CSS, b.Accent = css, true
		}
	}
	return b
}

var accentCache sync.Map // accent + token length -> template.CSS

// accentCSS is the override stylesheet for one accent, or "" when the accent
// does not pass the contrast check. The selector html:root[data-admin-theme]
// (0,2,1) outranks the :root[data-admin-theme="dark"] block of tokens.css.
func accentCSS(tokensCSS, hex string) template.CSS {
	key := hex + "|" + strconv.Itoa(len(tokensCSS))
	if v, ok := accentCache.Load(key); ok {
		return v.(template.CSS)
	}
	css := template.CSS("")
	if p, issues := a11y.CheckAccent(tokensCSS, hex); len(issues) == 0 {
		var sb strings.Builder
		sb.WriteString("html:root[data-admin-theme]{" + accentDecls(p.Light) + "}")
		sb.WriteString(`html:root[data-admin-theme="dark"]{` + accentDecls(p.Dark) + "}")
		sb.WriteString(`@media (prefers-color-scheme:dark){html:root[data-admin-theme="auto"]{` + accentDecls(p.Dark) + "}}")
		css = template.CSS(sb.String())
	}
	accentCache.Store(key, css)
	return css
}

func accentDecls(s a11y.AccentSet) string {
	return "--color-accent:" + s.Accent + ";--admin-accent:" + s.Accent + ";--accent-link:" + s.Link +
		";--color-brand:" + s.Accent + ";--color-on-brand:" + s.OnBrand
}

const brandingPath = "/settings/admin-branding"

// brandingData is the Settings > Branding page: the saved (or, after a failed
// save, submitted) values and the contrast figures of the accent.
type brandingData struct {
	Summary []ui.FieldError
	Errs    map[string]string
	Form    siteconfig.AdminBrand
	// LightRatio and DarkRatio are the accent on the light paper and the
	// derived dark shade (DarkHex) on the dark paper; empty without a valid accent.
	LightRatio, DarkRatio, DarkHex string
	// Customised is true when anything is saved, so Reset is offered.
	Customised bool
}

func (a *Admin) mountBrandingSettings(owner *httpx.Group) {
	owner.HandleFunc("GET "+brandingPath, a.handleBranding)
	owner.HandleFunc("POST "+brandingPath, a.handleBrandingSave)
	owner.HandleFunc("POST "+brandingPath+"/reset", a.handleBrandingReset)
}

func (a *Admin) handleBranding(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.logger().Error("branding settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The branding settings could not be loaded.")
		return
	}
	a.renderBranding(w, r, http.StatusOK, cfg.AdminBrand, nil, cfg.AdminBrand != (siteconfig.AdminBrand{}))
}

func (a *Admin) handleBrandingSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := a.loadSite(ctx)
	if err != nil {
		a.logger().Error("branding settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The branding settings could not be loaded.")
		return
	}
	if err := r.ParseForm(); err != nil {
		// An unreadable body must not pass as an empty form, which would wipe the branding.
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read. Nothing was saved.")
		return
	}
	as, _ := a.state()
	next, errs := validateBranding(r.PostForm, string(as.Tokens))
	if len(errs) > 0 {
		a.renderBranding(w, r, http.StatusUnprocessableEntity, next, errs, cfg.AdminBrand != (siteconfig.AdminBrand{}))
		return
	}
	if err := a.deps.Site.SetAdminBranding(ctx, next); err != nil {
		a.logger().Error("branding save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The branding could not be saved.")
		return
	}
	a.audit(r, "branding.update", "admin-branding", map[string]any{"old": brandDetail(cfg.AdminBrand), "new": brandDetail(next)})
	a.SetFlash(w, r, "success", "Branding saved.")
	http.Redirect(w, r, a.adminPath+brandingPath, http.StatusSeeOther)
}

func (a *Admin) handleBrandingReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := a.loadSite(ctx)
	if err != nil {
		a.logger().Error("branding settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The branding settings could not be loaded.")
		return
	}
	if err := a.deps.Site.ResetAdminBranding(ctx); err != nil {
		a.logger().Error("branding reset", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The branding could not be reset.")
		return
	}
	a.audit(r, "branding.reset", "admin-branding", map[string]any{"old": brandDetail(cfg.AdminBrand)})
	a.SetFlash(w, r, "success", "Branding reset to the default.")
	http.Redirect(w, r, a.adminPath+brandingPath, http.StatusSeeOther)
}

func brandDetail(b siteconfig.AdminBrand) map[string]any {
	return map[string]any{"name": b.Name, "logo": b.LogoURL, "accent": b.Accent, "managed_text": b.ManagedText, "managed_url": b.ManagedURL}
}

func (a *Admin) renderBranding(w http.ResponseWriter, r *http.Request, status int, b siteconfig.AdminBrand, errs []ui.FieldError, customised bool) {
	d := brandingData{Summary: errs, Errs: map[string]string{}, Form: b, Customised: customised}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	as, _ := a.state()
	if p, issues := a11y.CheckAccent(string(as.Tokens), b.Accent); b.Accent != "" && len(issues) == 0 {
		l, dk := a11y.Ratios(string(as.Tokens), b.Accent)
		d.LightRatio, d.DarkRatio, d.DarkHex = strconv.FormatFloat(l, 'f', 1, 64), strconv.FormatFloat(dk, 'f', 1, 64), p.Dark.Accent
	}
	v := a.NewView(w, r, "Branding")
	v.Section, v.Tab = "branding", "branding"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/branding", v)
}
