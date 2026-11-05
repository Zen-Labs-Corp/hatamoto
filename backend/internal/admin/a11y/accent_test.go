package a11y

import "testing"

func TestCheckAccent(t *testing.T) {
	css := readTokens(t)
	cases := []struct {
		hex  string
		pass bool
	}{
		{"#f5e642", false},
		{"#999999", false},
		{"#1F4E9A", true},
		{"#000000", true},
		{"#c8402a", false},
		{"#7a2a1c", true},
		{"blue", false},
		{"#fff", false},
	}
	light, dark := ParseTokens(css)
	for _, c := range cases {
		p, issues := CheckAccent(css, c.hex)
		if (len(issues) == 0) != c.pass {
			t.Errorf("%s: pass=%v, issues=%v", c.hex, len(issues) == 0, issues)
			continue
		}
		if !c.pass {
			continue
		}
		// ISC-11: every declared pair holds with the override applied.
		if got := pairIssues("light", withAccent(light, p.Light)); len(got) > 0 {
			t.Errorf("%s light: %v", c.hex, got)
		}
		if got := pairIssues("dark", withAccent(dark, p.Dark)); len(got) > 0 {
			t.Errorf("%s dark: %v", c.hex, got)
		}
	}
}

func TestRatios(t *testing.T) {
	l, d := Ratios(readTokens(t), "#1f4e9a")
	if l < 4.5 || d < 4.5 {
		t.Fatalf("ratios = %.2f, %.2f", l, d)
	}
	if l, d := Ratios(readTokens(t), "nope"); l != 0 || d != 0 {
		t.Fatalf("invalid hex ratios = %v, %v", l, d)
	}
}
