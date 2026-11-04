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
