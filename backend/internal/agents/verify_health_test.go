package agents

import (
	"slices"
	"testing"
)

func TestClassifyHealth(t *testing.T) {
	for _, c := range []struct {
		name              string
		verified, spoofed int64
		want              HealthLevel
	}{
		{"nothing decided", 0, 0, HealthUnknown},
		{"19 decided, all spoofed", 0, 19, HealthUnknown},
		{"20 decided, 21% spoofed", 79, 21, HealthBad}, // 100 decided
		{"exactly 20% spoofed", 80, 20, HealthOK},
		{"20 decided, 5 spoofed is 25%", 15, 5, HealthBad},
		{"20 decided, 4 spoofed is exactly 20%", 16, 4, HealthOK},
		{"healthy", 981, 19, HealthOK},
		{"all verified", 50, 0, HealthOK},
	} {
		h := VerificationHealth{Verified: c.verified, Spoofed: c.spoofed, Unverifiable: 1000}
		if got := ClassifyHealth(h); got != c.want {
			t.Errorf("%s: level %d, want %d", c.name, got, c.want)
		}
	}
}

func TestClassifyHealthIgnoresUnverifiable(t *testing.T) {
	h := VerificationHealth{Verified: 10, Spoofed: 2, Unverifiable: 100000}
	if got := ClassifyHealth(h); got != HealthUnknown {
		t.Errorf("12 decided with many unverifiable = %d, want unknown", got)
	}
}

func TestRangePublishedIDs(t *testing.T) {
	reg := Default()
	got := RangePublishedIDs(reg)
	if len(got) == 0 || !slices.IsSorted(got) {
		t.Fatalf("ids = %v, want a sorted non-empty list", got)
	}
	want := map[string]bool{}
	for _, a := range reg.Agents {
		if len(a.IPRanges) > 0 {
			want[a.ID] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ids, registry has %d range-published agents", len(got), len(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("%s is not range-published", id)
		}
	}
	for _, id := range []string{"gptbot", "claudebot"} {
		if !slices.Contains(got, id) {
			t.Errorf("%s missing", id)
		}
	}
	if slices.Contains(got, "amazonbot") {
		t.Error("amazonbot publishes no ranges and must not be listed")
	}
}
