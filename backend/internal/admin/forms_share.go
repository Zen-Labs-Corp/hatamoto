package admin

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
)

// shareWindowDays is the span of the per-page figures on the Share tab.
const shareWindowDays = 30

// formShareData is the Share tab of the form designer: where the form is used,
// and every way to put it on a page.
type formShareData struct {
	Name, Slug string
	Tabs       []ui.Tab
	Usage      ui.Table
	// Shortcode, Link, Endpoint and Markup are shown with a Copy button each.
	// Link and Endpoint are absolute, from the site's public address.
	Shortcode, Link, Endpoint, Markup string
	Status                            *ui.Badge
	Fields                            int
	// Paused: the form is not Active, so its link answers not found and its
	// shortcode renders nothing.
	Paused bool
	// Captcha: the form uses the spam check widget, which plain HTML cannot show.
	Captcha bool
}

// formPlacements are the places the form is used, as the usage index knows
// them (nil when the index is unavailable).
func (a *Admin) formPlacements(ctx context.Context, f *form.Form) []entity.FormPlacement {
	fn := a.deps.Growth.FormUsage
	if fn == nil {
		return nil
	}
	usage, err := fn(ctx)
	if err != nil {
		a.logger().Error("form usage failed", "form", f.ID.String(), "err", err)
		return nil
	}
	return usage[f.Slug]
}

// formPages are the paths of the published pages the form sits on, plus its
// hosted page while it is Active. They are what the Conversion card and the
// Share tab count visits and submissions for.
func (a *Admin) formPages(ctx context.Context, f *form.Form) []string {
	seen := map[string]bool{}
	if f.Status == form.StatusActive {
		seen[formHostedPath(f)] = true
	}
	for _, p := range a.formPlacements(ctx, f) {
		if path := a.placementPath(ctx, p); path != "" {
			seen[path] = true
		}
	}
	pages := make([]string, 0, len(seen))
	for p := range seen {
		pages = append(pages, p)
	}
	sort.Strings(pages)
	return pages
}

// formHostedPath is the path of the form's own page.
func formHostedPath(f *form.Form) string { return "/f/" + f.Slug }

// landingVisits maps landing path to visits in the span; ok is false when
// traffic is not available (no reader, or it failed).
func (a *Admin) landingVisits(ctx context.Context, from, to time.Time) (map[string]int64, bool) {
	if a.deps.Growth.Traffic == nil {
		return nil, false
	}
	top, err := a.deps.Growth.Traffic.TrafficTop(ctx, analytics.DimLanding, from, to, 0)
	if err != nil {
		a.logger().Error("form share traffic failed", "err", err)
		return nil, false
	}
	visits := make(map[string]int64, len(top))
	for _, t := range top {
		visits[t.K1] += t.Visits
	}
	return visits, true
}

