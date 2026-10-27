package render

import (
	"html/template"
	"regexp"
	"strings"
)

// Theme tokens reach raw <style> blocks (here and on the Next-rendered site),
// so they are restricted to a strict charset instead of relying on escaping.
var (
	colorRe  = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|(rgb|hsl)a?\([0-9 ,.%/-]{1,60}\)|[a-zA-Z]{3,30})$`)
	fontRe   = regexp.MustCompile(`^[\w\s,'"-]{1,200}$`)
	radiusRe = regexp.MustCompile(`^(0|[0-9]+(\.[0-9]+)?(px|rem|em|%))$`)
	quotedRe = regexp.MustCompile(`'[^'"]*'|"[^'"]*"`)
)

// ValidColor reports whether v is empty or a hex/rgb()/hsl()/named colour.
func ValidColor(v string) bool { return v == "" || colorRe.MatchString(v) }

// ValidFont reports whether v is empty or a font stack whose quotes are balanced.
func ValidFont(v string) bool {
	if v == "" {
		return true
	}
	return fontRe.MatchString(v) && !strings.ContainsAny(quotedRe.ReplaceAllString(v, ""), `'"`)
}

// ValidRadius reports whether v is empty or a CSS length.
func ValidRadius(v string) bool { return v == "" || radiusRe.MatchString(v) }

// cssTokens are ThemeTokens re-validated at render time. Values that fail
// validation (e.g. stored before validation existed) are dropped so the
// theme's own default applies. Because every value matches a strict
// charset, it is safe to mark as template.CSS; otherwise html/template would
// print rgb(), hsl() and quoted font stacks as ZgotmplZ.
type cssTokens struct {
	Primary, Background, Text    template.CSS
	Font, FontHeading            template.CSS
	RadiusSm, RadiusMd, RadiusLg template.CSS
}

func tokenCSS(v string, valid func(string) bool) template.CSS {
	if !valid(v) {
		return ""
	}
	return template.CSS(v)
}

func sanitizeTokens(t ThemeTokens) cssTokens {
	return cssTokens{
		Primary: tokenCSS(t.Primary, ValidColor), Background: tokenCSS(t.Background, ValidColor), Text: tokenCSS(t.Text, ValidColor),
		Font: tokenCSS(t.Font, ValidFont), FontHeading: tokenCSS(t.FontHeading, ValidFont),
		RadiusSm: tokenCSS(t.RadiusSm, ValidRadius), RadiusMd: tokenCSS(t.RadiusMd, ValidRadius), RadiusLg: tokenCSS(t.RadiusLg, ValidRadius),
	}
}

func (t cssTokens) Any() bool { return t != (cssTokens{}) }
