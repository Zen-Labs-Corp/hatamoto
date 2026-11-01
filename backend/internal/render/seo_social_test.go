package render

import (
	"testing"
	"time"

	"hatamoto.dev/core/internal/seo"
)

func metaValue(list []metaPair, key string) (string, bool) {
	for _, m := range list {
		if m.Key == key {
			return m.Content, true
		}
	}
	return "", false
}

func socialSettings(mut func(*seo.Social)) *seo.Settings {
	s := seo.Defaults()
	mut(&s.Social)
	return &s
}

func TestSocialHeadImagesAndCard(t *testing.T) {
	t.Parallel()
	site := func(s *seo.Social) { s.Image = "/site.png" }
	typed := func(s *seo.Social) {
		s.Image = "/site.png"
		s.Types = map[string]seo.SocialType{"doc": {Image: "/type.png"}}
	}
	summary := func(s *seo.Social) { s.Image = "/site.png"; s.Card = "summary" }
	cases := []struct {
		name     string
		page     PageMeta
		mut      func(*seo.Social)
		wantImg  string
		wantCard string
	}{
		{"site default is absolute and large (A1)", PageMeta{Name: "N"}, site, "https://x.io/site.png", "summary_large_image"},
		{"type default beats site default", PageMeta{Name: "N"}, typed, "https://x.io/type.png", "summary_large_image"},
		{"entry image beats type default, kept as stored", PageMeta{Name: "N", OGImage: "/own.png"}, typed, "/own.png", "summary_large_image"},
		{"card summary forced", PageMeta{Name: "N"}, summary, "https://x.io/site.png", "summary"},
		{"no image no card upgrade", PageMeta{Name: "N"}, func(s *seo.Social) { s.SiteTags = true }, "", "summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := seoHead(seoView(tc.page, socialSettings(tc.mut)), false)
			img, _ := metaValue(h.OG, "og:image")
			if img != tc.wantImg {
				t.Errorf("og:image %q, want %q", img, tc.wantImg)
			}
			if tw, _ := metaValue(h.Twitter, "twitter:image"); tw != tc.wantImg {
				t.Errorf("twitter:image %q, want %q", tw, tc.wantImg)
			}
			if h.Twitter[0].Key != "twitter:card" || h.Twitter[0].Content != tc.wantCard {
				t.Errorf("card %v, want %s", h.Twitter[0], tc.wantCard)
			}
		})
	}
}

func TestSocialHeadTitleAndDescription(t *testing.T) {
	t.Parallel()
	set := socialSettings(func(s *seo.Social) {
		s.Types = map[string]seo.SocialType{"doc": {Title: "%title% %sep% Docs"}}
	})
	h := seoHead(seoView(PageMeta{Name: "Install", Title: "Install", Description: "meta"}, set), false)
	if h.Title != "Install | Site" {
		t.Fatalf("<title> must not change: %q", h.Title)
	}
	for _, k := range []string{"og:title"} {
		if got, _ := metaValue(h.OG, k); got != "Install | Docs" {
			t.Errorf("%s %q", k, got)
		}
	}
	if got, _ := metaValue(h.Twitter, "twitter:title"); got != "Install | Docs" {
		t.Errorf("twitter:title %q", got)
	}

	// A hand-written social title and description win (A3).
	page := PageMeta{Name: "Install", Title: "Install", Description: "meta", OGTitle: "Mine", OGDescription: "Mine too"}
	h = seoHead(seoView(page, set), false)
	if got, _ := metaValue(h.OG, "og:title"); got != "Mine" {
		t.Errorf("og:title %q", got)
	}
	if got, _ := metaValue(h.Twitter, "twitter:description"); got != "Mine too" {
		t.Errorf("twitter:description %q", got)
	}
	if got, _ := metaValue(h.OG, "og:description"); got != "Mine too" {
		t.Errorf("og:description %q", got)
	}

	// Hand-written text also applies with no saved social settings.
	def := seo.Defaults()
	h = seoHead(seoView(PageMeta{Name: "N", OGTitle: "Mine"}, &def), false)
	if got, _ := metaValue(h.OG, "og:title"); got != "Mine" {
		t.Errorf("og:title %q", got)
	}
}

