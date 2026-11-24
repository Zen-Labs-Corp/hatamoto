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
	if got := Missing([]string{"comment.signin"}); !slices.Equal(got, []string{"comment.signin"}) {
		t.Fatalf("Missing = %v", got)
	}
	if got := Missing(nil); len(got) != 0 {
		t.Fatalf("Missing(nil) = %v", got)
	}
}

func TestFeaturesReportCommentPolicy(t *testing.T) {
	if !slices.Contains(Features(), "comment.policy") {
		t.Fatalf("Features = %v", Features())
	}
	if got := Missing([]string{"comment.policy"}); len(got) != 0 {
		t.Fatalf("Missing = %v", got)
	}
}

func TestFeaturesReportPluginEntityWrite(t *testing.T) {
	if !slices.Contains(Features(), "plugin.entity-write") {
		t.Fatalf("Features = %v", Features())
	}
	if got := Missing([]string{"plugin.entity-write"}); len(got) != 0 {
		t.Fatalf("Missing = %v", got)
	}
}

func TestFeaturesReportCommentRank(t *testing.T) {
	if !slices.Contains(Features(), "comment.rank") {
		t.Fatalf("Features = %v", Features())
	}
	if got := Missing([]string{"comment.rank"}); len(got) != 0 {
		t.Fatalf("Missing = %v", got)
	}
}
