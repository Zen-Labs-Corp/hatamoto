package server

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
)

var pageRobotsRe = regexp.MustCompile(`<meta name="robots" content="([^"]*)"`)

// One predicate table drives the page head, the sitemap, feeds and llms.txt: an
// entity is either kept out of all of them or listed in all of them.
func TestNoIndexPredicateAgreesAcrossConsumers(t *testing.T) {
	t.Parallel()
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	typeNoIndex := &seo.Settings{Index: seo.Index{NoIndexTypes: map[string]bool{"post": true}}}
	cases := []struct {
		name     string
		settings *seo.Settings
		schema   string
		metadata string
		attrs    string
		want     bool // noindex
	}{
		{"indexed", nil, "post", `{}`, `{}`, false},
		{"indexed with settings for another type", typeNoIndex, "page", `{}`, `{}`, false},
		{"type noindex", typeNoIndex, "post", `{}`, `{}`, true},
		{"metadata no_index", nil, "post", `{"no_index":true}`, `{}`, true},
		{"attributes no_index", nil, "post", `{}`, `{"no_index":true}`, true},
		{"seo robots noindex,nofollow", nil, "post", `{}`, `{"seo":{"robots":"noindex,nofollow"}}`, true},
		{"seo robots noindex, follow", nil, "post", `{}`, `{"seo":{"robots":"noindex, follow"}}`, true},
		{"seo robots nofollow only", nil, "post", `{}`, `{"seo":{"robots":"nofollow"}}`, false},
	}
	pub := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ent := &entity.Entity{
				ID: uuid.New(), Name: "x", Slug: "x", Status: entity.StatusPublished,
				Metadata: []byte(tc.metadata), Attributes: []byte(tc.attrs), PublishedAt: &pub,
				Schema: &entity.EntitySchema{Slug: tc.schema},
			}

			// Head: the route result the loader builds, rendered through the engine.
			meta := PageMeta{NoIndex: seo.EntryNoIndex(ent.Metadata, ent.Attributes)}
			v := pageViewFromRoute(&routeResult{Page: &PublicPageResponse{Meta: meta, Entity: ent}}, "https://x.io", "/x")
			v.Kind, v.SEO = render.KindSingle, tc.settings
			var buf bytes.Buffer
			if err := engine.RenderPage(&buf, v); err != nil {
				t.Fatal(err)
			}
			m := pageRobotsRe.FindStringSubmatch(buf.String())
			if got := m != nil && strings.Contains(m[1], "noindex"); got != tc.want {
				t.Errorf("head noindex = %v, want %v (robots %q)", got, tc.want, m)
			}

			// Sitemap: the SQL listing flags the entry (NoIndexSQL); the builder
			// adds the type switch.
			entry := entry(tc.schema, "x", "2025-03-01", func(e *entity.SitemapEntry) {
				e.NoIndex = seo.EntryNoIndex(ent.Metadata, ent.Attributes)
				e.IncludeInLLMs = true
			})
			snap := sitemapSnap(t, sitemapSchema("page", ""), sitemapSchema("post", ""))
			in := basePlanInput(snap, entry)
			in.SEO = tc.settings
			plan := planSitemap(in)
			listed := false
			for _, key := range []string{"blog", "post", "misc"} {
				if urls, ok := plan.GroupURLs(key); ok && len(urls) > 0 {
					listed = true
				}
			}
			if listed == tc.want {
				t.Errorf("sitemap listed = %v, want %v", listed, !tc.want)
			}

			// Feeds.
			items := feedItems([]*entity.Entity{ent}, "https://x.io", func(e *entity.Entity) string { return "/x" }, nil, nil, tc.settings)
			if (len(items) == 1) == tc.want {
				t.Errorf("feed listed = %v, want %v", len(items) == 1, !tc.want)
			}

			// llms.txt and the knowledge graph.
			if got := len(llmsEntries([]*entity.SitemapEntry{entry}, tc.settings)) == 1; got == tc.want {
				t.Errorf("llms listed = %v, want %v", got, !tc.want)
			}
		})
	}
}
