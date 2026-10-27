package v2

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/html"
)

// SkipReason enumerates every reason an anchor cannot be wrapped. The strings
// are stable — the /linking/suggest API response and the v2 backfill report
// format both read them by name. (Backfill tool itself was retired post-
// cutover; see cmd/normalize-link-markers/ for the residual one-shot.)
const (
	SkipNoMatch        = "no_match"
	SkipAlreadyLinked  = "already_linked"
	SkipInHeading      = "in_heading"
	SkipInCode         = "in_code"
	SkipExcludedSchema = "excluded_schema"
	SkipResolverError  = "resolver_error"
	SkipEmptyAnchor    = "empty_anchor"
)

// WrapOutcome is the result of one wrap attempt: exactly one of Inserted or
// SkipReason is set.
type WrapOutcome struct {
	Inserted   bool
	SkipReason string
}

// WrapResult is the aggregate after attempting every spec for one source.
// NewHTML is empty when no wraps were applied (caller can skip the write).
type WrapResult struct {
	NewHTML  string
	Inserted int
	Skips    map[string]int
}

// LinkSpec is the per-link input the wrap algorithm consumes. The migration
// driver and the /linking/insert endpoint both assemble these — the latter
// with a single spec at a time.
//
// CanonicalURL must already be resolved by Resolve(). AnchorText is the
// caller-supplied phrase; the wrapper normalises HTML entities (M&amp;S → M&S)
// before searching so equivalent encodings still match.
//
// TargetSlug and TargetSchemaSlug, when non-empty, are persisted on the
// generated <a> tag as `data-target-slug` and `data-target-schema`. These
// markers decouple the link's target identity from whatever the
// resolver baked into CanonicalURL, so the v2 slug-rename rewriter can find
// what it needs without parsing hrefs. The historical v2 backfill (retired post-cutover) left them
// empty; the one-shot normaliser (cmd/normalize-link-markers) populates
// them for those legacy rows. New inserts via Insert() should populate
// both.
type LinkSpec struct {
	TargetEntityID   uuid.UUID
	TargetSlug       string
	TargetSchemaSlug string
	AnchorText       string
	CanonicalURL     string
}

// skipTagsForWrap is the set of element names whose subtrees must not be
// touched by the wrap walker.
var skipTagsForWrap = map[string]string{
	"a":      SkipAlreadyLinked,
	"h1":     SkipInHeading,
	"h2":     SkipInHeading,
	"h3":     SkipInHeading,
	"h4":     SkipInHeading,
	"h5":     SkipInHeading,
	"h6":     SkipInHeading,
	"code":   SkipInCode,
	"pre":    SkipInCode,
	"script": SkipInCode,
	"style":  SkipInCode,
}

// WrapAll runs the per-source DOM walk for every spec in priority order and
// returns the rewritten HTML plus per-reason skip counts. Each spec is
// attempted against the live document — earlier wraps mutate the tree that
// later wraps walk, which is how "only the first occurrence per (source,
// target)" emerges. If no wraps are applied, NewHTML is empty.
func WrapAll(contentHTML string, specs []LinkSpec) (WrapResult, error) {
	res := WrapResult{Skips: map[string]int{}}
	contentHTML = strings.TrimSpace(contentHTML)
	if contentHTML == "" {
		for range specs {
			res.Skips[SkipNoMatch]++
		}
		return res, nil
	}

	root, err := html.Parse(strings.NewReader("<html><body>" + contentHTML + "</body></html>"))
	if err != nil {
		return res, fmt.Errorf("parse content html: %w", err)
	}
	body := findBody(root)
	if body == nil {
		for range specs {
			res.Skips[SkipNoMatch]++
		}
		return res, nil
	}

	changed := false
	for _, spec := range specs {
		anchor := normaliseHTMLEntities(spec.AnchorText)
		if strings.TrimSpace(anchor) == "" {
			res.Skips[SkipEmptyAnchor]++
			continue
		}
		if hasEntityLink(body, spec.TargetEntityID) {
			res.Skips[SkipAlreadyLinked]++
			continue
		}
		outcome := wrapFirstOccurrence(body, anchor, spec.CanonicalURL, spec.TargetEntityID, spec.TargetSlug, spec.TargetSchemaSlug)
		if outcome.Inserted {
			res.Inserted++
			changed = true
		} else {
			res.Skips[outcome.SkipReason]++
		}
	}

	if !changed {
		return res, nil
	}

	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&sb, c); err != nil {
			return res, fmt.Errorf("render content: %w", err)
		}
	}
	res.NewHTML = sb.String()
	return res, nil
}

