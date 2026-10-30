package render

import (
	"context"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Host-side head extension types. They mirror the plugin SDK's render.head
// filter payload; the plugin host adapts SDK values to these (see TODO-19), so
// this package stays free of SDK imports.

// HeadTag is one tag a render.head filter asks the host to emit. The host
// builds and escapes the markup; a filter never supplies raw HTML.
type HeadTag struct {
	// Kind is meta, link, script-src or jsonld.
	Kind string
	// Attrs are the tag attributes; names outside the per-kind allowlist are dropped.
	Attrs map[string]string
	// JSONLD is the document for Kind "jsonld".
	JSONLD []byte
}

// HeadContext is the render.head filter input/output.
type HeadContext struct {
	Path       string
	Language   string
	SchemaSlug string
	EntityID   string
	Tags       []HeadTag
	// ReplaceSiteGraph asks the host to omit its generic Organization/WebSite
	// graph because the filter supplies its own as jsonld tags.
	ReplaceSiteGraph bool
}

// HeadFilter transforms the head context; it is fail-open (see applyHeadFilter).
type HeadFilter func(ctx context.Context, c HeadContext) HeadContext

// headFilterTimeout bounds the filter chain, like the sitemap.urls filter.
var headFilterTimeout = 2 * time.Second

// extTag is a sanitised plugin tag, laid out for the head partial so that
// html/template escapes every value in its proper attribute context.
type extTag struct {
	Kind                                   string
	Name, Property, Content                string
	Rel, Href, Type, Hreflang, Crossorigin string
	Src                                    string
}

// allowedTagAttrs is the per-kind attribute allowlist.
var allowedTagAttrs = map[string]map[string]bool{
	"meta":       {"name": true, "property": true, "content": true},
	"link":       {"rel": true, "href": true, "type": true, "hreflang": true, "crossorigin": true},
	"script-src": {"src": true, "crossorigin": true},
}

// applyHeadFilter runs f with a deadline. A nil filter, a panic or a timeout
// returns in unchanged (fail-open): a misbehaving plugin never breaks a page.
func applyHeadFilter(ctx context.Context, f HeadFilter, in HeadContext) HeadContext {
	if f == nil {
		return in
	}
	ctx, cancel := context.WithTimeout(ctx, headFilterTimeout)
	defer cancel()
	done := make(chan HeadContext, 1)
	go func() {
		defer func() {
			if recover() != nil {
				NoteDegradedCtx(ctx)
				done <- in
			}
		}()
		done <- f(ctx, in)
	}()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		NoteDegradedCtx(ctx)
		return in
	}
}

// buildHeadView builds the full head: SEO output plus sanitised filter tags.
func buildHeadView(ctx context.Context, v *PageView, f HeadFilter) headView {
	// The filter runs first: its ReplaceSiteGraph decides what seoHead emits.
	// On panic or timeout applyHeadFilter returns the input, so the generic
	// graph is kept.
	out := applyHeadFilter(ctx, f, HeadContext{
		Path: v.Path, Language: v.Site.Language, SchemaSlug: v.SchemaSlug, EntityID: v.EntityID,
	})
	var ldScripts []template.HTML
	var extTags []extTag
	for _, t := range out.Tags {
		if t.Kind == "jsonld" {
			if s, ok := jsonLDScript(t.JSONLD); ok {
				ldScripts = append(ldScripts, s)
			}
			continue
		}
		if et, ok := sanitizeTag(t); ok {
			extTags = append(extTags, et)
		}
	}
	// Replace only when a plugin graph was actually accepted; otherwise the
	// page would be left with no site graph at all.
	h := seoHead(v, out.ReplaceSiteGraph && len(ldScripts) > 0)
	h.Meta = appendMeta(h.Meta, "geo.region", v.GeoRegion)
	h.Scripts = append(h.Scripts, ldScripts...)
	h.Ext = append(h.Ext, dropDuplicateAlternates(extTags, h.Alt)...)
	return h
}

// dropDuplicateAlternates removes plugin rel=alternate hreflang links the host
// already emits, so a plugin and the host never write the same tag twice.
func dropDuplicateAlternates(tags []extTag, alt []altLink) []extTag {
	if len(alt) == 0 {
		return tags
	}
	out := tags[:0:0]
	for _, t := range tags {
		if t.Kind == "link" && strings.EqualFold(t.Rel, "alternate") && t.Hreflang != "" && hasAlt(alt, t.Hreflang, t.Href) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func hasAlt(alt []altLink, lang, href string) bool {
	for _, a := range alt {
		if strings.EqualFold(a.Lang, lang) && a.Href == href {
			return true
		}
	}
	return false
}

// sanitizeTag enforces the kind and attribute allowlists and URL schemes.
func sanitizeTag(t HeadTag) (extTag, bool) {
	allowed, ok := allowedTagAttrs[t.Kind]
	if !ok {
		return extTag{}, false
	}
	attrs := map[string]string{}
	keys := make([]string, 0, len(t.Attrs))
	for k := range t.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := strings.ToLower(strings.TrimSpace(k))
		if !allowed[name] {
			continue
		}
		val := t.Attrs[k]
		switch name {
		case "href":
			if !httpOrRelative(val, true) {
				continue
			}
		case "src":
			if !httpsOrRelative(val) {
				continue
			}
		case "crossorigin":
			if val != "" && val != "anonymous" && val != "use-credentials" {
				continue
			}
		}
		attrs[name] = val
	}
	et := extTag{
		Kind: t.Kind, Name: attrs["name"], Property: attrs["property"], Content: attrs["content"],
		Rel: attrs["rel"], Href: attrs["href"], Type: attrs["type"], Hreflang: attrs["hreflang"],
		Crossorigin: attrs["crossorigin"], Src: attrs["src"],
	}
	switch t.Kind {
	case "meta":
		if et.Name == "" && et.Property == "" {
			return extTag{}, false
		}
	case "link":
		if et.Rel == "" || et.Href == "" {
			return extTag{}, false
		}
	case "script-src":
		if et.Src == "" {
			return extTag{}, false
		}
	}
	return et, true
}

func httpOrRelative(raw string, allowHTTP bool) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || raw == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		return allowHTTP
	case "":
		return u.Host == "" && !strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, `/\`)
	}
	return false
}

// httpsOrRelative allows https and relative URLs only (scripts never load over http).
func httpsOrRelative(raw string) bool { return httpOrRelative(raw, false) }
