package render

import (
	"bytes"
	"html/template"
	"strings"
)

// The loop widget lists entities and renders its child nodes once per item.
// Everything it shows comes from DataSource.Query (or, for from:"archive",
// from the items the route already built); bindings inside the item template
// resolve against the item through PageView.scope.
func init() {
	registerWidget("loop", prepLoop)
	registerWidget("loopempty", func(w *widgetCtx) (any, error) {
		w.Class = "hm-loopempty"
		return nil, nil
	})
}

const (
	loopDefaultLimit = 12
	loopMaxLimit     = 48
	// maxLoopsPerRender bounds the queries one render can start: each loop costs
	// several statements, and parts and refs can repeat a loop across the page.
	maxLoopsPerRender = 8
	// loopCanvasItems bounds the preview: item 1 is selectable, the rest plain.
	loopCanvasItems = 3
)

// loopOrders maps the loop's order prop onto QuerySpec.Order.
// pagedHead rebuilds the head of a page whose paged loop is on page N: a self
// canonical for N >= 2 and rel prev/next from the loop. It also reports the
// loop to the server (PagedLoop, LoopOutOfRange). The view itself, and so the
// layout's .Pagination, is left alone: only the head is built from a copy.
func (e *Engine) pagedHead(v *PageView, lp *loopPaged) headView {
	if v.PagedLoop != nil {
		*v.PagedLoop = true
	}
	if v.LoopOutOfRange != nil {
		*v.LoopOutOfRange = lp.Out
	}
	hv := *v
	hv.Pagination = lp.Pagination
	if lp.Page >= 2 {
		hv.Page.CanonicalURL = strings.TrimRight(v.BaseURL, "/") + lp.Pagination.URL(lp.Page)
	}
	return e.headView(&hv)
}

var loopOrders = map[string]string{
	"newest": "-published_at", "oldest": "published_at", "updated": "-updated_at",
	"name": "name", "name_desc": "-name",
}

// loopPaged is what the first paged loop of a page reports for the head step
// (canonical, prev/next) and the server (404 past the last page).
type loopPaged struct {
	Pagination *Pagination
	Page       int
	// Out is set when the request's page is beyond the last one.
	Out bool
}

type loopX struct {
	// Comment replaces the whole widget (a loop inside an item).
	Comment template.HTML
	Items   []template.HTML
	Empty   template.HTML
	Pager   *loopPager
	// NoneBox is the canvas-only "No items match" box.
	NoneBox bool
	// Over is the canvas-only notice for a loop past maxLoopsPerRender.
	Over bool
}

type loopPager struct {
	Prev, Next string
	Links      []PageLink
}

func prepLoop(w *widgetCtx) (any, error) {
	w.Class = "hm-loop"
	w.OwnChildren = true
	v := w.View
	if v.scope != nil { // a loop inside an item (through a ref): never nested
		return loopX{Comment: template.HTML("<!-- hm-loop: nested -->")}, nil // #nosec G203 -- constant
	}
	if v.loops++; v.loops > maxLoopsPerRender {
		if w.Canvas {
			return loopX{Over: true}, nil
		}
		return loopX{Comment: template.HTML("<!-- hm-loop: limit -->")}, nil // #nosec G203 -- constant
	}
	limit := intProp(w.P, "limit", loopDefaultLimit, 1, loopMaxLimit)
	var items []RelatedItem
	var pager *Pagination
	switch str(w.P, "from") {
	case "archive":
		items, pager = v.Items, v.Pagination
	case "source":
		items = v.extItems(str(w.P, "source"), limit)
		if len(items) > limit {
			items = items[:limit]
		}
	default:
		items, pager = w.queryLoop(limit)
	}
	if w.Canvas && len(items) > loopCanvasItems {
		items = items[:loopCanvasItems]
	}
	x := loopX{}
	if len(items) == 0 {
		x.NoneBox = w.Canvas
		for _, ch := range w.N.C {
			if ch != nil && ch.T == "loopempty" {
				var b bytes.Buffer
				w.E.renderNodeIn(&b, w.C, v, ch, w.N, w.Canvas)
				x.Empty = template.HTML(b.String()) // #nosec G203 -- markup of renderNodeIn, escaped there
				break
			}
		}
	}
	for i := range items {
		var b bytes.Buffer
		v.scope = &bindScope{item: &items[i]}
		for _, ch := range w.N.C {
			if ch != nil && ch.T != "loopempty" {
				w.E.renderNodeIn(&b, w.C, v, ch, w.N, w.Canvas && i == 0)
			}
		}
		v.scope = nil
		x.Items = append(x.Items, template.HTML(b.String())) // #nosec G203 -- markup of renderNodeIn, escaped there
	}
	if pager != nil && pager.TotalPages > 1 {
		x.Pager = &loopPager{Prev: pager.PrevURL, Next: pager.NextURL, Links: paginate(pager)}
	}
	return x, nil
}

// queryLoop runs the loop's one query. The first loop with paging "pages" owns
// ?page=N and reports itself on the view; later ones show page 1 and no pager.
func (w *widgetCtx) queryLoop(limit int) ([]RelatedItem, *Pagination) {
	v := w.View
	ds := w.E.opts.Data
	schema := str(w.P, "schema")
	if w.P["context"] == true {
		schema = v.SchemaSlug
	}
	if ds == nil || schema == "" {
		return nil, nil
	}
	// a listing route (v.Pagination) owns ?page= itself: a paged loop in one of its parts must not
	paged := str(w.P, "paging") == "pages" && v.Pagination == nil
	primary := paged && v.loopPaged == nil
	spec := QuerySpec{
		Schema: schema, Term: str(w.P, "term"), Author: str(w.P, "author"),
		Order: loopOrders[str(w.P, "order")], Limit: limit, Page: 1,
		Offset: intProp(w.P, "offset", 0, 0, 1000),
	}
	if spec.Order == "" {
		spec.Order = loopOrders["newest"]
	}
	if w.P["exclude_current"] == true {
		spec.ExcludeID = v.EntityID
	}
	if primary {
		spec.Page = min(max(v.PageNum, 1), MaxQueryPage)
	}
	res := ds.Query(spec)
	if p, ok := ds.(VariantPrefetcher); ok {
		var urls []string
		for _, it := range res.Items {
			if it.Image != "" {
				urls = append(urls, it.Image)
			}
		}
		if len(urls) > 0 {
			p.PrefetchVariants(urls)
		}
	}
	if !primary {
		return res.Items, nil
	}
	page := max(res.Page, 1)
	// no pager link past MaxQueryPage: those URLs would serve the last page again
	pg := NewPagination(v.Path, nil, page, limit, min(max(res.Total-spec.Offset, 0), MaxQueryPage*limit))
	v.loopPaged = &loopPaged{Pagination: pg, Page: page, Out: v.PageNum > max(res.Pages, 1)}
	return res.Items, pg
}
