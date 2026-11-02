package seo

import "testing"

func TestTemplateOrDefault(t *testing.T) {
	for in, want := range map[string]string{
		"":              DefaultTitle,
		"  \t":          DefaultTitle,
		" %title% ":     "%title%",
		"%title% | Own": "%title% | Own",
	} {
		if got := TemplateOrDefault(in); got != want {
			t.Errorf("TemplateOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeIndex(t *testing.T) {
	idx := Index{NoIndexTypes: map[string]bool{}, NoIndexTaxonomy: map[string]bool{}}
	NormalizeIndex(&idx)
	if idx.NoIndexTypes != nil || idx.NoIndexTaxonomy != nil {
		t.Fatalf("empty maps kept: %+v", idx)
	}
	idx = Index{NoIndexTypes: map[string]bool{"doc": true}}
	NormalizeIndex(&idx)
	if !idx.NoIndexTypes["doc"] {
		t.Fatalf("filled map lost: %+v", idx)
	}
}
