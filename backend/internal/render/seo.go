package render

import (
	"context"
	"encoding/json"
	"html/template"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/seo"
)

// metaPair is one <meta> tag, keyed by name= or property=.
type metaPair struct {
	Key     string
	Content string
}

// headView is the data the "head" partial renders. It is built by buildHeadView
// from a PageView (plus any head filter output) and holds only escaped-on-render
// strings, except Scripts and Comment which the host constructs itself from
// marshaled JSON / sanitised text (never from raw tenant or plugin strings).
type headView struct {
	Title       string
	Description string
	Canonical   string
	Robots      string
	// Prev and Next are the absolute rel="prev"/"next" URLs of a paginated listing.
	Prev, Next string
	// Alt are the hreflang alternates, x-default last. Nil on a one-locale page.
	Alt     []altLink
	OG      []metaPair
	Feeds   []FeedLink
	Twitter []metaPair
	// Meta are extra <meta name> tags, such as geo.region on a locale page.
	Meta []metaPair
	Ext  []extTag
	// Scripts are complete <script type="application/ld+json"> elements.
	Scripts []template.HTML
	// Comment is an optional AI-crawler HTML comment.
	Comment template.HTML
}

// altLink is one <link rel="alternate" hreflang> entry.
type altLink struct {
	Lang string
	Href string
}

// hreflangLinks builds the hreflang entries: every alternate that has a copy,
// then x-default. A paginated page (its canonical is the page itself) gets none.
func hreflangLinks(v *PageView) []altLink {
	if len(v.Alternates) == 0 || (v.Pagination != nil && v.Pagination.Page >= 2) {
		return nil
	}
	alt := make([]altLink, 0, len(v.Alternates)+1)
	for _, a := range v.Alternates {
		if !a.Missing {
			alt = append(alt, altLink{Lang: a.Lang, Href: a.Href})
		}
	}
	if v.XDefaultHref != "" {
		alt = append(alt, altLink{Lang: "x-default", Href: v.XDefaultHref})
	}
	return alt
}

// seoHead builds the SEO-derived part of the head from the page view. With
// replaceSiteGraph the generic Organization/WebSite graph is omitted because
// a head filter supplies its own.
func seoHead(v *PageView, replaceSiteGraph bool) headView {
	return seoHeadWith(context.Background(), v, replaceSiteGraph, nil)
}

// seoHeadWith is seoHead with the render.site_graph filter, which extends the
// core graph unless a head filter replaced it.
func seoHeadWith(ctx context.Context, v *PageView, replaceSiteGraph bool, sg SiteGraphFilter) headView {
	h := headView{}

	rawTitle := firstNonEmpty(v.Page.Title, v.Page.Name, v.Site.Name)
	h.Title = titleWithSite(rawTitle, v.Site.Name) // "<title> | <site>", like the Next.js title template
	h.Description = v.Page.Description
	if v.SEO != nil {
		applySEOTemplates(&h, v, rawTitle)
	}
	h.Canonical = firstNonEmpty(v.Page.CanonicalURL, strings.TrimRight(v.BaseURL, "/")+v.Path)
	h.Alt = hreflangLinks(v)
	if v.Page.NoIndex || v.Preview {
		h.Robots = "noindex,follow"
	}

	if p := v.Pagination; p != nil {
		if p.HasPrev {
			h.Prev = absURL(v.BaseURL, p.PrevURL)
		}
		if p.HasNext {
			h.Next = absURL(v.BaseURL, p.NextURL)
		}
	}

	for _, f := range v.Site.Feeds {
		f.Href = absURL(v.BaseURL, f.Href)
		h.Feeds = append(h.Feeds, f)
	}

	socialHead(&h, v, rawTitle)

	for _, raw := range v.JSONLD {
		if s, ok := jsonLDScript(raw); ok {
			h.Scripts = append(h.Scripts, s)
		}
	}
	if len(v.Breadcrumb) > 0 {
		if s, ok := marshalLDScript(breadcrumbLD(v)); ok {
			h.Scripts = append(h.Scripts, s)
		}
	}
	if v.Kind == KindArchive {
		if s, ok := marshalLDScript(collectionPageLD(v, h)); ok {
			h.Scripts = append(h.Scripts, s)
		}
	}
	if !replaceSiteGraph {
		core := siteGraphLD(v)
		s, ok := marshalLDScript(extendSiteGraph(ctx, sg, v, core))
		if !ok && sg != nil {
			s, ok = marshalLDScript(core) // a filter answer that cannot be encoded: keep the core graph
		}
		if ok {
			h.Scripts = append(h.Scripts, s)
		}
	}

	if c := commentText(v.AICrawlerComment); c != "" {
		h.Comment = template.HTML("<!-- " + c + " -->") // #nosec G203 -- commentText strips "--"
	}
	return h
}

