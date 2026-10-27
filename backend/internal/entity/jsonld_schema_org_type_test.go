package entity

import (
	"encoding/json"
	"testing"
	"time"
)

func settingsTypedEntity(slug, settings, schemaJSON string) *Entity {
	pub := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	return &Entity{
		Name: "Glossary term", Slug: "term", PublishedAt: &pub,
		UpdatedAt:  time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC),
		Attributes: json.RawMessage(`{"description":"Def","image":"/a.png"}`),
		Schema: &EntitySchema{Slug: slug, Name: slug,
			Schema: json.RawMessage(schemaJSON), Settings: json.RawMessage(settings)},
	}
}

func TestGenerateJSONLD_SchemaOrgTypeSetting(t *testing.T) {
	ent := settingsTypedEntity("glossary", `{"schema_org_type":"DefinedTerm"}`, `{"type":"object"}`)
	doc, err := GenerateJSONLD(ent, "https://e.com")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"@type": "DefinedTerm", "name": "Glossary term", "description": "Def", "image": "/a.png",
		"datePublished": "2024-02-01T00:00:00Z", "dateModified": "2025-06-07T08:09:10Z",
		"url": "https://e.com/term", "@id": "https://e.com/term#definedterm",
	}
	for k, v := range want {
		if doc[k] != v {
			t.Errorf("%s = %v, want %v", k, doc[k], v)
		}
	}
}

func TestGenerateJSONLD_SchemaOrgTypePrecedence(t *testing.T) {
	// annotations beat the setting
	ent := settingsTypedEntity("glossary", `{"schema_org_type":"DefinedTerm"}`, `{"type":"object","x-schema-org-type":"Course"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "Course" {
		t.Errorf("annotation lost: %v", doc["@type"])
	}
	// plugin builder beats the setting
	RegisterJSONLDBuilder("glossary-pb", func(doc map[string]any, _ *Entity, _ map[string]any, _ string) {
		doc["@type"] = "FromPlugin"
	})
	t.Cleanup(func() { unregisterJSONLDBuilderForTest("glossary-pb") })
	ent = settingsTypedEntity("glossary-pb", `{"schema_org_type":"DefinedTerm"}`, `{"type":"object"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "FromPlugin" {
		t.Errorf("plugin builder lost: %v", doc["@type"])
	}
	// no setting -> Thing
	ent = settingsTypedEntity("glossary", `{}`, `{"type":"object"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "Thing" {
		t.Errorf("default = %v", doc["@type"])
	}
}

func unregisterJSONLDBuilderForTest(slug string) {
	jsonldBuildersMu.Lock()
	defer jsonldBuildersMu.Unlock()
	delete(jsonldBuilders, slug)
}

func TestGenerateJSONLD_BuilderBeatsAnnotations(t *testing.T) {
	RegisterJSONLDBuilder("glossary-pa", func(doc map[string]any, _ *Entity, _ map[string]any, _ string) {
		doc["@type"] = "FromPlugin"
	})
	t.Cleanup(func() { unregisterJSONLDBuilderForTest("glossary-pa") })
	ent := settingsTypedEntity("glossary-pa", `{"schema_org_type":"DefinedTerm"}`, `{"type":"object","x-schema-org-type":"Course"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "FromPlugin" {
		t.Errorf("@type = %v, want FromPlugin", doc["@type"])
	}
}

func TestGenerateJSONLD_ArticleTypesGetHeadline(t *testing.T) {
	ent := settingsTypedEntity("notes", `{"schema_org_type":"BlogPosting"}`, `{"type":"object"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["headline"] != ent.Name {
		t.Errorf("headline = %v", doc["headline"])
	}
}
