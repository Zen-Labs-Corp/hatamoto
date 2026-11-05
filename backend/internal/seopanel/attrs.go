package seopanel

import (
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/seo"
)

// Attrs is the SEO values an entity stores in its attributes, as the SEO tabs
// show them.
type Attrs struct {
	Title, Description, Canonical, OGImage string
	OGTitle, OGDescription                 string
	NoIndex                                bool
	SchemaType, CustomJSONLD               string
	NoFollow, NoArchive, NoSnippet         bool
	MaxSnippet, MaxImagePreview            string
	HideFromSitemap                        bool
	BreadcrumbTitle, PrimaryTerm           string
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// FromAttrs reads the SEO values out of an entity's attributes.
func FromAttrs(m map[string]any) Attrs {
	seoMap, _ := m["seo"].(map[string]any)
	b, _ := m["no_index"].(bool)
	b = b || strings.Contains(strings.ToLower(str(seoMap, "robots")), "noindex")
	d := seo.ParseDirectives(str(seoMap, "robots"))
	f := Attrs{Title: str(m, "meta_title"), Description: str(m, "meta_description"), Canonical: str(m, "canonical_url"), OGImage: str(m, "og_image"),
		OGTitle: str(m, "og_title"), OGDescription: str(m, "og_description"), NoIndex: b,
		SchemaType: str(m, "schema_type"), CustomJSONLD: str(m, "custom_jsonld"),
		HideFromSitemap: m["hide_from_sitemap"] == true || str(m, "hide_from_sitemap") == "true",
		BreadcrumbTitle: str(m, "breadcrumb_title"), PrimaryTerm: str(m, "primary_term"),
		NoFollow: d.NoFollow, NoArchive: d.NoArchive, NoSnippet: d.NoSnippet, MaxImagePreview: d.MaxImagePreview}
	if d.MaxSnippet != nil {
		f.MaxSnippet = strconv.Itoa(*d.MaxSnippet)
	}
	return f
}

// BodyProperty is the schema's text property the SEO checks read as the body:
// "content" or "body" when the type has one of type string, else "".
func BodyProperty(sc *entity.EntitySchema) string {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if jsonx.Unmarshal(sc.Schema, &s, jsonx.StoredLoose) != nil {
		return ""
	}
	for _, name := range []string{"content", "body"} {
		if s.Properties[name].Type == "string" {
			return name
		}
	}
	return ""
}
