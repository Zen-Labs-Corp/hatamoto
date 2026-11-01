package server

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestApplyAttributeMetaSocialText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, attrs, wantTitle, wantDesc string
	}{
		{"both", `{"og_title":" Share me ","og_description":"Short pitch"}`, "Share me", "Short pitch"},
		{"title only", `{"og_title":"T"}`, "T", ""},
		{"blank ignored", `{"og_title":"  ","og_description":""}`, "", ""},
		{"absent", `{"meta_title":"M"}`, "", ""},
		{"wrong type ignored", `{"og_title":5}`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var m render.PageMeta
			applyAttributeMeta(&m, json.RawMessage(tc.attrs), "https://site.test/")
			if m.OGTitle != tc.wantTitle || m.OGDescription != tc.wantDesc {
				t.Fatalf("got %q / %q, want %q / %q", m.OGTitle, m.OGDescription, tc.wantTitle, tc.wantDesc)
			}
		})
	}
}
