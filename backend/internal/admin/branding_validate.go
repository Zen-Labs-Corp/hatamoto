package admin

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/siteconfig"
)

const (
	brandMaxName    = 60
	brandMaxManaged = 80
	brandMaxURL     = 300
)

var (
	// brandLogoRx accepts a raster file of the media library only: a same-origin
	// path, never an absolute or data: URL, and never an SVG.
	brandLogoRx   = regexp.MustCompile(`^/media/[A-Za-z0-9._/-]+\.(?i:png|jpe?g|webp)$`)
	brandAccentRx = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// validateBranding reads the branding form. The returned value carries what
// was submitted (accent lowercased) so the page can show it again; errs is
// empty when it may be stored. The accent must pass the declared contrast
// pairs (a11y.CheckAccent) in both themes.
func validateBranding(form url.Values, tokensCSS string) (b siteconfig.AdminBrand, errs []ui.FieldError) {
	get := func(k string) string { return strings.TrimSpace(form.Get(k)) }
	fail := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }

	b.Name = get("name")
	if utf8.RuneCountInString(b.Name) > brandMaxName || strings.IndexFunc(b.Name, unicode.IsControl) >= 0 {
		fail("name", "Admin name", fmt.Sprintf("Use at most %d characters, without line breaks.", brandMaxName))
	}

	b.LogoURL = get("logo")
	if form.Get("remove_logo") != "" {
		b.LogoURL = ""
	}
	if b.LogoURL != "" && !brandLogoRx.MatchString(b.LogoURL) {
		fail("logo", "Logo", "Choose a PNG, JPEG or WebP image from the media library.")
	}

	b.Accent = strings.ToLower(get("accent"))
	switch {
	case b.Accent == "":
	case !brandAccentRx.MatchString(b.Accent):
		fail("accent", "Accent colour", "Enter a colour like #1f4e9a.")
	default:
		if _, issues := a11y.CheckAccent(tokensCSS, b.Accent); len(issues) > 0 {
			surface, tinted := a11y.LightBackgrounds(tokensCSS, b.Accent)
			fail("accent", "Accent colour", fmt.Sprintf("That colour is too light to read. Needs 4.5:1 on both the admin background and the tinted highlight, this measures %s:1 and %s:1. Try a darker shade.", ratioText(surface), ratioText(tinted)))
		}
	}

	b.ManagedText = get("managed_text")
	if utf8.RuneCountInString(b.ManagedText) > brandMaxManaged || strings.IndexFunc(b.ManagedText, unicode.IsControl) >= 0 {
		fail("managed-text", "Managed by text", fmt.Sprintf("Use at most %d characters, without line breaks.", brandMaxManaged))
	}

	b.ManagedURL = get("managed_url")
	if b.ManagedURL != "" {
		switch u, err := url.Parse(b.ManagedURL); {
		case len(b.ManagedURL) > brandMaxURL:
			fail("managed-url", "Managed by link", fmt.Sprintf("Use at most %d characters.", brandMaxURL))
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			fail("managed-url", "Managed by link", "Enter a web address starting with https:// or http://.")
		case b.ManagedText == "":
			fail("managed-text", "Managed by text", "Add the text the link should show.")
		}
	}
	return b, errs
}

// ratioText prints a contrast ratio to one decimal. A ratio below the 4.5
// minimum that would round up to it is truncated instead, so a failing ratio
// never reads as the minimum itself.
func ratioText(r float64) string {
	if r < 4.5 && r >= 4.45 {
		r = 4.4
	}
	return fmt.Sprintf("%.1f", r)
}
