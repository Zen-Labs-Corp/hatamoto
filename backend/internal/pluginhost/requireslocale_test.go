package pluginhost

import "testing"

func TestRangeRequiresLocale(t *testing.T) {
	older := []string{"0.13.0", "0.14.0", "0.15.0"}
	later := append([]string{"0.16.0"}, older...)
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
		{"garbage", older, false},
	}
	for _, c := range cases {
		if got := rangeRequiresLocale(c.rng, c.comp); got != c.want {
			t.Errorf("rangeRequiresLocale(%q, %v) = %v, want %v", c.rng, c.comp, got, c.want)
		}
	}
}