// shareUsageTable lists where the form is used with each page's visits,
// submissions and conversion over the last 30 days. Visits are landing visits
// of the page, not form views, so a page people reach mid-visit can show fewer
// visits than submissions: its conversion then reads as a dash. Areas (a
// footer, a block) and unpublished pages are listed without figures.
func (a *Admin) shareUsageTable(ctx context.Context, fs FormStore, f *form.Form, origin string) ui.Table {
	t := ui.Table{
		ID: "form-usage-table", Caption: "Where this form is used",
		Columns: []ui.Col{
			{Key: "page", Label: "Page"}, {Key: "title", Label: "Title"},
			{Key: "views", Label: "Views, 30 days", Align: "right"}, {Key: "subs", Label: "Submissions", Align: "right"},
			{Key: "conv", Label: "Conversion", Align: "right"},
		},
		Empty: ui.Empty{Icon: "globe", Title: "Not on any page yet", Body: "Add a Form block to a page, or use one of the options below."},
	}
	loc := a.siteLocation(ctx)
	now := analyticsNow().In(loc)
	since := now.AddDate(0, 0, -shareWindowDays)
	subsByPage := map[string]int64{}
	subsOK := false
	if recent, _, _, _, err := scanRecentSubmissions(ctx, fs, f.ID, since, submissionTrackerCap); err != nil {
		a.logger().Error("form share submissions failed", "form", f.ID.String(), "err", err)
	} else {
		subsOK = true
		for _, s := range recent {
			subsByPage[pagePath(s.Metadata.PageURL)]++
		}
	}
	visits, visitsOK := a.landingVisits(ctx, since, now)

	row := func(path, title string) ui.Row {
		views, subs, conv := "—", "—", "—"
		if visitsOK {
			views = fmtNum(visits[path])
		}
		if subsOK {
			subs = fmtNum(subsByPage[path])
		}
		if visitsOK && subsOK && visits[path] > 0 {
			conv = strconv.FormatFloat(float64(subsByPage[path])/float64(visits[path])*100, 'f', 1, 64) + "%"
		}
		return ui.Row{ID: path, Label: title, Cells: []ui.Cell{
			{Text: path, Mono: true}, {Text: title}, {Text: views}, {Text: subs}, {Text: conv},
		}, Actions: []ui.Action{{Label: "Open page", Href: origin + path, NewTab: true, Button: true, AriaLabel: "Open " + path}}}
	}
	other := func(name, note string) ui.Row {
		return ui.Row{ID: name, Label: name, Cells: []ui.Cell{
			{Text: name}, {Text: note}, {Text: "—"}, {Text: "—"}, {Text: "—"},
		}}
	}

	seen := map[string]bool{}
	var pages, rest []ui.Row
	for _, p := range a.formPlacements(ctx, f) {
		if p.Kind == entity.PlacementArea {
			rest = append(rest, other(p.Area, "Shown on every page that uses it"))
			continue
		}
		path := a.placementPath(ctx, p)
		if path == "" {
			rest = append(rest, other(p.Name, "Not published"))
			continue
		}
		if !seen[path] {
			seen[path] = true
			pages = append(pages, row(path, p.Name))
		}
	}
	if hosted := formHostedPath(f); f.Status == form.StatusActive && !seen[hosted] {
		pages = append(pages, row(hosted, "Hosted form page"))
	}
	t.Rows = append(pages, rest...)
	return t
}

// absoluteInsert adds line after the first line of markup (the form tag).
func absoluteInsert(markup, line string) string {
	i := strings.IndexByte(markup, '\n')
	if i < 0 {
		return markup
	}
	return markup[:i+1] + line + "\n" + markup[i+1:]
}

func (a *Admin) renderFormShare(w http.ResponseWriter, r *http.Request, f *form.Form) {
	ctx := r.Context()
	fs, ok := a.formStore(w, r)
	if !ok {
		return
	}
	origin := a.addressOrigin(r)
	endpoint := origin + a.formEndpoint(f.Slug)
	// The pasted form posts to the redirecting no-JS route; the JSON API URL is for scripts.
	markup := absoluteInsert(form.EmbedHTML(f, origin+"/forms/"+f.Slug+"/submit"), `<input type="hidden" name="_redirect" value="`+formHostedPath(f)+`">`)
	fields := 0
	for _, fl := range f.Fields {
		if fl.IsInput() {
			fields++
		}
	}
	d := formShareData{
		Name: f.Name, Slug: f.Slug, Tabs: a.formDesignTabs(ctx, f.ID.String(), "share"),
		Usage:     a.shareUsageTable(ctx, fs, f, origin),
		Shortcode: `[form slug="` + f.Slug + `"]`, Link: origin + formHostedPath(f),
		Endpoint: endpoint, Markup: markup,
		Status: formStatusBadge(f.Status), Fields: fields,
		Paused: f.Status != form.StatusActive, Captcha: f.Settings.EnableCaptcha,
	}
	v := a.NewView(w, r, "Share and embed: "+f.Name)
	v.Crumbs = []Crumb{{Label: "Forms", Href: a.adminPath + "/forms"}, {Label: f.Name, Href: a.adminPath + "/forms/design/" + f.ID.String()}, {Label: "Share and embed"}}
	v.Data = d
	v.Width = "wide"
	a.Render(w, r, http.StatusOK, "forms/share", v)
}
