package pluginhost

import (
	"slices"
	"testing"

	"hatamoto.dev/sdk"
)

func TestRangeRequiresLocale(t *testing.T) {
	older := []string{"0.13.0", "0.14.0", "0.15.0"}
	later := append([]string{"0.17.0", "0.16.0"}, older...)
	cases := []struct {
		rng  string
		comp []string
		want bool
	}{
		{"^0.15", older, true},
		{">=0.15", older, true},
		{"^0.15", later, true},
		{">=0.15", later, true},
		{"^0.14", older, false},
		{"^0.14", later, false},
		{">=0.5", later, false},
		{"^0.13", older, false},
		{"^0.16", later, true},
		{"^0.17", later, true},
		{"garbage", older, false},
	}
	for _, c := range cases {
		if got := rangeRequiresLocale(c.rng, c.comp); got != c.want {
			t.Errorf("rangeRequiresLocale(%q, %v) = %v, want %v", c.rng, c.comp, got, c.want)
		}
	}
}

func TestRangeRequiresTerms(t *testing.T) {
	compat := append(slices.Clone(sdk.CompatibleWith), sdk.Version)
	for rng, want := range map[string]bool{
		"^0.20":    true,
		">=0.20":   true,
		"^0.19":    false,
		">=0.5":    false,
		"^0.5":     false,
		"garbage":  false,
		"^0.21":    false,
		">=0.19.0": false,
	} {
		if got := rangeRequires(rng, compat, termsRequiredSince); got != want {
			t.Errorf("rangeRequires(%q, termsRequiredSince) = %v, want %v", rng, got, want)
		}
	}
}
