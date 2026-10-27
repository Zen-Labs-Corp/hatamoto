package render

import (
	"net/url"
	"strconv"
)

// NewPagination describes page of a listing of total items, perPage per page.
// path is the listing's path; query holds the parameters to keep (any "page"
// entry is ignored). page is clamped to 1..TotalPages.
func NewPagination(path string, query url.Values, page, perPage, total int) *Pagination {
	if perPage < 1 {
		perPage = 1
	}
	pages := max((total+perPage-1)/perPage, 1)
	page = min(max(page, 1), pages)
	q := url.Values{}
	for k, v := range query {
		if k != "page" {
			q[k] = v
		}
	}
	p := &Pagination{
		Page: page, PerPage: perPage, Total: total, TotalPages: pages,
		HasPrev: page > 1, HasNext: page < pages,
		Path: path, Query: q.Encode(),
	}
	if p.HasPrev {
		p.PrevURL = p.URL(page - 1)
	}
	if p.HasNext {
		p.NextURL = p.URL(page + 1)
	}
	return p
}

// URL is the root-relative URL of page n; page 1 carries no page parameter.
func (p *Pagination) URL(n int) string {
	u := p.Path
	sep := "?"
	if p.Query != "" {
		u += "?" + p.Query
		sep = "&"
	}
	if n > 1 {
		u += sep + "page=" + strconv.Itoa(n)
	}
	return u
}

// PageLink is one entry of the paginate func: a numbered page or a gap.
type PageLink struct {
	N       int
	URL     string
	Current bool
	// Gap marks an ellipsis between non-adjacent pages (N and URL are empty).
	Gap bool
}

// paginateWindow is how many pages show on each side of the current one.
const paginateWindow = 2

// paginate lists the page links to show: the first and last page, the current
// page and paginateWindow either side, with gaps in between. It accepts a
// *Pagination (nil or a single page gives no links).
func paginate(v any) []PageLink {
	p, ok := v.(*Pagination)
	if !ok || p == nil || p.TotalPages < 2 {
		return nil
	}
	var out []PageLink
	last := 0
	for n := 1; n <= p.TotalPages; n++ {
		if n != 1 && n != p.TotalPages && (n < p.Page-paginateWindow || n > p.Page+paginateWindow) {
			continue
		}
		if last != 0 && n != last+1 {
			out = append(out, PageLink{Gap: true})
		}
		out = append(out, PageLink{N: n, URL: p.URL(n), Current: n == p.Page})
		last = n
	}
	return out
}