func TestSocialHeadSiteTags(t *testing.T) {
	t.Parallel()
	on := socialSettings(func(s *seo.Social) { s.SiteTags = true })
	v := seoView(PageMeta{Name: "N"}, on)
	v.Kind, v.EntityID = KindHome, ""
	v.Site.Language = "en-US"
	h := seoHead(v, false)
	if got, _ := metaValue(h.OG, "og:site_name"); got != "Site" {
		t.Errorf("og:site_name %q", got)
	}
	if got, _ := metaValue(h.OG, "og:locale"); got != "en_US" {
		t.Errorf("og:locale %q", got)
	}
	v.Locale = "pt-BR"
	if got, _ := metaValue(seoHead(v, false).OG, "og:locale"); got != "pt_BR" {
		t.Errorf("og:locale %q", got)
	}

	off := socialSettings(func(s *seo.Social) { s.Articles = true })
	v = seoView(PageMeta{Name: "N"}, off)
	h = seoHead(v, false)
	for _, k := range []string{"og:site_name", "og:locale"} {
		if _, ok := metaValue(h.OG, k); ok {
			t.Errorf("%s emitted with the switch off", k)
		}
	}
}

func TestSocialHeadArticles(t *testing.T) {
	t.Parallel()
	pub := time.Date(2026, 3, 1, 10, 0, 0, 0, time.FixedZone("x", 3600))
	upd := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	on := socialSettings(func(s *seo.Social) { s.Articles = true; s.FBPage = "https://example.com/pg" })
	post := func(schema string, kind string, set *seo.Settings) *PageView {
		v := seoView(PageMeta{Name: "N"}, set)
		v.SchemaSlug, v.Kind = schema, kind
		v.Entity = &EntityView{PublishedAt: pub, UpdatedAt: upd}
		return v
	}

	h := seoHead(post("post", KindSingle, on), false)
	for k, want := range map[string]string{
		"og:type":                "article",
		"article:published_time": "2026-03-01T09:00:00Z",
		"article:modified_time":  "2026-03-02T10:00:00Z",
		"article:publisher":      "https://example.com/pg",
	} {
		if got, _ := metaValue(h.OG, k); got != want {
			t.Errorf("%s %q, want %q", k, got, want)
		}
	}

	// A doc stays a website; an archive of the post schema never becomes an article (A4).
	for _, v := range []*PageView{post("doc", KindSingle, on), post("post", KindArchive, on)} {
		h := seoHead(v, false)
		if got, _ := metaValue(h.OG, "og:type"); got != "website" {
			t.Errorf("%s/%s og:type %q", v.SchemaSlug, v.Kind, got)
		}
		if _, ok := metaValue(h.OG, "article:published_time"); ok {
			t.Errorf("%s/%s emitted article tags", v.SchemaSlug, v.Kind)
		}
	}

	// Switch off: no article tags on a post.
	h = seoHead(post("post", KindSingle, socialSettings(func(s *seo.Social) { s.FBPage = "https://example.com/pg" })), false)
	if got, _ := metaValue(h.OG, "og:type"); got != "website" {
		t.Errorf("og:type %q", got)
	}

	// A set OGType still wins.
	v := post("post", KindSingle, on)
	v.Page.OGType = "product"
	if got, _ := metaValue(seoHead(v, false).OG, "og:type"); got != "product" {
		t.Errorf("og:type %q", got)
	}

	// Falls back to the view times when the entity has none.
	v = post("post", KindSingle, on)
	v.Entity = nil
	v.PublishedAt = pub
	h = seoHead(v, false)
	if got, _ := metaValue(h.OG, "article:published_time"); got != "2026-03-01T09:00:00Z" {
		t.Errorf("published %q", got)
	}
	if _, ok := metaValue(h.OG, "article:modified_time"); ok {
		t.Error("zero modified time must be skipped")
	}
}

func TestSocialHeadOptionalIDsOnlyWhenFilled(t *testing.T) {
	t.Parallel()
	h := seoHead(seoView(PageMeta{Name: "N"}, socialSettings(func(s *seo.Social) { s.SiteTags = true })), false)
	if _, ok := metaValue(h.OG, "fb:app_id"); ok {
		t.Error("fb:app_id without a value")
	}
	if _, ok := metaValue(h.Twitter, "twitter:site"); ok {
		t.Error("twitter:site without a value")
	}
	h = seoHead(seoView(PageMeta{Name: "N"}, socialSettings(func(s *seo.Social) { s.FBAppID = "42"; s.TwitterSite = "@me" })), false)
	if got, _ := metaValue(h.OG, "fb:app_id"); got != "42" {
		t.Errorf("fb:app_id %q", got)
	}
	if got, _ := metaValue(h.Twitter, "twitter:site"); got != "@me" {
		t.Errorf("twitter:site %q", got)
	}
}
