package a11y

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Tokens are the custom properties of one theme, resolved (var() followed).
type Tokens map[string]string

var (
	blockRx = regexp.MustCompile(`(?s):root(\[data-admin-theme="dark"\])?\s*\{(.*?)\}`)
	propRx  = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	varRx   = regexp.MustCompile(`^var\((--[a-z0-9-]+)\)$`)
)

// ParseTokens reads tokens.css into the light and dark token sets (dark
// inherits whatever it does not override, as the cascade does).
func ParseTokens(css string) (light, dark Tokens) {
	light, dark = Tokens{}, Tokens{}
	for _, m := range blockRx.FindAllStringSubmatch(css, -1) {
		target := light
		if m[1] != "" {
			target = dark
		}
		for _, p := range propRx.FindAllStringSubmatch(m[2], -1) {
			target[p[1]] = strings.TrimSpace(p[2])
		}
	}
	merged := Tokens{}
	for k, v := range light {
		merged[k] = v
	}
	for k, v := range dark {
		merged[k] = v
	}
	return light, merged
}

// Resolve follows var() references.
func (t Tokens) Resolve(name string) (string, bool) {
	v, ok := t[name]
	for i := 0; ok && i < 8; i++ {
		m := varRx.FindStringSubmatch(v)
		if m == nil {
			return v, true
		}
		v, ok = t[m[1]]
	}
	return "", false
}

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff), true
}

func lum(r, g, b float64) float64 {
	f := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

// Ratio is the WCAG contrast ratio of two #rrggbb colours.
func Ratio(fg, bg string) (float64, error) {
	r1, g1, b1, ok1 := parseHex(fg)
	r2, g2, b2, ok2 := parseHex(bg)
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("cannot parse %q / %q", fg, bg)
	}
	a, b := lum(r1, g1, b1), lum(r2, g2, b2)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05), nil
}

// Pair is a foreground/background token pair and the minimum ratio: 4.5 for
// text, 3 for UI boundaries and focus rings (WCAG 2.2 1.4.3 / 1.4.11).
type Pair struct {
	FG, BG string
	Min    float64
	What   string
}

// DeclaredPairs are the combinations the partials actually use.
var DeclaredPairs = []Pair{
	{"--color-sumi", "--color-paper", 4.5, "text on surface"},
	{"--color-sumi", "--color-shiro", 4.5, "text on page"},
	{"--color-sumi-soft", "--color-paper", 4.5, "body text on surface"},
	{"--color-sumi-soft", "--color-kasumi", 4.5, "body text on hover"},
	{"--color-hai", "--color-paper", 4.5, "muted text on surface"},
	{"--color-hai", "--color-shiro", 4.5, "muted text on page"},
	{"--color-hai", "--color-kasumi", 4.5, "muted text on hover/fill"},
	{"--color-accent", "--color-paper", 4.5, "link on surface"},
	{"--color-accent", "--color-shiro", 4.5, "link on page"},
	{"--color-accent", "--color-accent-soft", 4.5, "accent on accent-soft"},
	{"--color-success", "--color-success-soft", 4.5, "success text"},
	{"--color-warning", "--color-warning-soft", 4.5, "warning text"},
	{"--color-paper", "--color-guide-pad", 4.5, "canvas padding value badge text"},
	{"--color-danger", "--color-danger-soft", 4.5, "danger text"},
	{"--color-danger", "--color-paper", 4.5, "danger text on surface"},
	{"--color-paper", "--color-sumi", 4.5, "primary button text"},
	{"--color-paper", "--color-danger", 4.5, "danger button text"},
	{"--color-paper", "--color-sumi-soft", 4.5, "primary button hover text"},
	{"--color-accent", "--color-paper", 3, "focus ring on surface"},
	{"--color-accent", "--color-shiro", 3, "focus ring on page"},
	{"--border-control", "--color-paper", 3, "control border on surface"},
	{"--border-control", "--color-shiro", 3, "control border on page"},
	{"--chart-1", "--color-paper", 3, "chart series 1 on surface"},
	{"--chart-2", "--color-paper", 3, "chart series 2 on surface"},
	{"--chart-3", "--color-paper", 3, "chart series 3 on surface"},
	{"--chart-4", "--color-paper", 3, "chart series 4 on surface"},
	{"--chart-5", "--color-paper", 3, "chart series 5 on surface"},
	{"--chart-6", "--color-paper", 3, "chart series 6 on surface"},
	{"--color-brand", "--color-paper", 3, "brand mark on surface"},
	{"--color-brand", "--color-shiro", 3, "brand mark on page"},
	{"--color-sumi", "--color-accent-soft", 4.5, "bulk bar text"},
	{"--color-sumi", "--color-kasumi", 4.5, "active nav text"},
	{"--color-success", "--color-paper", 4.5, "success text on surface"},
	{"--color-warning", "--color-paper", 4.5, "warning text on surface"},
	{"--color-success", "--color-shiro", 4.5, "success text on page"},
	{"--color-warning", "--color-shiro", 4.5, "warning text on page"},
	{"--color-danger", "--color-shiro", 4.5, "danger text on page"},
	{"--color-sumi-soft", "--color-shiro", 4.5, "nav text on sidebar"},
	{"--color-sumi", "--color-kasumi", 4.5, "table head on hover"},
	{"--color-sumi", "--chart-bar-soft", 4.5, "bar list name on its bar"},
	{"--color-sumi", "--chart-bar-quiet", 4.5, "quiet bar list name on its bar"},
}

// ContrastIssues checks every declared pair in both themes.
func ContrastIssues(css string) []string {
	light, dark := ParseTokens(css)
	var out []string
	for name, t := range map[string]Tokens{"light": light, "dark": dark} {
		for _, p := range DeclaredPairs {
			fg, ok1 := t.Resolve(p.FG)
			bg, ok2 := t.Resolve(p.BG)
			if !ok1 || !ok2 {
				out = append(out, fmt.Sprintf("%s: %s or %s is not defined", name, p.FG, p.BG))
				continue
			}
			r, err := Ratio(fg, bg)
			if err != nil {
				out = append(out, fmt.Sprintf("%s: %s: %v", name, p.What, err))
				continue
			}
			if r < p.Min {
				out = append(out, fmt.Sprintf("%s: %s: %s on %s is %.2f:1, need %.1f", name, p.What, p.FG, p.BG, r, p.Min))
			}
		}
	}
	return out
}
