package packages

import (
	"errors"
	"testing"
	"time"
)

func TestCheckFresh(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	f := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	day := 24 * time.Hour
	cases := []struct {
		name            string
		issued, expires string
		max             time.Duration
		want            error
	}{
		{"ok", f(-day), f(day), 15 * day, nil},
		{"expired", f(-2 * day), f(-day), 15 * day, ErrIndexExpired},
		{"expires exactly now", f(-day), f(0), 15 * day, ErrIndexExpired},
		{"future", f(time.Hour), f(day), 15 * day, ErrIndexNotFresh},
		{"inside skew", f(5 * time.Minute), f(day), 15 * day, nil},
		{"over lifetime", f(-day), f(15 * day), 15 * day, ErrIndexNotFresh},
		{"at lifetime", f(-day), f(14 * day), 15 * day, nil},
		{"negative window", f(day), f(-day), 15 * day, ErrIndexNotFresh},
		{"equal window", f(0), f(0), 15 * day, ErrIndexNotFresh},
		{"malformed", "soon", f(day), 15 * day, ErrIndexNotFresh},
	}
	for _, tc := range cases {
		err := CheckFresh("release manifest", tc.issued, tc.expires, now, tc.max)
		if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestVersionHelpers(t *testing.T) {
	valid := map[string]bool{
		"v1.2.3": true, "1.2.3": true, "v1.2": false, "v1.2.3-rc.1": true,
		"v1.2.3+build5": true, "": false, "vx.y.z": false,
		"v1.2.3-": false, "v+1.2.3": false, "v1.+2.3": false, "v1.2.3+": false, "v1.2.3-rc..1": false,
		"v1.2.3-rc_1": false, "garbage": false, "v1.2.3.4": false, "v1.-2.3": false, "v1. 2.3": false,
	}
	for v, want := range valid {
		if got := ValidSemver(v); got != want {
			t.Errorf("ValidSemver(%q) = %v, want %v", v, got, want)
		}
	}
	cmp := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.3+a", "v1.2.3+b", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.10.0", -1},
		{"v1.2.3", "v1.2.3-rc.1", 1},
		{"v1.2.3-rc.1", "v1.2.3", -1},
		{"v1.2.3-rc.2", "v1.2.3-rc.1", 1},
		{"v1.0.0-rc.10", "v1.0.0-rc.9", 1},
		{"v1.0.0-rc.9", "v1.0.0-rc.10", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1},
		{"v1.0.0-alpha.beta", "v1.0.0-beta", -1},
		{"v1.0.0-beta.11", "v1.0.0-rc.1", -1},
		{"v1.0.0-1", "v1.0.0-alpha", -1},
	}
	for _, tc := range cmp {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
