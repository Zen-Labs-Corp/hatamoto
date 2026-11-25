package menu

import (
	"testing"

	"github.com/google/uuid"
)

func TestIsUntouchedCoreSeed(t *testing.T) {
	header, footer, rail := LocationHeader, LocationFooter, "rail"
	post := "post"
	blog := func() *MenuItem { return &MenuItem{Label: "Blog", LinkType: LinkTypeArchive, SchemaSlug: &post} }
	id := uuid.New()
	cases := []struct {
		name string
		m    *Menu
		want bool
	}{
		{"nil", nil, false},
		{"empty header", &Menu{Name: "Header", Slug: "header", Location: &header}, true},
		{"header with the blog link", &Menu{Name: "Header", Slug: "header", Location: &header, Items: []*MenuItem{blog()}}, true},
		{"empty footer", &Menu{Name: "Footer", Slug: "footer", Location: &footer}, true},
		{"renamed header", &Menu{Name: "Main", Slug: "header", Location: &header}, false},
		{"other slug", &Menu{Name: "Header", Slug: "mine", Location: &header}, false},
		{"relabelled blog link", &Menu{Name: "Header", Slug: "header", Location: &header, Items: []*MenuItem{{Label: "Weblog", LinkType: LinkTypeArchive, SchemaSlug: &post}}}, false},
		{"custom link", &Menu{Name: "Header", Slug: "header", Location: &header, Items: []*MenuItem{{Label: "Blog", LinkType: LinkTypeCustom}}}, false},
		{"nested blog link", &Menu{Name: "Header", Slug: "header", Location: &header, Items: []*MenuItem{{Label: "Blog", LinkType: LinkTypeArchive, ParentID: &id}}}, false},
		{"a second item", &Menu{Name: "Header", Slug: "header", Location: &header, Items: []*MenuItem{blog(), blog()}}, false},
		{"footer with an item", &Menu{Name: "Footer", Slug: "footer", Location: &footer, Items: []*MenuItem{blog()}}, false},
		{"theme location", &Menu{Name: "Rail", Slug: "rail", Location: &rail}, false},
		{"no location", &Menu{Name: "Header", Slug: "header"}, false},
	}
	for _, c := range cases {
		if got := IsUntouchedCoreSeed(c.m); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
