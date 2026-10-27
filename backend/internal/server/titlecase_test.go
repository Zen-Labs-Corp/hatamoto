package server

import (
	"strings"
	"testing"
)

// The old implementation was strings.Title; the helper must give the same
// output, so the table is computed with it.
func TestTitleCaseMatchesStringsTitle(t *testing.T) {
	for _, in := range []string{
		"blog post", "faq", "already Mixed", "",
		"3d models", "3d-models", "top 10s", "top-10s", "10x",
		"o'neil", "foo.bar", "foo_bar baz", "a-b-c", "snake_case",
		"ünïcode wörds", "ßtraße", "ǆungla", "日本語 text", "x\u00a0y", "tab\tsep\nnl",
		"  leading", "trailing  ", "émile-zola", "a1b2", "١٢٣abc",
	} {
		want := strings.Title(in) //lint:ignore SA1019 the reference being matched
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, strings.Title gave %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"3d-models": "3d-Models", "top-10s": "Top-10s"} {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}