func appendMeta(dst []metaPair, key, content string) []metaPair {
	if content == "" {
		return dst
	}
	return append(dst, metaPair{Key: key, Content: content})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// absURL resolves a possibly relative path against base.
func absURL(base, u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		return strings.TrimRight(base, "/") + u
	}
	return u
}

func breadcrumbLD(v *PageView) map[string]any {
	items := make([]map[string]any, 0, len(v.Breadcrumb))
	for i, c := range v.Breadcrumb {
		item := map[string]any{
			"@type":    "ListItem",
			"position": i + 1,
			"name":     c.Name,
		}
		if c.URL != "" {
			item["item"] = absURL(v.BaseURL, c.URL)
		}
		items = append(items, item)
	}
	doc := map[string]any{
		"@context":        "https://schema.org",
		"@type":           "BreadcrumbList",
		"itemListElement": items,
	}
	// A stable @id lets other nodes reference the trail; it is anchored on the
	// last crumb's URL (the current page). Like the Next BreadcrumbJsonLd, a
	// last crumb without a URL anchors on the bare base URL.
	last := v.Breadcrumb[len(v.Breadcrumb)-1]
	if last.URL != "" {
		doc["@id"] = absURL(v.BaseURL, last.URL) + "#breadcrumb"
	} else {
		doc["@id"] = v.BaseURL + "#breadcrumb"
	}
	return doc
}

// siteGraphLD is the Organization + WebSite @graph. Ids, urls and the
// WebSite SearchAction (the site search route) match the Next.js JSON-LD so
// @id references from entity documents (base#organization) resolve.
func siteGraphLD(v *PageView) map[string]any {
	base := strings.TrimRight(v.BaseURL, "/")
	org := orgNode(v, base)
	site := map[string]any{
		"@type":     "WebSite",
		"@id":       base + "#website",
		"url":       base,
		"name":      v.Site.Name,
		"publisher": map[string]any{"@id": base + "#organization"},
		"potentialAction": map[string]any{
			"@type": "SearchAction",
			"target": map[string]any{
				"@type":       "EntryPoint",
				"urlTemplate": base + "/search?q={search_term_string}",
			},
			"query-input": "required name=search_term_string",
		},
	}
	if v.Site.Language != "" {
		site["inLanguage"] = v.Site.Language
	}
	graph := []any{org}
	if v.Business != nil {
		graph = append(graph, localBusinessLD(v, base))
	}
	graph = append(graph, site)
	return map[string]any{"@context": "https://schema.org", "@graph": graph}
}

// ratingLD is the AggregateRating node, or nil without a value. Best and Worst
// print when set. Count prints as ratingCount (a string) by default and as
// reviewCount (a JSON number) when the owner counts reviews; a review count
// that is not a plain number is left out.
func ratingLD(r Rating) map[string]any {
	if r.Value == "" {
		return nil
	}
	out := map[string]any{"@type": "AggregateRating", "ratingValue": r.Value}
	setIf(out, "bestRating", r.Best)
	setIf(out, "worstRating", r.Worst)
	if r.CountType == "reviews" {
		if n, ok := bizprofile.ParseDecimal(r.Count); ok {
			out["reviewCount"] = n
		}
	} else {
		setIf(out, "ratingCount", r.Count)
	}
	return out
}

