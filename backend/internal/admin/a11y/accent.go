package a11y

import (
	"fmt"
	"strings"
)

// AccentSet is the five admin colour tokens one accent sets in one theme.
type AccentSet struct{ Accent, Link, OnBrand string }

// Palette is the accent as the light theme uses it and the shade derived for
// the dark theme. One hex cannot be readable text on both papers, so the
// owner's colour is the light one and the dark one is lightened until every
// declared pair passes.
type Palette struct{ Light, Dark AccentSet }

// darkSteps is how many 4% steps toward white the dark shade may take; at the
// last step the colour is white, which always passes.
const darkSteps = 25

// CheckAccent validates an owner's #rrggbb accent against the declared pairs
// in both themes and returns the palette to emit. The issues are empty when
// the accent is acceptable; they name the failing pairs otherwise.
func CheckAccent(tokensCSS, hex string) (Palette, []string) {
	light, dark := ParseTokens(tokensCSS)
	hex = strings.ToLower(strings.TrimSpace(hex))
	if _, _, _, ok := parseHex(hex); !ok || len(hex) != 7 || hex[0] != '#' {
		return Palette{}, []string{fmt.Sprintf("%q is not a #rrggbb colour", hex)}
	}

	p := Palette{Light: AccentSet{Accent: hex, Link: hex, OnBrand: "#ffffff"}}
	issues := pairIssues("light", withAccent(light, p.Light))

	onDark, _ := dark.Resolve("--color-paper")
	for i := 0; i <= darkSteps; i++ {
		c := mix(hex, "#ffffff", float64(i)*0.04)
		p.Dark = AccentSet{Accent: c, Link: c, OnBrand: onDark}
		if len(pairIssues("dark", withAccent(dark, p.Dark))) == 0 {
			return p, issues
		}
	}
	return p, append(issues, "dark: no shade of this colour reaches the required contrast")
}

// Ratios are the contrast ratios shown to the owner: the accent on the light
// paper and the derived dark shade on the dark paper. Zero when hex is invalid.
func Ratios(tokensCSS, hex string) (light, dark float64) {
	lt, dt := ParseTokens(tokensCSS)
	p, _ := CheckAccent(tokensCSS, hex)
	if p.Light.Accent == "" {
		return 0, 0
	}
	lp, _ := lt.Resolve("--color-paper")
	dp, _ := dt.Resolve("--color-paper")
	light, _ = Ratio(p.Light.Accent, lp)
	dark, _ = Ratio(p.Dark.Accent, dp)
	return light, dark
}

// withAccent copies t and overrides the tokens an accent drives.
func withAccent(t Tokens, s AccentSet) Tokens {
	c := Tokens{}
	for k, v := range t {
		c[k] = v
	}
	c["--color-accent"], c["--admin-accent"], c["--color-brand"] = s.Accent, s.Accent, s.Accent
	c["--accent-link"], c["--color-on-brand"] = s.Link, s.OnBrand
	return c
}

// mix blends a toward b by f (0 to 1) and returns #rrggbb.
func mix(a, b string, f float64) string {
	r1, g1, b1, _ := parseHex(a)
	r2, g2, b2, _ := parseHex(b)
	m := func(x, y float64) int { return int(x + (y-x)*f + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", m(r1, r2), m(g1, g2), m(b1, b2))
}

// LightBackgrounds are the accent's contrast ratios on the two light admin
// backgrounds it is read on: the plain surface and the tinted highlight
// (accent-soft). Both must reach 4.5:1. Zero when hex is invalid.
func LightBackgrounds(tokensCSS, hex string) (surface, tinted float64) {
	lt, _ := ParseTokens(tokensCSS)
	p, _ := CheckAccent(tokensCSS, hex)
	if p.Light.Accent == "" {
		return 0, 0
	}
	paper, _ := lt.Resolve("--color-paper")
	soft, _ := lt.Resolve("--color-accent-soft")
	surface, _ = Ratio(p.Light.Accent, paper)
	tinted, _ = Ratio(p.Light.Accent, soft)
	return surface, tinted
}
