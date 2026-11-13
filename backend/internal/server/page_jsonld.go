package server

import (
	"context"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// buildPageJSONLD is the JSON-LD document of an entry page, shared by the
// HTML and the Accept: application/ld+json responses: the generated document
// (plus FAQPage), the canonical rewrite, the author, the WebPage switch of
// the schema or entry, then the jsonld.enrich hook. noBreadcrumbLD says the BreadcrumbList script must
// be left out; the visible trail is not affected. It is set even when
// generation fails.
func buildPageJSONLD(ctx context.Context, ent *entity.Entity, baseURL, pagePath string, faqItems []entity.FAQItem, by *bylines) (doc map[string]any, noBreadcrumbLD bool, err error) {
	var st entity.SchemaURLSettings
	if ent.Schema != nil {
		st, _ = ent.Schema.URLSettings()
	}
	var attrs map[string]any
	_ = jsonx.Unmarshal(ent.Attributes, &attrs, jsonx.StoredLoose)
	sw := entity.ResolveNodeSwitches(st, attrs)
	noBreadcrumbLD = sw.Breadcrumb != nil && !*sw.Breadcrumb

	doc, err = entity.GenerateJSONLDWithFAQUnenriched(ent, baseURL, faqItems)
	if err != nil {
		return nil, noBreadcrumbLD, err
	}
	if ent.Schema != nil {
		entity.ApplyCanonicalToJSONLD(doc, ent.Schema.Slug, baseURL+pagePath)
	}
	by.applyAuthorFrom(ctx, ent, st, baseURL, doc)
	by.applyJSONLD(ctx, ent.ID, baseURL, doc)
	if sw.WebPage != nil {
		doc = switchWebPage(doc, *sw.WebPage, ent.Name, baseURL, pagePath)
	}
	// Last, so a jsonld.enrich plugin sees the final document and keeps the
	// last word. On the success path only, and not for a dropped document.
	if doc != nil {
		entity.EnrichJSONLD(ent, baseURL, doc)
	}
	return doc, noBreadcrumbLD, nil
}

// switchWebPage drops the WebPage node (on = false) or makes sure the
// document carries one (on = true, wrapping a typed document in a @graph).
func switchWebPage(doc map[string]any, on bool, name, baseURL, pagePath string) map[string]any {
	if doc == nil {
		return doc
	}
	graph, isGraph := doc["@graph"].([]map[string]any)
	isWebPage := func(n map[string]any) bool { return n["@type"] == "WebPage" }
	if !on {
		if !isGraph {
			if isWebPage(doc) {
				return nil
			}
			return doc
		}
		kept := graph[:0:0]
		for _, n := range graph {
			if !isWebPage(n) {
				kept = append(kept, n)
			}
		}
		doc["@graph"] = kept
		return doc
	}
	if isGraph {
		for _, n := range graph {
			if isWebPage(n) {
				return doc
			}
		}
	} else if isWebPage(doc) {
		return doc
	}
	url := baseURL + pagePath
	page := map[string]any{
		"@type":    "WebPage",
		"@id":      url + "#webpage",
		"url":      url,
		"name":     name,
		"isPartOf": map[string]any{"@id": baseURL + "#website"},
	}
	if isGraph {
		doc["@graph"] = append(graph, page)
		return doc
	}
	delete(doc, "@context")
	return map[string]any{"@context": "https://schema.org", "@graph": []map[string]any{doc, page}}
}
