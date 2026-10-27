package render

import (
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxListFunc caps the limit of recent, related and terms.
	maxListFunc = 50
	// maxSrcset bounds the entries of one srcset attribute.
	maxSrcset = 6
	// maxAttrValues caps the limit of attrValues.
	maxAttrValues = 500
)

// dataFuncs are the template funcs backed by the engine's DataSource. They are
// fail-open: with no DataSource every one returns no data.
func dataFuncs(ds DataSource) template.FuncMap {
	return template.FuncMap{
		// recent "post" 5: the newest published entities of a schema.
		"recent": func(schema string, limit int) []RelatedItem {
			if ds == nil {
				return nil
			}
			return ds.Recent(schema, clampLimit(limit))
		},
		// terms "category" 10: the site's terms of a kind with entity counts.
		"terms": func(kind string, limit int) []TermView {
			if ds == nil {
				return nil
			}
			return ds.Terms(kind, clampLimit(limit))
		},
		// menu "header": the items of the menu at a location.
		"menu": func(location string) []NavItem {
			if ds == nil {
				return nil
			}
			return ds.Menu(location)
		},
		// navPos (menu "sidebar") .Path: the current item, its ancestors
		// (Trail) and its depth-first neighbours (Prev, Next).
		"navPos": navPos,
		// query (dict "schema" "blog" "attr" "series_slug" "eq" "x" "author" "jo"
		// "term" "category:news" "order" "-published_at" "limit" 12 "page" 2):
		// a page of published entities of any schema with Items, Total, Page,
		// Pages and Limit. Only "schema" is required.
		"query": func(args map[string]any) QueryResult { return query(ds, args) },
		// attrValues "blog" "category" 50: the distinct non-empty string values
		// of an attribute across a schema's published entities, with counts,
		// sorted by value.
		"attrValues": func(schema, attr, limit any) []AttrCount {
			if ds == nil {
				return nil
			}
			return ds.AttrValues(strings.TrimSpace(toStr(schema)), strings.TrimSpace(toStr(attr)), min(max(toInt(limit), 1), maxAttrValues))
		},
		// related . 3: other published entities of the page's schema.
		"pageNum": pageNum,
		// queryParam . "name": the first value of a request query parameter,
		// capped at maxParamLen bytes, "" when absent.
		"queryParam": queryParam,
		"related":    related,
		"paginate":   paginate,
		// srcset .Entity.Image: an srcset value for a local media URL, "" when
		// there are no resized copies.
		"srcset": func(src any) string { return srcset(ds, strVal(src)) },
	}
}

// query builds a QuerySpec from the dict of the query func and runs it. Limit
// and page are clamped (defaults 12 and 1); a missing schema or DataSource
// gives an empty result with Page and Pages 1.
func query(ds DataSource, args map[string]any) QueryResult {
	q := QuerySpec{
		Schema: strings.TrimSpace(toStr(args["schema"])),
		Attr:   strings.TrimSpace(toStr(args["attr"])),
		Author: strings.TrimSpace(toStr(args["author"])),
		Term:   strings.TrimSpace(toStr(args["term"])),
		Order:  strings.TrimSpace(toStr(args["order"])),
		Limit:  DefaultQueryLimit,
		Page:   min(max(toInt(args["page"]), 1), MaxQueryPage),
	}
	if v, ok := args["eq"]; ok {
		if v == nil { // e.g. .Attrs.missing: nothing can equal it
			return QueryResult{Page: 1, Pages: 1, Limit: q.Limit}
		}
		q.EqSet = true
		if b, isBool := v.(bool); isBool {
			q.Eq = strconv.FormatBool(b)
		} else {
			q.Eq = toStr(v)
		}
	}
	switch toStr(args["status"]) {
	case "", "published":
	case "published+scheduled":
		q.IncludeScheduled = true
	}
	if n := toInt(args["limit"]); n > 0 {
		q.Limit = min(n, MaxQueryLimit)
	}
	if ds == nil || q.Schema == "" {
		return QueryResult{Page: 1, Pages: 1, Limit: q.Limit}
	}
	r := ds.Query(q)
	r.Limit = q.Limit
	r.Page, r.Pages = max(r.Page, 1), max(r.Pages, 1)
	return r
}

// pageNum is the page number of the request (?page=, 1..MaxQueryPage, default
// 1); v is the page data (the template's "." or "$").
func pageNum(v any) int {
	n := 0
	switch p := v.(type) {
	case *preparedSection:
		n = p.Page.PageNum
	case preparedSection:
		n = p.Page.PageNum
	case *pageData:
		n = p.PageNum
	case pageData:
		n = p.PageNum
	case *PageView:
		n = p.PageNum
	case PageView:
		n = p.PageNum
	}
	return min(max(n, 1), MaxQueryPage)
}

// maxParamLen caps what queryParam returns.
const maxParamLen = 200

// queryParam is the first value of the request's query parameter name, cut to
// maxParamLen bytes at a rune boundary; v is the page data or a section ("."
// or "$"). Absent or unknown data gives "".
func queryParam(v any, name string) string {
	var q url.Values
	switch p := v.(type) {
	case *pageData:
		q = p.Params
	case pageData:
		q = p.Params
	case *PageView:
		q = p.Params
	case PageView:
		q = p.Params
	case *preparedSection:
		q = p.Page.Params
	case preparedSection:
		q = p.Page.Params
	}
	return strings.ToValidUTF8(truncateUTF8(q.Get(name), maxParamLen), "")
}

// truncateUTF8 cuts s to at most n bytes (the cut may split a rune; callers
// drop the invalid tail).
func truncateUTF8(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clampLimit(n int) int { return min(max(n, 1), maxListFunc) }

// related returns up to limit related items for a page; v is the page data
// (the template's "." or "$").
func related(v any, limit int) []RelatedItem {
	var fn func(int) []RelatedItem
	switch p := v.(type) {
	case *pageData:
		fn = p.Related
	case pageData:
		fn = p.Related
	case *PageView:
		fn = p.Related
	case PageView:
		fn = p.Related
	}
	if fn == nil {
		return nil
	}
	return fn(clampLimit(limit))
}

// srcset builds "url 300w, url 1024w" from the variants of a local media URL.
// Remote and unknown URLs give "": only copies that exist are listed.
func srcset(ds DataSource, src string) string {
	src = imgSrc(src)
	if ds == nil || !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "http") {
		return ""
	}
	vs := ds.ImageVariants(src)
	if len(vs) == 0 {
		return ""
	}
	if len(vs) > maxSrcset {
		vs = vs[:maxSrcset]
	}
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		if v.Width > 0 && imgSrc(v.URL) != "" {
			parts = append(parts, fmt.Sprintf("%s %dw", strings.ReplaceAll(v.URL, ",", "%2C"), v.Width))
		}
	}
	return strings.Join(parts, ", ")
}
