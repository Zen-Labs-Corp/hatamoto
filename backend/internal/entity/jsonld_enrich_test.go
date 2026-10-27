package entity

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func enrichTestEntity() *Entity {
	return &Entity{
		ID: uuid.New(), Name: "Hello", Slug: "hello", Status: StatusPublished,
		Attributes: json.RawMessage(`{"title":"Hello"}`),
		Schema:     &EntitySchema{Slug: "page", Schema: json.RawMessage(`{"type":"object"}`)},
	}
}

// ISC-14: the enricher runs exactly once per GenerateJSONLD call and its
// mutations reach the caller's document.
func TestGenerateJSONLDAppliesEnricherOncePerCall(t *testing.T) {
	calls := 0
	SetJSONLDEnricher(func(_ *Entity, _ string, doc map[string]any) {
		calls++
		doc["x-enriched"] = true
	})
	t.Cleanup(func() { SetJSONLDEnricher(nil) })

	for i := 1; i <= 2; i++ {
		doc, err := GenerateJSONLD(enrichTestEntity(), "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if doc["x-enriched"] != true {
			t.Fatalf("call %d: doc not enriched: %v", i, doc)
		}
		if calls != i {
			t.Fatalf("after call %d enricher ran %d times", i, calls)
		}
	}
}

func TestGenerateJSONLDSkipsEnricherOnError(t *testing.T) {
	SetJSONLDEnricher(func(*Entity, string, map[string]any) { t.Error("enricher ran on error path") })
	t.Cleanup(func() { SetJSONLDEnricher(nil) })
	if _, err := GenerateJSONLD(nil, ""); err == nil {
		t.Fatal("expected error for nil entity")
	}
}

func TestGenerateJSONLDWithoutEnricher(t *testing.T) {
	SetJSONLDEnricher(nil)
	doc, err := GenerateJSONLD(enrichTestEntity(), "https://example.com")
	if err != nil || doc == nil {
		t.Fatalf("doc=%v err=%v", doc, err)
	}
}
