package render

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func relLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("not a #rrggbb colour: %q", hex)
	}
	var l float64
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		n, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		c := float64(n) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		l += w * c
	}
	return l
}

func contrastRatio(t *testing.T, a, b string) float64 {
	la, lb := relLuminance(t, a), relLuminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The footer text colour meets WCAG AA (4.5:1) on the page background in the
// light and the dark token set of the default theme.
func TestFooterContrast(t *testing.T) {
	raw, err := os.ReadFile("themes/default/assets/css/base.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	rule := regexp.MustCompile(`(?m)^\.hm-footer \{[^}]*\bcolor: var\((--[a-z-]+)\)`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal(".hm-footer colour is not a var() token")
	}
	token := rule[1]
	block := func(re string) string {
		m := regexp.MustCompile(re).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("block %q not found", re)
		}
		return m[1]
	}
	val := func(body, name string) string {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6})`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s not set to a hex colour", name)
		}
		return m[1]
	}
	for name, body := range map[string]string{
		"light": block(`(?s):root \{(.*?)\n\}`),
		"dark":  block(`(?s):root\[data-theme="dark"\] \{(.*?)\n\}`),
	} {
		if r := contrastRatio(t, val(body, token), val(body, "--hm-bg")); r < 4.5 {
			t.Errorf("%s: footer %s on --hm-bg = %.2f:1, want >= 4.5", name, token, r)
		}
		for _, bg := range []string{"--hm-bg", "--hm-shiro", "--hm-kasumi"} {
			if r := contrastRatio(t, val(body, "--hm-label"), val(body, bg)); r < 4.5 {
				t.Errorf("%s: --hm-label on %s = %.2f:1, want >= 4.5", name, bg, r)
			}
		}
	}
}
