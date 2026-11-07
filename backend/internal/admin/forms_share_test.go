package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

func TestShareTabCopyAndMarkup(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t, func(d *Deps) {
		d.BaseURL = func(*http.Request) string { return "https://site.test" }
	})
	fm := ff.addForm("Contact", contactFields...)
	body := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/share", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`value="[form slug=&#34;contact&#34;]"`,
		`value="https://site.test/f/contact"`,
		`value="https://site.test/api/v1/public/forms/contact/submit"`,
		`data-hm-copy-field="share-shortcode"`, `data-hm-copy-field="share-link"`,
		`data-hm-copy-field="share-endpoint"`, `data-hm-copy-field="share-html"`,
		`action=&#34;https://site.test/api/v1/public/forms/contact/submit&#34;`, `name=&#34;hm_website&#34;`, `name=&#34;_redirect&#34;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("share tab lacks %q", want)
		}
	}
	if strings.Contains(body, "This form is paused") {
		t.Error("an active form is described as paused")
	}

	fm.Status = form.StatusInactive
	body = g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/share", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "This form is paused") {
		t.Error("a paused form does not say so")
	}
}

func TestShareUsageTable(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	fm := ff.addForm("Contact", contactFields...)
	now := time.Now()
	sub := func(page string) {
		s := ff.addSub(fm, form.SubmissionStatusRead, now.Add(-time.Hour), map[string]any{"name": "A"})
		s.Metadata.PageURL = page
	}
	sub("https://site.test/contact")
	sub("https://site.test/contact?x=1")
	sub("https://site.test/other")

	a := &Admin{adminPath: "/admin"}
	a.deps.Growth.Traffic = &landingTraffic{rows: []analytics.TrafficTop{
		{K1: "/contact", Counts: analytics.Counts{Visits: 8}},
		{K1: "/f/contact", Counts: analytics.Counts{Visits: 0}},
	}}
	a.deps.Content.PublicPath = func(_ context.Context, e *entity.Entity) string { return "/" + e.Slug }
	a.deps.Growth.FormUsage = pageUsage("contact", "contact")

	tbl := a.shareUsageTable(context.Background(), ff, fm, "https://site.test")
	rows := map[string][]string{}
	for _, r := range tbl.Rows {
		var cells []string
		for _, c := range r.Cells {
			cells = append(cells, c.Text)
		}
		rows[cells[0]] = cells
	}
	if got := strings.Join(rows["/contact"], "|"); got != "/contact|Page|8|2|25.0%" {
		t.Errorf("/contact row = %q", got)
	}
	if got := strings.Join(rows["/f/contact"], "|"); got != "/f/contact|Hosted form page|0|0|—" {
		t.Errorf("hosted row = %q", got)
	}
	if got := strings.Join(rows["Footer"], "|"); got != "Footer|Shown on every page that uses it|—|—|—" {
		t.Errorf("area row = %q", got)
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(tbl.Rows))
	}

	// Without a traffic reader views and conversion are dashes, submissions still count.
	a.deps.Growth.Traffic = nil
	tbl = a.shareUsageTable(context.Background(), ff, fm, "")
	if c := tbl.Rows[0].Cells; c[2].Text != "—" || c[3].Text != "2" || c[4].Text != "—" {
		t.Errorf("no traffic: %+v", c)
	}
}
