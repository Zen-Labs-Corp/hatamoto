package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// staticSEO serves fixed SEO settings to the list; nothing else is used.
type staticSEO struct {
	SEOService
	set seo.Settings
}

func (s staticSEO) Get(context.Context) (seo.Settings, error) { return s.set, nil }

func TestEntitiesSEOViewGolden(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.seedAttrs(11, `{"summary":"The first","meta_title":"Alpha for search","meta_description":"A short one","no_index":true}`)
	body := c.get(t, "owner-token", "/admin/entities?view=seo", "entities-list-seo")
	has(t, body,
		">Name<", ">SEO title<", ">Description<", ">Keyword<", ">Noindex<",
		`for="seo-`+fid(11).String()+`-title">SEO title for Alpha service</label>`, `value="Alpha for search"`,
		`name="orig_title" value="Alpha for search" form="seo-`+fid(11).String()+`"`,
		`SEO description for Alpha service`, `A short one</textarea>`, `11 / 160`, `16 / 60`,
		`role="switch" id="seo-`+fid(11).String()+`-noindex" name="noindex" value="true" form="seo-`+fid(11).String()+`" checked`,
		`<form class="hm-cell-edit hm-cell-edit--save" id="seo-`+fid(11).String()+`" method="post" action="/admin/entities/`+fid(11).String()+`/seo">`,
		`name="back" value="/admin/entities?view=seo"`, `data-hm-savestate`,
		`aria-label="Columns"`, `href="/admin/entities"`, `aria-current="page">SEO`,
	)
	lacks(t, body, `aria-label="Select all rows`, `data-hm-select-all`)
	// Types without the writing view have no keyword to edit.
	lacks(t, body, `name="keyword"`)
	// The default view keeps its columns and gains only the toggle.
	def := c.get(t, "owner-token", "/admin/entities", "")
	has(t, def, `aria-label="Select all rows in Entities"`, `aria-label="Columns"`, `aria-current="page">Default`)
	lacks(t, def, `name="orig_title"`, `name="view"`)
	// An editor sees the same view.
	has(t, c.get(t, "editor-token", "/admin/entities?view=seo", ""), `name="orig_title"`)
}

func TestEntitiesSEOViewPlaceholdersAndHome(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.a.deps.SEO = staticSEO{set: seo.Settings{Separator: "|", Home: "Welcome %sep% %sitename%", Types: map[string]seo.TypeTemplates{"service": {Title: "%title% %sep% %sitename%", Description: "About %title%"}}}}
	site := c.a.deps.Site.(*fakeSite)
	site.cfg.CompanyName = "Example Co"
	site.cfg.HomepageEntityID = fid(11)
	c.seedAttrs(12, `{"summary":"The second","meta_title":"Typed title"}`)
	body := c.get(t, "owner-token", "/admin/entities?view=seo", "")
	a, b := fid(11).String(), fid(12).String()
	has(t, body,
		`id="seo-`+a+`-title" name="title" form="seo-`+a+`" placeholder="Welcome | Example Co"`,
		`id="seo-`+b+`-title" name="title" form="seo-`+b+`" value="Typed title" placeholder="Beta service | Example Co"`,
		`placeholder="About Beta service"`,
	)
}

func TestEntitiesSEOViewKeepsViewAcrossFiltersSortAndPages(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	for i := 0; i < 25; i++ {
		c.ents.addEntity(200+i, c.recipe, fmt.Sprintf("Recipe %02d", i), fmt.Sprintf("recipe-%02d", i), entity.StatusDraft, `{}`, t0.Add(10*time.Hour))
	}
	body := c.get(t, "owner-token", "/admin/entities?view=seo&type=recipe&sort=name&dir=asc", "")
	has(t, body, `name="view" value="seo"`, `Showing 1–20 of 25`)
	for _, class := range []string{`hm-pagination__link`, `hm-table__sort`} {
		links := regexp.MustCompile(`class="`+class+`" href="([^"]*)"`).FindAllStringSubmatch(body, -1)
		if len(links) == 0 {
			t.Errorf("no %s links found", class)
		}
		for _, l := range links {
			if !strings.Contains(l[1], "view=seo") {
				t.Errorf("%s link lost the view: %s", class, l[1])
			}
		}
	}
	// The toggle keeps the filters and drops the page.
	body = c.get(t, "owner-token", "/admin/entities?view=seo&type=recipe&page=2", "")
	has(t, body, `href="/admin/entities?type=recipe"`, `href="/admin/entities?type=recipe&amp;view=seo"`)
	// A chip's remove link keeps the view.
	body = c.get(t, "owner-token", "/admin/entities?view=seo&from=2026-03-01", "")
	has(t, body, `href="/admin/entities?view=seo"`)
	// Anything but "seo" is the default view.
	lacks(t, c.get(t, "owner-token", "/admin/entities?view=other", ""), `name="orig_title"`)
}

func TestEntitiesSEOViewKeywordOnlyForWritingView(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	r.seedAttrs(80, `{"content":"<p>Hi</p>","seo":{"focus_keyword":"go servers"}}`)
	body := r.get(t, "owner-token", "/admin/entities?view=seo", "")
	id := r.ent.ID.String()
	has(t, body, `id="seo-`+id+`-keyword" name="keyword" form="seo-`+id+`" value="go servers"`, `name="orig_keyword" value="go servers"`, `Focus keyword for Go servers`)
	if n := strings.Count(body, `name="keyword"`); n != 1 {
		t.Errorf("keyword inputs = %d, want 1 (only the post)", n)
	}
}
