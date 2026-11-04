package entity

import (
	"encoding/json"
	"testing"
)

func TestStructuralAttrsChanged(t *testing.T) {
	t.Parallel()
	schema := &EntitySchema{Schema: json.RawMessage(`{"properties":{"body":{"type":"string","x-hm-alias":["post_content"]}}}`)}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same", `{"content":"x"}`, `{"content":"x"}`, false},
		{"empty strings and false are absent", `{"content":"x","meta_title":"","no_index":false,"og_image":" "}`, `{"content":"x"}`, false},
		{"empty seo object", `{"content":"x","seo":{}}`, `{"content":"x","seo":{"focus_keyword":""}}`, false},
		{"trimmed", `{"content":" x "}`, `{"content":"x"}`, false},
		{"non-structural keys", `{"content":"x","featured":true,"excerpt":"e","rating":3}`, `{"content":"x"}`, false},
		{"alias resolved", `{"body":"x"}`, `{"post_content":"x"}`, false},
		{"body", `{"content":"y"}`, `{"content":"x"}`, true},
		{"meta title", `{"meta_title":"t"}`, `{}`, true},
		{"og key", `{"og_title":"t"}`, `{}`, true},
		{"no_index", `{"no_index":true}`, `{}`, true},
		{"seo focus", `{"seo":{"focus_keyword":"k"}}`, `{}`, true},
		{"seo llms off", `{"seo":{"include_in_llms_txt":false}}`, `{}`, true},
		{"alias body differs", `{"body":"y"}`, `{"post_content":"x"}`, true},
	}
	for _, c := range cases {
		if got := structuralAttrsChanged(schema, json.RawMessage(c.a), json.RawMessage(c.b)); got != c.want {
			t.Errorf("%s: changed = %v, want %v", c.name, got, c.want)
		}
	}
}

// A caller that leaves page_doc out, or sends derived page_sections instead of
// the stored copy, is not editing the doc: the service carries it over.
func TestStructuralChangeCarriesStoredDoc(t *testing.T) {
	t.Parallel()
	stored := `{"page_sections":` + secsA + `,"page_doc":` + fromSecs(t, secsA) + `,"content":"x"}`
	ex := &Entity{Schema: &EntitySchema{Slug: "page", Schema: json.RawMessage(`{}`)}, Attributes: json.RawMessage(stored)}
	docOnly := &Entity{Schema: ex.Schema, Attributes: json.RawMessage(`{"page_doc":` + twoCol + `,"content":"x"}`)}
	cases := []struct {
		name   string
		ex     *Entity
		caller string
		want   bool
	}{
		{"doc omitted", ex, `{"content":"x","excerpt":"e","featured":true}`, false},
		{"doc omitted on a builder-only page", docOnly, `{"content":"x","excerpt":"e"}`, false},
		{"derived sections only", ex, `{"content":"x","page_sections":` + secsA + `}`, false},
		{"doc omitted, body edited", ex, `{"content":"y"}`, true},
		{"doc edited", ex, `{"content":"x","page_doc":` + twoCol + `}`, true},
	}
	for _, c := range cases {
		if got := structuralChange(c.ex, &UpdateEntityInput{}, json.RawMessage(c.caller)); got != c.want {
			t.Errorf("%s: changed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsSEOAttr(t *testing.T) {
	for k, want := range map[string]bool{
		"meta_title": true, "og_image": true, "seo": true, "custom_jsonld": true,
		"content": false, "body": false, "page_doc": false, "page_sections": false, "title": false,
	} {
		if got := IsSEOAttr(k); got != want {
			t.Errorf("IsSEOAttr(%q) = %v, want %v", k, got, want)
		}
	}
}
