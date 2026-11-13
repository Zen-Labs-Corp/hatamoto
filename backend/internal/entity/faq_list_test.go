package entity

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
)

func faqAttrs(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := jsonx.Unmarshal([]byte(raw), &m, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFAQItemsFromAttributes(t *testing.T) {
	one := FAQItem{"What is it?", "A thing."}
	two := FAQItem{"Où ?", "Ici."}
	three := FAQItem{"Wann?", "Jetzt."}
	cases := []struct {
		name, attrs string
		want        []FAQItem
	}{
		{"entity list", `{"faq":[{"question":"What is it?","answer":"A thing."}]}`, []FAQItem{one}},
		{"page_doc component", `{"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}}]}}`, []FAQItem{two}},
		{"nested component", `{"page_doc":{"v":2,"nodes":[{"id":"c","t":"container","c":[{"id":"a","t":"component","p":{"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}}]}]}}`, []FAQItem{two}},
		{"legacy sections", `{"page_sections":[{"type":"faq","content":{"faq":[{"question":"Wann?","answer":"Jetzt."}]}}]}`, []FAQItem{three}},
		{"page_doc wins over frozen sections", `{"faq":[{"question":"What is it?","answer":"A thing."}],"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}}]},"page_sections":[{"content":{"faq":[{"question":"Wann?","answer":"Jetzt."}]}}]}`, []FAQItem{one, two}},
		{"question deleted from page_doc is not emitted", `{"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}}]},"page_sections":[{"content":{"faq":[{"question":"Où ?","answer":"Ici."},{"question":"Wann?","answer":"Jetzt."}]}}]}`, []FAQItem{two}},
		{"null page_doc falls back to sections", `{"page_doc":null,"page_sections":[{"content":{"faq":[{"question":"Wann?","answer":"Jetzt."}]}}]}`, []FAQItem{three}},
		{"hidden component skipped", `{"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"hidden":true,"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}},{"id":"b","t":"container","c":[{"id":"c","t":"component","p":{"hidden":false,"content":{"faq":[{"question":"Wann?","answer":"Jetzt."}]}}}]}]}}`, []FAQItem{three}},
		{"invisible legacy section skipped", `{"page_sections":[{"isVisible":false,"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}},{"is_visible":false,"content":{"faq":[{"question":"What is it?","answer":"A thing."}]}},{"isVisible":true,"content":{"faq":[{"question":"Wann?","answer":"Jetzt."}]}}]}`, []FAQItem{three}},
		{"html answer stripped", `{"faq":[{"question":"What is it?","answer":"<p>A <b>thing</b>.</p>"}]}`, []FAQItem{{"What is it?", "A thing."}}},
		{"incomplete skipped", `{"faq":[{"question":"Q only"},{"answer":"A only"},{"question":"","answer":"x"},{"question":"What is it?","answer":"A thing."}]}`, []FAQItem{one}},
		{"malformed ignored", `{"faq":"nope","page_doc":"x","page_sections":[1,"a",{"content":3},{"content":{"faq":{"a":1}}}]}`, nil},
		{"list of non-objects", `{"faq":[1,"x",null,[1]]}`, nil},
		{"none", `{}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FAQItemsFromAttributes(faqAttrs(t, tc.attrs)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := FAQItemsFromAttributes(nil); got != nil {
		t.Errorf("nil attrs: %v", got)
	}
}

// A converted page: the list sits at p.content.faq and a hidden section is
// marked on its container. The doc is validated like a stored one.
func TestFAQItemsFromConvertedPageDoc(t *testing.T) {
	hide := false
	doc := builder.FromSections([]builder.Section{
		{ID: "s1", Type: "faq", Content: map[string]any{"faq": []any{map[string]any{"question": "Wann?", "answer": "Jetzt."}}}},
		{ID: "s2", Type: "faq", IsVisible: &hide, Content: map[string]any{"faq": []any{map[string]any{"question": "Où ?", "answer": "Ici."}}}},
	})
	raw, err := jsonx.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Parse(raw); err != nil {
		t.Fatalf("fixture is not a valid page_doc: %v", err)
	}
	var stored any
	if err := jsonx.Unmarshal(raw, &stored, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	attrs := map[string]any{
		"page_doc":      stored,
		"page_sections": []any{map[string]any{"content": map[string]any{"faq": []any{map[string]any{"question": "Frozen?", "answer": "Yes."}}}}},
	}
	want := []FAQItem{{"Wann?", "Jetzt."}}
	if got := FAQItemsFromAttributes(attrs); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
