package entity

import (
	"encoding/json"
	"strings"
	"testing"
)

// The seeded person schema (it carries x-schema-org-type) must still use the
// built-in person mapping.
const personSchemaJSON = `{"type":"object","x-schema-org-type":"Person","properties":{"job_title":{"type":"string"}}}`

func personLD(t *testing.T, attrs string) map[string]any {
	t.Helper()
	e := &Entity{Name: "Ada Lovelace", Slug: "ada", Attributes: json.RawMessage(attrs),
		Schema: &EntitySchema{Slug: "person", Schema: json.RawMessage(personSchemaJSON), Settings: json.RawMessage(`{"url_pattern":"/author/{slug}"}`)}}
	doc, err := GenerateJSONLD(e, "https://x.test")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestPersonJSONLD(t *testing.T) {
	t.Parallel()
	doc := personLD(t, `{"job_title":"Engineer","photo":"/p.jpg","short_bio":"Writes code.","public_email":"ada@example.com",
		"email":"private@example.com","linkedin":"https://l.example/ada","x":"https://x.example/ada","website":"https://ada.example"}`)
	if doc["@type"] != "Person" || doc["name"] != "Ada Lovelace" || doc["jobTitle"] != "Engineer" || doc["image"] != "/p.jpg" {
		t.Errorf("person doc = %v", doc)
	}
	if doc["email"] != "ada@example.com" {
		t.Errorf("email = %v, want the public_email only", doc["email"])
	}
	if doc["description"] != "Writes code." {
		t.Errorf("description = %v", doc["description"])
	}
	same, _ := doc["sameAs"].([]string)
	if len(same) != 3 {
		t.Errorf("sameAs = %v, want linkedin, x and website", doc["sameAs"])
	}
	if w, _ := doc["worksFor"].(map[string]any); w["@id"] != "https://x.test#organization" {
		t.Errorf("worksFor = %v", doc["worksFor"])
	}
}

func TestPersonJSONLDNeverPublishesTheAccountEmail(t *testing.T) {
	t.Parallel()
	doc := personLD(t, `{"email":"private@example.com"}`)
	if _, has := doc["email"]; has {
		t.Errorf("email = %v without a public_email", doc["email"])
	}
	b, _ := json.Marshal(doc)
	if strings.Contains(string(b), "private@example.com") {
		t.Errorf("the email leaked: %s", b)
	}
}

func TestPersonJSONLDDescriptionIsPlainText(t *testing.T) {
	t.Parallel()
	doc := personLD(t, `{"bio":"<p>Hello <b>world</b></p><p>Second</p>"}`)
	if doc["description"] != "Hello world Second" {
		t.Errorf("description = %q", doc["description"])
	}
	long := personLD(t, `{"bio":"<p>`+strings.Repeat("é", 400)+`</p>"}`)
	if d, _ := long["description"].(string); len([]rune(d)) != 300 {
		t.Errorf("description has %d runes, want 300", len([]rune(d)))
	}
	if d, _ := personLD(t, `{"short_bio":"`+strings.Repeat("a", 500)+`"}`)["description"].(string); len([]rune(d)) != 300 {
		t.Errorf("short_bio description has %d runes, want 300", len([]rune(d)))
	}
}