// hasEntityLink reports whether any <a data-entity-id="targetID"> already
// exists anywhere in the subtree. Used as the "already linked" guard so the
// same source never points to the same target twice via separate wrap calls.
func hasEntityLink(n *html.Node, targetID uuid.UUID) bool {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, "data-entity-id") {
				if id, err := uuid.Parse(a.Val); err == nil && id == targetID {
					return true
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasEntityLink(c, targetID) {
			return true
		}
	}
	return false
}

func wrapFirstOccurrence(root *html.Node, anchor, href string, targetID uuid.UUID, targetSlug, targetSchemaSlug string) WrapOutcome {
	var seenSkipReason string
	var result *WrapOutcome

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if result != nil {
			return
		}
		if n.Type == html.ElementNode {
			if reason, blocked := skipTagsForWrap[strings.ToLower(n.Data)]; blocked {
				if textSubtreeContainsAnchor(n, anchor) {
					if seenSkipReason == "" {
						seenSkipReason = reason
					}
				}
				return
			}
		}
		if n.Type == html.TextNode {
			text := normaliseHTMLEntities(n.Data)
			if start, end, ok := findWholeWord(text, anchor); ok {
				splitAndWrap(n, start, end, href, targetID, targetSlug, targetSchemaSlug)
				result = &WrapOutcome{Inserted: true}
				return
			}
		}
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			walk(c)
			if result != nil {
				return
			}
			c = next
		}
	}
	walk(root)

	if result != nil {
		return *result
	}
	if seenSkipReason != "" {
		return WrapOutcome{SkipReason: seenSkipReason}
	}
	return WrapOutcome{SkipReason: SkipNoMatch}
}

func textSubtreeContainsAnchor(n *html.Node, anchor string) bool {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	text := normaliseHTMLEntities(sb.String())
	_, _, ok := findWholeWord(text, anchor)
	return ok
}

func splitAndWrap(textNode *html.Node, start, end int, href string, targetID uuid.UUID, targetSlug, targetSchemaSlug string) {
	original := textNode.Data
	prefix := original[:start]
	matched := original[start:end]
	suffix := original[end:]

	parent := textNode.Parent
	if parent == nil {
		return
	}

	attrs := []html.Attribute{
		{Key: "href", Val: href},
		{Key: "data-entity-id", Val: targetID.String()},
		{Key: "class", Val: "entity-link"},
	}
	if targetSlug != "" {
		attrs = append(attrs, html.Attribute{Key: "data-target-slug", Val: targetSlug})
	}
	if targetSchemaSlug != "" {
		attrs = append(attrs, html.Attribute{Key: "data-target-schema", Val: targetSchemaSlug})
	}
	anchor := &html.Node{
		Type: html.ElementNode,
		Data: "a",
		Attr: attrs,
	}
	anchor.AppendChild(&html.Node{Type: html.TextNode, Data: matched})

	next := textNode.NextSibling
	parent.RemoveChild(textNode)

	if prefix != "" {
		parent.InsertBefore(&html.Node{Type: html.TextNode, Data: prefix}, next)
	}
	parent.InsertBefore(anchor, next)
	if suffix != "" {
		parent.InsertBefore(&html.Node{Type: html.TextNode, Data: suffix}, next)
	}
}

// findWholeWord performs a case-insensitive whole-word search for needle
// inside haystack, returning the byte-indexed [start, end) of the first
// match in haystack (preserving original casing for slicing).
func findWholeWord(haystack, needle string) (int, int, bool) {
	if needle == "" {
		return 0, 0, false
	}
	hLen := len(haystack)
	nLen := len(needle)
	for i := 0; i+nLen <= hLen; i++ {
		if !strings.EqualFold(haystack[i:i+nLen], needle) {
			continue
		}
		if !isWordBoundary(haystack, i) {
			continue
		}
		if !isWordBoundary(haystack, i+nLen) {
			continue
		}
		return i, i + nLen, true
	}
	return 0, 0, false
}

func isWordBoundary(s string, pos int) bool {
	if pos <= 0 || pos >= len(s) {
		return true
	}
	prev, _ := utf8.DecodeLastRuneInString(s[:pos])
	next, _ := utf8.DecodeRuneInString(s[pos:])
	return !isWordRune(prev) || !isWordRune(next)
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func normaliseHTMLEntities(s string) string {
	return html.UnescapeString(s)
}

// findBody is defined in rewriter.go and reused here.
