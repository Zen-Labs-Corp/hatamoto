package seo

import "strings"

// TemplateOrDefault trims a title template and falls back to DefaultTitle
// when it is empty, so an emptied home or archive template restores the default.
func TemplateOrDefault(v string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return DefaultTitle
}

// NormalizeIndex nils the empty noindex maps so nothing is stored when
// nothing is noindex.
func NormalizeIndex(idx *Index) {
	if len(idx.NoIndexTypes) == 0 {
		idx.NoIndexTypes = nil
	}
	if len(idx.NoIndexTaxonomy) == 0 {
		idx.NoIndexTaxonomy = nil
	}
}
