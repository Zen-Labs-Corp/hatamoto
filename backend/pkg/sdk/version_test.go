package sdk

import (
	"slices"
	"testing"
)

func TestVersionPinned(t *testing.T) {
	if Version != "0.16.0" {
		t.Fatalf("Version = %q, want 0.16.0; changing it is an SDK release decision, update this pin with it", Version)
	}
}

func TestCompatibleWithPinned(t *testing.T) {
	want := []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0", "0.11.0", "0.12.0", "0.13.0", "0.14.0", "0.15.0"}
	if !slices.Equal(CompatibleWith, want) {
		t.Fatalf("CompatibleWith = %v, want %v; every 0.x minor from 0.5 stays served until 1.0", CompatibleWith, want)
	}
	if slices.Contains(CompatibleWith, Version) {
		t.Errorf("CompatibleWith lists Version %s", Version)
	}
	prev := semver{}
	for i, s := range CompatibleWith {
		v, _, err := parseSemver(s)
		if err != nil {
			t.Fatalf("CompatibleWith[%d] = %q: %v", i, s, err)
		}
		if i > 0 && v.cmp(prev) <= 0 {
			t.Errorf("CompatibleWith is not sorted ascending at %q", s)
		}
		prev = v
	}
}

func TestCompatibleWithOldHost(t *testing.T) {
	list := []string{"0.5.0", "0.6.0", "0.7.0", "0.8.0", "0.9.0", "0.10.0", "0.11.0"}
	for rng, want := range map[string]bool{"^0.13": false, "^0.12": true, "^0.11": true, "^0.10": true, "^0.5": true} {
		got, err := compatibleWith("0.12.0", list, rng)
		if err != nil || got != want {
			t.Errorf("compatibleWith(0.12.0, %s) = %v,%v want %v", rng, got, err, want)
		}
	}
}

func TestCompatibleWithNewHost(t *testing.T) {
	list := []string{"0.5.0", "0.12.0"}
	for rng, want := range map[string]bool{"^0.12": true, "^0.16": true, "^0.17": false} {
		got, err := compatibleWith("0.16.0", list, rng)
		if err != nil || got != want {
			t.Errorf("compatibleWith(0.16.0, %s) = %v,%v want %v", rng, got, err, want)
		}
	}
}
