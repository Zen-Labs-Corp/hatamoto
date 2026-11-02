package admin

import (
	"net/url"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

// The index switches of Titles & meta live in seo.Index, apart from the title
// templates: a type with only a noindex flag has no template row to keep it,
// and a Titles save must never wipe it. A post without these keys leaves the
// stored values as they are.
const (
	seoIndexValue   = "index"
	seoIndexNoIndex = "noindex"
)

// seoIndexArchiveKeys are the archive rows of the tab that have an index switch.
var seoIndexArchiveKeys = map[string]string{
	"archive": "Index content archives",
	"author":  "Index author pages",
	"search":  "Index search results",
}

// seoIndexFromForm applies the posted index keys over idx. types and kinds are
// the rows the page listed; others are left alone. idx is a private copy.
func seoIndexFromForm(form url.Values, idx seo.Index, types []seoTypeRow, kinds []string) seo.Index {
	idx.NoIndexTypes = copyFlags(idx.NoIndexTypes)
	idx.NoIndexTaxonomy = copyFlags(idx.NoIndexTaxonomy)
	for _, row := range types {
		if name := "type[" + row.Slug + "][index]"; form.Has(name) {
			setFlag(&idx.NoIndexTypes, row.Slug, form.Get(name) == seoIndexNoIndex)
		}
	}
	for _, kind := range kinds {
		if name := "index[taxonomy][" + kind + "]"; form.Has(name) {
			setFlag(&idx.NoIndexTaxonomy, kind, form.Get(name) != seoIndexValue)
		}
	}
	if form.Has("index[author]") {
		idx.NoIndexAuthor = form.Get("index[author]") != seoIndexValue
	}
	if form.Has("index[archive]") {
		idx.NoIndexArchive = form.Get("index[archive]") != seoIndexValue
	}
	if form.Has("index[search]") {
		idx.IndexSearch = form.Get("index[search]") == seoIndexValue
	}
	return idx
}

func copyFlags(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// setFlag records a true flag and drops a false one; an empty map becomes nil
// so nothing is stored when nothing is noindex.
func setFlag(m *map[string]bool, key string, on bool) {
	if on {
		(*m)[key] = true
	} else {
		delete(*m, key)
	}
}

// seoIndexSave copies the index switches the tab owns onto a fresh read.
func seoIndexSave(dst *seo.Settings, idx seo.Index) {
	dst.Index = idx
	if len(dst.Index.NoIndexTypes) == 0 {
		dst.Index.NoIndexTypes = nil
	}
	if len(dst.Index.NoIndexTaxonomy) == 0 {
		dst.Index.NoIndexTaxonomy = nil
	}
}

// seoIndexOptions is the index / noindex choice of a content type.
func seoIndexOptions(noindex bool) []ui.Option {
	return []ui.Option{
		{Value: seoIndexValue, Label: "Index", Selected: !noindex},
		{Value: seoIndexNoIndex, Label: "Noindex", Selected: noindex},
	}
}
