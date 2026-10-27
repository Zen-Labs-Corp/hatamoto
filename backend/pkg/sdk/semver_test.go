package sdk

import "testing"

func TestSatisfies(t *testing.T) {
	tests := []struct {
		v, rng string
		want   bool
	}{
		{"0.1.0", "^0.1", true},
		{"0.1.9", "^0.1", true},
		{"0.2.0", "^0.1", false},
		{"0.1.0", "^0.1.2", false},
		{"1.4.0", "^1.2", true},
		{"2.0.0", "^1.2", false},
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		{"0.5.0", ">=0.1.0 <1.0.0", true},
		{"1.0.0", ">=0.1.0 <1.0.0", false},
		{"0.1.2", "~0.1.2", true},
		{"0.1.9", "~0.1.2", true},
		{"0.2.0", "~0.1.2", false},
		{"1.9.0", "~1", true},
		{"2.0.0", "~1", false},
		{"0.1.0", "=0.1.0", true},
		{"0.1.1", "=0.1.0", false},
		{"0.1.0", "0.1.0", true},
		{"0.1.1", ">0.1.0", true},
		{"0.1.0", "<=0.1.0", true},
		{"v0.1.0", "^0.1", true},
		{"0.9.0", "^0", true},
		{"0.1.0", "^0", true},
		{"1.0.0", "^0", false},
		{"0.0.9", "^0.0", true},
		{"0.1.0", "^0.0", false},
		// prerelease/build follow semver precedence
		{"0.1.0-rc.1", "^0.1", false},
		{"0.1.0-rc.1", ">=0.1.0-alpha", true},
		{"0.1.0-alpha", ">=0.1.0-rc.1", false},
		{"0.1.0-rc.2", ">0.1.0-rc.1", true},
		{"0.1.0-rc.10", ">0.1.0-rc.2", true},
		{"0.1.0-rc", "<0.1.0-rc.1", true},
		{"0.1.0-1", "<0.1.0-alpha", true},
		{"0.1.0", ">0.1.0-rc.1", true},
		{"0.1.0+build.5", "=0.1.0", true},
		{"0.1.0+build.5", "^0.1", true},
	}
	for _, tt := range tests {
		got, err := Satisfies(tt.v, tt.rng)
		if err != nil {
			t.Errorf("Satisfies(%q,%q): %v", tt.v, tt.rng, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Satisfies(%q,%q) = %v, want %v", tt.v, tt.rng, got, tt.want)
		}
	}
}

func TestSatisfiesErrors(t *testing.T) {
	for _, c := range [][2]string{{"x", "^0.1"}, {"0.1.0", ""}, {"0.1.0", "^a.b"}, {"1.2.3.4", "^1"}, {"", "^1"}, {"1.0.0-", "^1"}, {"1.0.0-01", "^1"}, {"1.0.0+", "^1"}, {"1.0.0-a_b", "^1"}, {"1.0.0-a..b", "^1"}} {
		if _, err := Satisfies(c[0], c[1]); err == nil {
			t.Errorf("Satisfies(%q,%q): want error", c[0], c[1])
		}
	}
}

func TestVersionConstIsValid(t *testing.T) {
	if ok, err := Satisfies(Version, "="+Version); err != nil || !ok {
		t.Fatalf("Version %q invalid: %v", Version, err)
	}
}
