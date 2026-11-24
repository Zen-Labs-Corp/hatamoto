package kit

import (
	"slices"
	"testing"
)

func TestKnownFeaturesPinned(t *testing.T) {
	want := []string{"comment.policy", "comment.rank", "comment.signin", "plugin.entity-write"}
	if !slices.Equal(KnownFeatures, want) {
		t.Fatalf("KnownFeatures = %v, want %v", KnownFeatures, want)
	}
}

func TestFeaturesSubsetOfKnown(t *testing.T) {
	for _, f := range Features() {
		if !slices.Contains(KnownFeatures, f) {
			t.Errorf("feature %q is not a known feature", f)
		}
	}
}

func TestMissing(t *testing.T) {
	if got := Missing([]string{"comment.policy"}); !slices.Equal(got, []string{"comment.policy"}) {
		t.Fatalf("Missing = %v", got)
	}
	if got := Missing(nil); len(got) != 0 {
		t.Fatalf("Missing(nil) = %v", got)
	}
}
