package server

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestFallbackDescriptionBuilderDocIsPlain(t *testing.T) {
	var attrs map[string]any
	doc := `{"page_doc":{"v":2,"nodes":[
		{"id":"a","t":"heading","p":{"text":"Work","level":2}},
		{"id":"b","t":"text","p":{"html":"<p>Three <strong>shops</strong> and a <a href=\"https://acme.example/p\">portfolio</a> in snake_case.</p>"}}]}}`
	if err := jsonx.Unmarshal([]byte(doc), &attrs, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	const want = "Three shops and a portfolio in snake_case."
	if got := fallbackDescription(attrs, nil); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
