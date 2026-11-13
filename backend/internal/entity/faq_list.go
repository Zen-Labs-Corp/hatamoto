package entity

import (
	"strings"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
)

// faqListKey is the attribute (entity level, and prop of a page_doc component
// or key of a legacy section's content) holding a list of
// {question, answer} objects.
const faqListKey = "faq"

// faqAnswerMax bounds one answer in runes once its markup is removed.
const faqAnswerMax = 5000

// FAQItemsFromList reads a list of {question, answer} objects. The answer may
// be rich text; it is reduced to plain text. Items missing either part, and
// anything that is not such a list, are skipped.
func FAQItemsFromList(v any) []FAQItem {
	list, _ := v.([]any)
	var out []FAQItem
	for _, it := range list {
		m, _ := it.(map[string]any)
		q, _ := m["question"].(string)
		a, _ := m["answer"].(string)
		q = strings.TrimSpace(PlainText(q, faqAnswerMax))
		a = strings.TrimSpace(PlainText(a, faqAnswerMax))
		if q != "" && a != "" {
			out = append(out, FAQItem{Question: q, Answer: a})
		}
	}
	return out
}

// FAQItemsFromAttributes collects the structured FAQ of an entry: its own
// faq list first, then the faq list of every page_doc component in document
// order (hidden ones skipped), then, only when the entry has no page_doc, of
// every visible legacy page_sections entry.
func FAQItemsFromAttributes(attrs map[string]any) []FAQItem {
	out := FAQItemsFromList(attrs[faqListKey])
	raw, hasDoc := attrs[keyDoc]
	if hasDoc && raw != nil {
		var doc builder.Doc
		if b, err := jsonx.Marshal(raw); err == nil && jsonx.Unmarshal(b, &doc, jsonx.StoredLoose) == nil {
			for _, n := range builder.Components(&doc) {
				if hidden, _ := n.P["hidden"].(bool); hidden {
					continue
				}
				out = append(out, FAQItemsFromList(n.P[faqListKey])...)
			}
		}
	}
	if hasDoc && raw != nil {
		// A converted page keeps a frozen page_sections mirror; the renderer
		// ignores it once page_doc exists, so the FAQ must too.
		return out
	}
	sections, _ := attrs[keySections].([]any)
	for _, s := range sections {
		sec, _ := s.(map[string]any)
		if visible, ok := sec["isVisible"].(bool); ok && !visible {
			continue
		}
		if visible, ok := sec["is_visible"].(bool); ok && !visible {
			continue
		}
		content, _ := sec["content"].(map[string]any)
		out = append(out, FAQItemsFromList(content[faqListKey])...)
	}
	return out
}
