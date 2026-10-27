package adminpath

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestGenerate10kStrongUniqueFormat(t *testing.T) {
	re := regexp.MustCompile(`^/[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}-[0-9a-hjkmnp-tv-z]{4}$`)
	seen := map[string]struct{}{}
	for range 10000 {
		p, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(p) {
			t.Fatalf("bad format %q", p)
		}
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate %q", p)
		}
		seen[p] = struct{}{}
		if got := Assess(context.Background(), p, nil); got.Level != LevelStrong {
			t.Fatalf("%s: %v %+v", p, got.Level, got.Reasons)
		}
	}
}

func TestGenerateAlphabet(t *testing.T) {
	if len(crockford) != 32 || strings.ContainsAny(crockford, "ilou") {
		t.Fatalf("alphabet %q", crockford)
	}
}

// A raw draw that is not strong must be rare, or Generate would spend its
// tries often.
func TestRawDrawRejectRateIsTiny(t *testing.T) {
	const n = 100000
	rejected := 0
	for range n {
		p, err := draw()
		if err != nil {
			t.Fatal(err)
		}
		if Assess(context.Background(), p, nil).Level != LevelStrong {
			rejected++
		}
	}
	t.Logf("rejected %d of %d raw draws", rejected, n)
	if rate := float64(rejected) / n; rate > 0.01 {
		t.Fatalf("reject rate %.4f", rate)
	}
}
