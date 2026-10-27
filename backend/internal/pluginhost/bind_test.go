package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

func bindRig(t *testing.T) *hooks.Engine {
	t.Helper()
	e := hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	BindHostHooks(e)
	t.Cleanup(func() { entity.SetJSONLDEnricher(nil) })
	return e
}

func bindEntity() *entity.Entity {
	return &entity.Entity{
		ID: uuid.New(), Name: "Hello", Slug: "hello", Locale: "fr", Status: entity.StatusPublished,
		Attributes: json.RawMessage(`{}`),
		Schema:     &entity.EntitySchema{Slug: "page", Schema: json.RawMessage(`{"type":"object"}`)},
	}
}

// ISC-14/15: filters registered through the engine reach GenerateJSONLD, run
// once per call, and a failing filter leaves the doc untouched.
func TestJSONLDEnrichFilterThroughEngine(t *testing.T) {
	e := bindRig(t)
	calls := 0
	hooks.OnFilter(e.For("seo"), sdk.JSONLDEnrich, 10, func(_ context.Context, d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		calls++
		if d.Entity.SchemaSlug != "page" || d.Entity.Slug != "hello" || d.Entity.Locale != "fr" || d.BaseURL != "https://example.com" || d.Doc == nil {
			t.Errorf("payload = %+v", d)
		}
		d.Doc["x-seo"] = "yes"
		return d, nil
	})
	hooks.OnFilter(e.For("bad"), sdk.JSONLDEnrich, 20, func(_ context.Context, d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		d.Doc["x-bad"] = true
		return d, errors.New("boom") // error: value before this filter is kept
	})

	for i := 1; i <= 2; i++ {
		doc, err := entity.GenerateJSONLD(bindEntity(), "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if doc["x-seo"] != "yes" {
			t.Fatalf("doc = %v", doc)
		}
		if calls != i {
			t.Fatalf("filter ran %d times after %d calls", calls, i)
		}
	}
}

func TestJSONLDEnrichReplacementMapIsCopiedBack(t *testing.T) {
	e := bindRig(t)
	hooks.OnFilter(e.For("swap"), sdk.JSONLDEnrich, 10, func(_ context.Context, d sdk.JSONLDDoc) (sdk.JSONLDDoc, error) {
		d.Doc = map[string]any{"@type": "Replaced"}
		return d, nil
	})
	doc, err := entity.GenerateJSONLD(bindEntity(), "")
	if err != nil {
		t.Fatal(err)
	}
	if doc["@type"] != "Replaced" || len(doc) != 1 {
		t.Fatalf("doc = %v", doc)
	}
}
