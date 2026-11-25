package packages

import (
	"encoding/json/jsontext"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func decodeTop(t *testing.T, v jsontext.Value) map[string]any {
	t.Helper()
	var m map[string]any
	if err := jsonx.Unmarshal(v, &m, jsonx.Stored); err != nil {
		t.Fatalf("decode %s: %v", v, err)
	}
	return m
}

func mustMerge(t *testing.T, stored, shipped string) SchemaMerge {
	t.Helper()
	m, err := MergeSchema(jsontext.Value(stored), jsontext.Value(shipped))
	if err != nil {
		t.Fatalf("MergeSchema: %v", err)
	}
	return m
}

func propsOf(t *testing.T, v jsontext.Value) map[string]any {
	t.Helper()
	p, _ := decodeTop(t, v)["properties"].(map[string]any)
	return p
}

func TestMergeSchemaAddsProperty(t *testing.T) {
	stored := `{"type":"object","properties":{"price":{"type":"number"}},"required":["price"]}`
	shipped := `{"properties":{"price":{"type":"number"},"sku":{"type":"string","title":"SKU"}},"required":["price","sku"]}`
	m := mustMerge(t, stored, shipped)
	if !reflect.DeepEqual(m.Added, []string{"sku"}) || !reflect.DeepEqual(m.Labels, []string{"SKU"}) {
		t.Fatalf("added = %v labels = %v", m.Added, m.Labels)
	}
	if _, ok := propsOf(t, m.Merged)["sku"]; !ok {
		t.Fatalf("merged lacks sku: %s", m.Merged)
	}
	if got := decodeTop(t, m.Merged)["required"]; !reflect.DeepEqual(got, []any{"price"}) {
		t.Fatalf("required = %v", got)
	}
}

func TestMergeSchemaAppendsToOrder(t *testing.T) {
	stored := `{"properties":{"a":{"type":"string"},"b":{"type":"string"}},"x-hm-order":["a","b"]}`
	shipped := `{"properties":{"a":{"type":"string"},"c":{"type":"string"},"b":{"type":"string"},"d":{"type":"string"}}}`
	m := mustMerge(t, stored, shipped)
	if got := decodeTop(t, m.Merged)["x-hm-order"]; !reflect.DeepEqual(got, []any{"a", "b", "c", "d"}) {
		t.Fatalf("order = %v", got)
	}
}

func TestMergeSchemaIdempotent(t *testing.T) {
	stored := `{"properties":{"a":{"type":"string"}}}`
	shipped := `{"properties":{"a":{"type":"string"}}}`
	if m := mustMerge(t, stored, shipped); m.Merged != nil || len(m.Added) != 0 {
		t.Fatalf("unchanged schema merged: %+v", m)
	}
	shipped = `{"properties":{"a":{"type":"string"},"n":{"type":"string"}}}`
	first := mustMerge(t, stored, shipped)
	if first.Merged == nil {
		t.Fatal("no merge")
	}
	if again := mustMerge(t, string(first.Merged), shipped); again.Merged != nil {
		t.Fatalf("second merge not empty: %s", again.Merged)
	}
}

func TestMergeSchemaKeepsRetyped(t *testing.T) {
	stored := `{"properties":{"price":{"type":"number"}}}`
	shipped := `{"properties":{"price":{"type":"string"},"sku":{"type":"string"}}}`
	m := mustMerge(t, stored, shipped)
	if !reflect.DeepEqual(m.Retyped, []string{"price"}) {
		t.Fatalf("retyped = %v", m.Retyped)
	}
	if got := propsOf(t, m.Merged)["price"]; !reflect.DeepEqual(got, map[string]any{"type": "number"}) {
		t.Fatalf("price = %v", got)
	}
}

func TestMergeSchemaKeepsMissingAndOwnerFields(t *testing.T) {
	stored := `{"properties":{"price":{"type":"number"},"note":{"type":"string"}}}`
	shipped := `{"properties":{"sku":{"type":"string"}}}`
	m := mustMerge(t, stored, shipped)
	if !reflect.DeepEqual(m.Missing, []string{"note", "price"}) {
		t.Fatalf("missing = %v", m.Missing)
	}
	p := propsOf(t, m.Merged)
	for _, k := range []string{"price", "note", "sku"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("merged lacks %s: %s", k, m.Merged)
		}
	}
}

func TestMergeSchemaRequiredUntouched(t *testing.T) {
	m := mustMerge(t, `{"properties":{"a":{"type":"string"}}}`, `{"properties":{"b":{"type":"string"}},"required":["b"]}`)
	if _, ok := decodeTop(t, m.Merged)["required"]; ok {
		t.Fatalf("required appeared: %s", m.Merged)
	}
}

func TestMergeSchemaWithoutStoredProperties(t *testing.T) {
	m := mustMerge(t, `{"type":"object"}`, `{"properties":{"b":{"type":"string"}}}`)
	if _, ok := propsOf(t, m.Merged)["b"]; !ok {
		t.Fatalf("merged = %s", m.Merged)
	}
}

func TestMergeSchemaInvalidJSON(t *testing.T) {
	good := jsontext.Value(`{"properties":{}}`)
	if _, err := MergeSchema(jsontext.Value(`{`), good); err == nil {
		t.Fatal("invalid stored accepted")
	}
	if _, err := MergeSchema(good, jsontext.Value(`{"properties":`)); err == nil {
		t.Fatal("invalid shipped accepted")
	}
}

func TestMergeSchemaLabels(t *testing.T) {
	m := mustMerge(t, `{"properties":{}}`, `{"properties":{"join_link":{"type":"string"},"sku":{"type":"string","title":"SKU code"}}}`)
	if !reflect.DeepEqual(m.Labels, []string{"Join link", "SKU code"}) {
		t.Fatalf("labels = %v", m.Labels)
	}
}