// setIf sets key only for a non-empty value.
func setIf(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// appendUnique appends the non-empty values not already in dst.
func appendUnique(dst []string, vals ...string) []string {
	for _, v := range vals {
		if v == "" || slices.Contains(dst, v) {
			continue
		}
		dst = append(dst, v)
	}
	return dst
}

// jsonLDScript wraps an already-built JSON-LD document.
func jsonLDScript(raw json.RawMessage) (template.HTML, bool) {
	if !json.Valid(raw) {
		return "", false
	}
	return ldScriptFromBytes(raw)
}

func marshalLDScript(doc any) (template.HTML, bool) {
	b, err := json.Marshal(doc)
	if err != nil {
		return "", false
	}
	return ldScriptFromBytes(b)
}

// ldScriptFromBytes re-encodes raw through encoding/json (which escapes <, >,
// & and U+2028/9) and additionally rewrites "</" so the payload can never
// close the script element.
func ldScriptFromBytes(raw []byte) (template.HTML, bool) {
	b, err := json.Marshal(json.RawMessage(raw))
	if err != nil {
		return "", false
	}
	body := strings.ReplaceAll(string(b), "</", `<\/`)
	return template.HTML(`<script type="application/ld+json">` + body + `</script>`), true // #nosec G203 -- JSON-encoded, "<" escaped
}

// commentText makes s safe for the inside of an HTML comment.
func commentText(s string) string {
	s = strings.TrimSpace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "")
	}
	s = strings.TrimRight(s, "-! ")
	return strings.NewReplacer("\x00", "").Replace(s)
}

// seoInput describes the page for the template resolver.
func seoInput(v *PageView, rawTitle string) seo.Input {
	kind := v.Kind
	if kind == "" {
		kind = KindPage
		if v.EntityID != "" {
			kind = KindSingle
		}
	}
	in := seo.Input{
		Kind:            seo.Kind(kind),
		SchemaSlug:      v.SchemaSlug,
		HandTitle:       v.Page.Title,
		HandDescription: v.Page.Description,
		TitleSet:        v.Page.TitleSet,
		DescriptionSet:  v.Page.DescriptionSet,
		Vars: seo.Vars{
			Title:    rawTitle,
			SiteName: v.Site.Name,
			Tagline:  v.Site.Tagline,
			Archive:  v.Page.Name,
		},
	}
	if e := v.Entity; e != nil {
		in.Vars.Excerpt = e.Excerpt
		if !e.PublishedAt.IsZero() {
			in.Vars.Date = e.PublishedAt.UTC().Format("2006-01-02")
		}
		if e.Author != nil {
			in.Vars.Author = e.Author.Name
		}
	}
	if v.Author != nil {
		in.Vars.Author = v.Author.Name
	}
	if v.Term != nil {
		in.TermKind = v.Term.Kind
		in.Vars.Term = v.Term.Name
	}
	in.Vars.SearchPhrase = v.Query
	if p := v.Pagination; p != nil {
		if p.Page > 1 {
			in.Vars.Page = p.Page
		}
		if p.TotalPages > 1 {
			in.Vars.PageTotal = p.TotalPages
		}
	}
	return in
}

// applySEOTemplates sets the title and description from the site's templates.
// With the default templates and no description template it changes nothing and
// allocates nothing, so the legacy head stays byte-identical.
func applySEOTemplates(h *headView, v *PageView, rawTitle string) {
	in := seoInput(v, rawTitle)
	legacy := seo.IsLegacyTitle(v.SEO, in)
	if legacy && !seo.HasDescriptionTemplate(v.SEO, in) {
		return
	}
	r := seo.Resolve(v.SEO, in)
	if !legacy {
		h.Title = r.Title
	}
	if r.Description != "" {
		h.Description = r.Description
	}
}

// titleWithSite appends " | <site>" to a page title, matching the Next.js
// title template. The site name alone, and titles that already end with it
// after a separator (" | ", " - ", " – ", " — ", " · ", " : "; imported SEO
// titles carry the old site's template), are left as they are. Open Graph and
// Twitter titles use the bare title.
func titleWithSite(title, site string) string {
	site = strings.TrimSpace(site)
	if site == "" || title == site {
		return title
	}
	for _, sep := range []string{" | ", " - ", " – ", " — ", " · ", " : "} {
		if strings.HasSuffix(title, sep+site) {
			return title
		}
	}
	return title + " | " + site
}

// collectionPageLD describes an archive listing, like the Next.js blog page.
func collectionPageLD(v *PageView, h headView) map[string]any {
	n := len(v.Items)
	if v.Pagination != nil {
		n = v.Pagination.Total
	}
	doc := map[string]any{
		"@context":      "https://schema.org",
		"@type":         "CollectionPage",
		"name":          firstNonEmpty(v.Page.Title, v.Page.Name),
		"url":           h.Canonical,
		"numberOfItems": n,
	}
	if h.Description != "" {
		doc["description"] = h.Description
	}
	return doc
}
