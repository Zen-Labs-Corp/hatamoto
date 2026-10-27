package v2

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/net/html"
)

// wrapTargetFields is the allowlist of JSON field names whose string value the
// JSONB wrap walker is allowed to mutate. Matches the v1
// `injectInternalLinks` callsites so the backfill produces the same visible
// wrap topology as v1 did.
//
// Excluded by omission: headlines/headings (headline, subheadline, heading,
// subheading, eyebrow, title), CTAs (cta_text, button_text), metadata
// (meta_title, meta_description, name, slug), URLs (url, href, src, link).
var wrapTargetFields = map[string]bool{
	// Top-level entity attributes that carry HTML bodies.
	"content":          true, // blog/glossary/team-member legacy single-string body
	"full_description": true, // service/industry-vertical long description

	// page_sections[*].content.* fields that the public renderer treats as HTML.
	"body":                true,
	"description":         true,
	"paragraph":           true,
	"answer":              true, // FAQ items
	"problemDescription":  true, // problem/solution sections
	"solutionDescription": true,
}

// textLeaf is one mutable string location inside an entity's attributes JSON.
// `path` is the sequence of keys (string) or indices (int) needed to reach the
// leaf from the root map. `value` is the current string; mutations write
// through `set` so the caller never re-walks the JSON.
type textLeaf struct {
	path  []any
	value string
	set   func(string)
}

// WrapAllJSONB walks an entity's full attributes JSON and applies each spec
// to the first wrappable text leaf, mirroring the v1 render-time wrap
// topology. Returns aggregate counts and the mutated attributes JSON (same
// shape as input). If no wrap landed, returns the input bytes unchanged.
//
// Algorithm:
//  1. Unmarshal the attributes into a tree.
//  2. Collect every string leaf whose parent key is in `wrapTargetFields`,
//     in document order.
//  3. For each spec, check if the target is already linked in ANY leaf — if
//     so, count as already_linked and move on (matches single-string
//     WrapAll's hasEntityLink guard, scaled across leaves).
//  4. Otherwise walk leaves in order and call `wrapFirstOccurrence` on each
//     until one succeeds; stop on first hit.
//  5. Re-serialize and return.
//
// Cross-leaf safety: the wrap walker uses the existing skip-tag list (a,
// h1-h6, code, pre) per leaf. Field-name-based heading exclusion (e.g.
// "headline") is handled by the allowlist above — those fields are never
// even considered as wrap targets.
func WrapAllJSONB(attrs []byte, specs []LinkSpec) (WrapResult, []byte, error) {
	res := WrapResult{Skips: map[string]int{}}
	if len(attrs) == 0 || len(specs) == 0 {
		for range specs {
			res.Skips[SkipNoMatch]++
		}
		return res, attrs, nil
	}

	var root map[string]any
	if err := json.Unmarshal(attrs, &root); err != nil {
		return res, attrs, fmt.Errorf("unmarshal attributes: %w", err)
	}

	leaves := attributeLeaves(root)
	if len(leaves) == 0 {
		for range specs {
			res.Skips[SkipNoMatch]++
		}
		return res, attrs, nil
	}

	changed := false
	for _, spec := range specs {
		anchor := normaliseHTMLEntities(spec.AnchorText)
		if strings.TrimSpace(anchor) == "" {
			res.Skips[SkipEmptyAnchor]++
			continue
		}
		if entityLinkedAnywhere(leaves, spec.TargetEntityID) {
			res.Skips[SkipAlreadyLinked]++
			continue
		}

		var lastSkipReason string
		var inserted bool
		for i := range leaves {
			leaf := &leaves[i]
			wrapped, outcome := wrapInLeafValue(leaf.value, anchor, spec.CanonicalURL, spec.TargetEntityID, spec.TargetSlug, spec.TargetSchemaSlug)
			if outcome.Inserted {
				leaf.value = wrapped
				leaf.set(wrapped)
				res.Inserted++
				changed = true
				inserted = true
				break
			}
			// Track the most-informative skip reason. in_heading / in_code
			// outrank no_match because they tell the operator the anchor
			// existed but lived in a forbidden context.
			if outcome.SkipReason != SkipNoMatch && lastSkipReason == "" {
				lastSkipReason = outcome.SkipReason
			}
		}
		if !inserted {
			if lastSkipReason == "" {
				lastSkipReason = SkipNoMatch
			}
			res.Skips[lastSkipReason]++
		}
	}

	if !changed {
		return res, attrs, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return res, attrs, fmt.Errorf("re-marshal attributes: %w", err)
	}
	res.NewHTML = string(out) // semantic overload: callers reading WrapResult.NewHTML get the full attributes JSON
	return res, out, nil
}

