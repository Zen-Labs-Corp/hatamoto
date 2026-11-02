package entity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func overrideEntity(slug, settings, schemaJSON, attrs string) *Entity {
	e := settingsTypedEntity(slug, settings, schemaJSON)
	e.Attributes = json.RawMessage(attrs)
	return e
}

func TestGenerateJSONLD_NoSettingsUnchanged(t *testing.T) {
	for _, slug := range []string{"page", "post", "glossary"} {
		base, err := generateBaseJSONLD(overrideEntity(slug, `{}`, `{"type":"object"}`, `{"description":"d"}`), "https://e.com")
		if err != nil {
			t.Fatal(err)
		}
		got, err := GenerateJSONLD(overrideEntity(slug, `{}`, `{"type":"object"}`, `{"description":"d"}`), "https://e.com")
		if err != nil || !reflect.DeepEqual(got, base) {
			t.Errorf("%s: %v != %v (%v)", slug, got, base, err)
		}
	}
}

func TestGenerateJSONLD_ArticleSubtype(t *testing.T) {
	ent := overrideEntity("docs", `{"schema_org_type":"Article","article_type":"NewsArticle"}`, `{"type":"object"}`, `{}`)
	doc, _ := GenerateJSONLD(ent, "https://e.com")
	if doc["@type"] != "NewsArticle" {
		t.Errorf("@type = %v", doc["@type"])
	}
	// Non-article documents keep their type.
	ent = overrideEntity("docs", `{"schema_org_type":"Service","article_type":"NewsArticle"}`, `{"type":"object"}`, `{}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "Service" {
		t.Errorf("@type = %v", doc["@type"])
	}
}

func TestGenerateJSONLD_EntryTypeOverride(t *testing.T) {
	plain, _ := GenerateJSONLD(overrideEntity("docs", `{"schema_org_type":"Article"}`, `{"type":"object"}`, `{}`), "https://e.com")
	ent := overrideEntity("docs", `{"schema_org_type":"Article","article_type":"BlogPosting"}`, `{"type":"object"}`, `{"schema_type":"Course"}`)
	doc, _ := GenerateJSONLD(ent, "https://e.com")
	if doc["@type"] != "Course" {
		t.Fatalf("@type = %v", doc["@type"])
	}
	for k, v := range plain {
		if k != "@type" && !reflect.DeepEqual(doc[k], v) {
			t.Errorf("%s changed: %v != %v", k, doc[k], v)
		}
	}
	// Unlisted value is ignored.
	ent = overrideEntity("docs", `{"schema_org_type":"Article"}`, `{"type":"object"}`, `{"schema_type":"Product"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "Article" {
		t.Errorf("@type = %v", doc["@type"])
	}
}

func TestGenerateJSONLD_OverrideSkipsPluginBuilder(t *testing.T) {
	RegisterJSONLDBuilder("override-pb", func(doc map[string]any, _ *Entity, _ map[string]any, _ string) {
		doc["@type"] = "FromPlugin"
	})
	t.Cleanup(func() { unregisterJSONLDBuilderForTest("override-pb") })
	ent := overrideEntity("override-pb", `{}`, `{"type":"object"}`, `{"schema_type":"Course"}`)
	if doc, _ := GenerateJSONLD(ent, "https://e.com"); doc["@type"] != "FromPlugin" {
		t.Errorf("@type = %v", doc["@type"])
	}
}

func TestValidateCustomJSONLD(t *testing.T) {
	t.Parallel()
	ok := []string{
		``, `  `,
		`{"@context":"https://schema.org","@type":"Thing"}`,
		`[{"@context":"https://schema.org","@type":"A"},{"@context":{"@vocab":"x"},"@type":["B"]}]`,
	}
	for _, s := range ok {
		if err := ValidateCustomJSONLD(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{
		`{`, `"x"`, `3`, `[]`, `[1]`, `{"@type":"Thing"}`, `{"@context":"https://schema.org"}`,
		`{"@context":"","@type":"Thing"}`, `[{"@context":"c","@type":"T"},{"@context":"c"}]`,
	}
	for _, s := range bad {
		if err := ValidateCustomJSONLD(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestValidateCustomJSONLDSizeCap(t *testing.T) {
	t.Parallel()
	head := `{"@context":"c","@type":"T","x":"`
	pad := func(total int) string {
		return head + strings.Repeat("a", total-len(head)-2) + `"}`
	}
	if err := ValidateCustomJSONLD(pad(MaxCustomJSONLDBytes)); err != nil {
		t.Errorf("at cap: %v", err)
	}
	if err := ValidateCustomJSONLD(pad(MaxCustomJSONLDBytes + 1)); err == nil {
		t.Error("over cap accepted")
	}
}

func TestCustomJSONLDDocs(t *testing.T) {
	t.Parallel()
	payload := `{"@context":"https://schema.org", "@type":"Thing", "name":"</script><b>"}`
	attrs, _ := json.Marshal(map[string]string{"custom_jsonld": payload})
	docs := CustomJSONLDDocs(attrs)
	if len(docs) != 1 {
		t.Fatalf("docs = %v", docs)
	}
	var got map[string]any
	if err := json.Unmarshal(docs[0], &got); err != nil || got["name"] != "</script><b>" {
		t.Fatalf("doc = %s, %v", docs[0], err)
	}
	if strings.Contains(string(docs[0]), ": ") {
		t.Errorf("not compact: %s", docs[0])
	}
	for _, a := range []string{``, `{}`, `{"custom_jsonld":5}`, `{"custom_jsonld":"{"}`} {
		if CustomJSONLDDocs(json.RawMessage(a)) != nil {
			t.Errorf("%q yielded docs", a)
		}
	}
}
