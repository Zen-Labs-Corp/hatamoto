package server

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestAttrsNoIndex(t *testing.T) {
	cases := []struct {
		name  string
		attrs string
		want  bool
	}{
		{"no_index true", `{"no_index":true}`, true},
		{"no_index false", `{"no_index":false}`, false},
		{"robots noindex follow", `{"seo":{"robots":"noindex, follow"}}`, true},
		{"robots uppercase", `{"seo":{"robots":"NOINDEX"}}`, true},
		{"robots index", `{"seo":{"robots":"index,follow"}}`, false},
		{"absent", `{}`, false},
		{"invalid json", `{not json`, false},
		{"empty", ``, false},
		{"wrong type no_index", `{"no_index":"yes"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attrsNoIndex(json.RawMessage(tc.attrs)); got != tc.want {
				t.Fatalf("attrsNoIndex(%q) = %v, want %v", tc.attrs, got, tc.want)
			}
		})
	}
}

func TestApplyAttributeMetaCanonical(t *testing.T) {
	for _, tc := range []struct{ attrs, want string }{
		{`{"canonical_url":"https://other.example/x"}`, "https://other.example/x"},
		{`{"canonical_url":"/guides/x"}`, "https://site.test/guides/x"},
		{`{"canonical_url":"//evil.example/x"}`, "https://site.test/own"},
		{`{"canonical_url":"javascript:alert(1)"}`, "https://site.test/own"},
		{`{}`, "https://site.test/own"},
	} {
		m := render.PageMeta{CanonicalURL: "https://site.test/own"}
		applyAttributeMeta(&m, json.RawMessage(tc.attrs), "https://site.test/")
		if m.CanonicalURL != tc.want {
			t.Errorf("%s: canonical %q, want %q", tc.attrs, m.CanonicalURL, tc.want)
		}
	}
}