// attributeLeaves is the text leaves of an entity's content. When the entity
// has a page_doc it is the document (the renderer prefers it), so only its
// leaves count; page_sections is then just the frozen mirror and is left
// alone. Without a doc, page_sections is walked.
func attributeLeaves(root map[string]any) []textLeaf {
	_, hasDoc := root["page_doc"].(map[string]any)
	var out []textLeaf
	for k, child := range root {
		if hasDoc && k == "page_sections" {
			continue
		}
		if s, ok := child.(string); ok && wrapTargetFields[k] {
			m, key := root, k
			out = append(out, textLeaf{path: []any{k}, value: s, set: func(v string) { m[key] = v }})
			continue
		}
		out = append(out, collectTextLeaves(child, []any{k})...)
	}
	return out
}

// collectTextLeaves walks the tree depth-first and returns every string
// reachable through a key in wrapTargetFields. Arrays of objects are
// traversed (e.g. faq.items, process.steps), so `items[N].answer` is
// reachable. The path is preserved for diagnostics but mutation goes
// through the returned `set` closure (which captures the parent map/slice
// directly so writes happen in-place).
func collectTextLeaves(node any, parentKey []any) []textLeaf {
	var out []textLeaf
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			childKey := append(append([]any{}, parentKey...), k)
			if s, ok := child.(string); ok && wrapTargetFields[k] {
				// Capture the map by closure so set() writes through.
				m := v
				key := k
				out = append(out, textLeaf{
					path:  childKey,
					value: s,
					set:   func(newVal string) { m[key] = newVal },
				})
				continue
			}
			out = append(out, collectTextLeaves(child, childKey)...)
		}
	case []any:
		for idx, child := range v {
			out = append(out, collectTextLeaves(child, append(append([]any{}, parentKey...), idx))...)
		}
	}
	return out
}

// entityLinkedAnywhere reports whether any leaf's HTML already contains
// `<a data-entity-id="targetID">`. Implements the cross-leaf
// already-linked guard so each (source, target) pair only wraps once.
func entityLinkedAnywhere(leaves []textLeaf, targetID uuid.UUID) bool {
	for _, leaf := range leaves {
		if leaf.value == "" {
			continue
		}
		if leafContainsEntityLink(leaf.value, targetID) {
			return true
		}
	}
	return false
}

// leafContainsEntityLink parses a single string and asks whether any
// `<a data-entity-id="targetID">` exists. Reuses hasEntityLink from wrap.go
// for the recursion, just with a per-leaf parse.
func leafContainsEntityLink(s string, targetID uuid.UUID) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	root, err := html.Parse(strings.NewReader("<html><body>" + s + "</body></html>"))
	if err != nil {
		return false
	}
	body := findBody(root)
	if body == nil {
		return false
	}
	return hasEntityLink(body, targetID)
}

// wrapInLeafValue runs the single-spec wrap against one string leaf and
// returns (newValue, outcome). The string is parsed under the same
// `<html><body>` wrapper as WrapAll so the skip-tag walker behaves
// identically.
func wrapInLeafValue(leafValue, anchor, href string, targetID uuid.UUID, targetSlug, targetSchemaSlug string) (string, WrapOutcome) {
	leafValue = strings.TrimSpace(leafValue)
	if leafValue == "" {
		return leafValue, WrapOutcome{SkipReason: SkipNoMatch}
	}
	root, err := html.Parse(strings.NewReader("<html><body>" + leafValue + "</body></html>"))
	if err != nil {
		return leafValue, WrapOutcome{SkipReason: SkipNoMatch}
	}
	body := findBody(root)
	if body == nil {
		return leafValue, WrapOutcome{SkipReason: SkipNoMatch}
	}
	outcome := wrapFirstOccurrence(body, anchor, href, targetID, targetSlug, targetSchemaSlug)
	if !outcome.Inserted {
		return leafValue, outcome
	}
	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&sb, c); err != nil {
			return leafValue, WrapOutcome{SkipReason: SkipNoMatch}
		}
	}
	return sb.String(), outcome
}
