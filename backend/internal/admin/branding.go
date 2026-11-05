package admin

import (
	"html/template"
	"strconv"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/admin/a11y"
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
