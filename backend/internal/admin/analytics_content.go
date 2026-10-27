package admin

import (
	"context"
	"net/url"
	"strconv"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/agents"
)

// contentPage is the body of the Content section.
type contentPage struct {
	Lede string
	Hero ui.AiaChart
	// Coverage: published pages fetched at least once, as of today.
	Coverage     *ui.CategoryBar
	CoverageNote string
	NeverByType  ui.BarList
	// First fetch: time from publish to the first fetch, and who fetched first.
	FirstFetch *ui.CategoryBar
	FirstBy    ui.BarList
	TopPages   ui.BarList
	FirstCrawl ui.AiaChart
	Never      ui.AiaRows
}

func (p *contentPage) lede() string { return p.Lede }

// pageTypeLabel is the display name of a page type slug; nil is "Other pages".
func pageTypeLabel(slug *string) string {
	if slug == nil {
		return otherPages
	}
	return schemaLabel(*slug)
}

func (a *Admin) buildContent(ctx context.Context, q url.Values, rg agents.Range, cmp bool) (any, error) {
	npage := atoiMin(q.Get("npage"), 1)
	pv, err := a.pagesData(ctx, rg, 1, npage)
	if err != nil {
		return nil, err
	}
	fc, err := a.firstCrawlsData(ctx, rg, 1)
	if err != nil {
		return nil, err
	}
	published, never := pv.PublishedTotal, pv.NeverN
	fetched := int64(published - never)
	unit := per(rg)

	p := &contentPage{
		Lede: strconv.FormatInt(fetched, 10) + " of " + strconv.Itoa(published) + " published pages have been fetched at least once since tracking began.",
		Hero: ui.AiaChart{ID: "w-ct-hero", Title: "Fetches by page type", Description: "What AI agents read, grouped by the type of page.",
			Src: a.dataURL("pages.json", rg, cmp), Chart: "by_type", Hero: true, Compare: cmp,
			Table: pv.typeM.table("AI fetches per day by page type", "Day")},
		FirstCrawl: ui.AiaChart{ID: "w-ct-first", Title: "First crawls", Description: "Pages fetched for the first time, per " + unit + ".",
			Src: a.dataURL("first-crawls.json", rg, false), Chart: "per_day", Cols: true, Table: fc.table},
	}

	// Coverage: how many published pages have been fetched, and what the rest are.
	if published > 0 {
		seg := func(label, kind string, n int64) ui.CategorySegment {
			return ui.CategorySegment{Label: label, Kind: kind, N: float64(n), Value: fmtNum(n), Share: share(n, int64(published))}
		}
		p.Coverage = &ui.CategoryBar{Label: "Published pages by fetch status", Columns: true,
			Segments: []ui.CategorySegment{seg("Fetched at least once", "series-1", fetched), seg("Never fetched", "series-6", int64(never))}}
		p.CoverageNote = strconv.FormatInt(fetched, 10) + " of " + strconv.Itoa(published) + " published pages have been fetched by at least one AI agent since tracking began. This is the state today, not filtered by the date range."
	}
	p.NeverByType = ui.BarList{Label: "Never fetched pages by page type", Quiet: true}
	for _, n := range pv.NeverByType {
		p.NeverByType.Items = append(p.NeverByType.Items, ui.BarItem{Label: n.Label, Value: fmtNum(int64(n.Count)), N: float64(n.Count)})
	}

	// Time to first fetch: the buckets are the whole range, not the page of items.
	var firsts int
	for _, b := range fc.Buckets {
		firsts += b.Count
	}
	if firsts > 0 {
		kinds := map[string]string{"Same day": "series-1", "1 to 3 days": "series-2", "4 to 7 days": "series-3", "8 days or more": "series-5", "Not recorded": "series-6"}
		p.FirstFetch = &ui.CategoryBar{Label: "Time from publish to first fetch", Columns: true}
		for _, b := range fc.Buckets {
			p.FirstFetch.Segments = append(p.FirstFetch.Segments, ui.CategorySegment{Label: b.Label, Kind: kinds[b.Label], N: float64(b.Count),
				Value: fmtNum(int64(b.Count)), Share: share(int64(b.Count), int64(firsts))})
		}
	}
	p.FirstBy = ui.BarList{Label: "Pages first fetched, by agent", Quiet: true}
	for i, ag := range fc.ByAgent {
		if i == 4 {
			break
		}
		p.FirstBy.Items = append(p.FirstBy.Items, ui.BarItem{Label: ag.Name, Value: fmtNum(int64(ag.Count)), N: float64(ag.Count)})
	}

	p.TopPages = ui.BarList{Label: "The ten pages AI agents fetched most", Quiet: true, Mono: true}
	for i, pg := range pv.Top {
		if i == 10 {
			break
		}
		p.TopPages.Items = append(p.TopPages.Items, ui.BarItem{Label: pg.Path, Note: pageTypeLabel(pg.SchemaSlug), Value: fmtNum(pg.Hits), N: float64(pg.Hits)})
	}

	p.Never = ui.AiaRows{Caption: "Published pages no AI agent has fetched", Columns: []string{"Page", "Page type", "Published"}, Empty: "No data for this period.",
		Page: ui.Pagination{Page: npage, PerPage: analyticsPerPage, Total: never, BaseURL: a.adminPath + "/analytics/content", Param: "npage", Query: analyticsViewCarry(q)}}
	for _, n := range pv.Never {
		pub := ui.AiaCell{Text: "Not recorded", Muted: true}
		if n.PublishedAt != nil {
			pub = ui.AiaCell{Text: fmtDate(*n.PublishedAt)}
		}
		p.Never.Rows = append(p.Never.Rows, []ui.AiaCell{{Text: n.Name, Mono: n.Slug}, {Text: pageTypeLabel(n.SchemaSlug)}, pub})
	}
	return p, nil
}
