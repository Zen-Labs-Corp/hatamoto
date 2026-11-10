package sdk

import (
	"slices"
	"testing"

	jsonv2 "encoding/json/v2"
)

func TestEntityTermJSON(t *testing.T) {
	b, err := jsonv2.Marshal(EntityTerm{Kind: "category", Slug: "news", Name: "News"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"kind":"category","slug":"news","name":"News"}`; got != want {
		t.Errorf("EntityTerm = %s, want %s", got, want)
	}
	b, err = jsonv2.Marshal(EntityTermFilter{Kind: "tag", Slug: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"kind":"tag","slug":"go"}`; got != want {
		t.Errorf("EntityTermFilter = %s, want %s", got, want)
	}
	var back EntityTerm
	if err := jsonv2.Unmarshal([]byte(`{"kind":"tag","slug":"go","name":"Go"}`), &back); err != nil || back != (EntityTerm{"tag", "go", "Go"}) {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

// Terms are read with entities.get; the entity.* event payload must not grow.
func TestEntityEventHasNoTerms(t *testing.T) {
	b, err := jsonv2.Marshal(EntityEvent{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := jsonv2.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"id", "locale", "name", "schema_slug", "slug", "status", "version"}
	if !slices.Equal(keys, want) {
		t.Errorf("EntityEvent keys = %v, want %v", keys, want)
	}
}
