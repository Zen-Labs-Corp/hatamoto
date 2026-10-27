package render

import (
	"encoding/json"
	"html/template"
	"net/url"
	"strings"
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
	OG         []metaPair
	Feeds      []FeedLink
	Twitter    []metaPair
	Ext        []extTag
	// Scripts are complete <script type="application/ld+json"> elements.
	Scripts []template.HTML
	// Comment is an optional AI-crawler HTML comment.
	Comment template.HTML
}

// seoHead builds the SEO-derived part of the head from the page view. With
// replaceSiteGraph the generic Organization/WebSite graph is omitted because
// a head filter supplies its own.
func seoHead(v *PageView, replaceSiteGraph bool) headView {
	h := headView{}

	rawTitle := firstNonEmpty(v.Page.Title, v.Page.Name, v.Site.Name)
	h.Title = titleWithSite(rawTitle, v.Site.Name) // "<title> | <site>", like the Next.js title template
	h.Description = v.Page.Description
	h.Canonical = firstNonEmpty(v.Page.CanonicalURL, strings.TrimRight(v.BaseURL, "/")+v.Path)
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

	ogType := firstNonEmpty(v.Page.OGType, "website")
	h.OG = appendMeta(h.OG, "og:title", rawTitle)
	h.OG = appendMeta(h.OG, "og:description", h.Description)
	h.OG = appendMeta(h.OG, "og:image", v.Page.OGImage)
	h.OG = appendMeta(h.OG, "og:type", ogType)
	h.OG = appendMeta(h.OG, "og:url", h.Canonical)

	card := "summary"
	if v.Page.OGImage != "" {
		card = "summary_large_image"
	}
	h.Twitter = appendMeta(h.Twitter, "twitter:card", card)
	h.Twitter = appendMeta(h.Twitter, "twitter:title", rawTitle)
	h.Twitter = appendMeta(h.Twitter, "twitter:description", h.Description)
	h.Twitter = appendMeta(h.Twitter, "twitter:image", v.Page.OGImage)

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
		if s, ok := marshalLDScript(siteGraphLD(v)); ok {
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
	org := map[string]any{
		"@type": "Organization",
		"@id":   base + "#organization",
		"name":  v.Site.Name,
		"url":   base,
	}
	if v.Site.LogoURL != "" {
		org["logo"] = absURL(base, v.Site.LogoURL)
	}
	if v.Site.Tagline != "" {
		org["description"] = v.Site.Tagline
	}
	if v.Site.Phone != "" {
		org["telephone"] = v.Site.Phone
		contact := map[string]any{"@type": "ContactPoint", "telephone": v.Site.Phone, "contactType": "customer service"}
		if v.Site.Language != "" {
			contact["availableLanguage"] = v.Site.Language
		}
		if v.Site.Hours != "" {
			contact["hoursAvailable"] = v.Site.Hours
		}
		org["contactPoint"] = contact
	}
	if a := v.Site.Address; a != (Address{}) {
		addr := map[string]any{"@type": "PostalAddress"}
		for k, val := range map[string]string{
			"streetAddress": a.Street, "addressLocality": a.City, "addressRegion": a.State,
			"postalCode": a.PostalCode, "addressCountry": a.Country,
		} {
			if val != "" {
				addr[k] = val
			}
		}
		org["address"] = addr
	}
	var same []string
	for _, l := range v.Site.SocialLinks {
		if u, err := url.Parse(l.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			same = append(same, l.URL)
		}
	}
	if len(same) > 0 {
		org["sameAs"] = same
	}
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
	return map[string]any{"@context": "https://schema.org", "@graph": []any{org, site}}
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
