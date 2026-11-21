package redirect

import (
	"slices"
	"testing"
)

func TestSortPatterns_MatchesRuntimeOrder(t *testing.T) {
	sources := []string{"/a/*", "/a/b/*", "/a/**", "/zz/*/c"}
	mk := func() []*Redirect {
		out := make([]*Redirect, len(sources))
		for i, s := range sources {
			out[i] = &Redirect{SourcePath: s, TargetURL: "/t", StatusCode: 301}
		}
		return out
	}
	rules := mk()
	SortPatterns(rules)
	var got []string
	for _, r := range rules {
		got = append(got, r.SourcePath)
	}
	want := []string{"/zz/*/c", "/a/b/*", "/a/**", "/a/*"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	runtime := mk()
	sortPatterns(runtime)
	for i := range runtime {
		if runtime[i].SourcePath != got[i] {
			t.Fatalf("differs from sortPatterns at %d: %q vs %q", i, runtime[i].SourcePath, got[i])
		}
	}
}
