package entity

import (
	"reflect"
	"testing"
)

const (
	structBase      = "https://example.test"
	annotatedSchema = `{"type":"object","x-schema-org-type":"Article"}`
)

// Paths that emit dateModified: settings-typed, legacy article, annotated.
func structuredPaths(settings, attrs string) map[string]*Entity {
	return map[string]*Entity{
		"settings-typed": overrideEntity("glossary", mergeTypeSetting(settings, "DefinedTerm"), `{"type":"object"}`, attrs),
		"article":        overrideEntity("post", settings, `{"type":"object"}`, attrs),
		"annotated":      overrideEntity("docs", settings, annotatedSchema, attrs),
	}
}

func mergeTypeSetting(settings, typ string) string {
	if settings == `{}` {
		return `{"schema_org_type":"` + typ + `"}`
	}
	return settings[:len(settings)-1] + `,"schema_org_type":"` + typ + `"}`
}

func TestModifiedFrom(t *testing.T) {
	// fixture: published 2024-02-01, updated 2025-06-07T08:09:10Z
	cases := []struct{ name, attrs, want string }{
		{"rfc3339 attribute", `{"reviewed":"2025-09-01T10:00:00Z"}`, "2025-09-01T10:00:00Z"},
		{"date-only attribute", `{"reviewed":"2025-09-01"}`, "2025-09-01T00:00:00Z"},
		{"missing attribute", `{}`, "2025-06-07T08:09:10Z"},
		{"unparseable attribute", `{"reviewed":"last tuesday"}`, "2025-06-07T08:09:10Z"},
		{"empty attribute", `{"reviewed":""}`, "2025-06-07T08:09:10Z"},
		{"non-string attribute", `{"reviewed":42}`, "2025-06-07T08:09:10Z"},
		{"earlier than published is clamped", `{"reviewed":"2023-01-01"}`, "2024-02-01T00:00:00Z"},
	}
	for _, tc := range cases {
		for path, ent := range structuredPaths(`{"modified_from":"reviewed"}`, tc.attrs) {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				doc, err := GenerateJSONLD(ent, structBase)
				if err != nil {
					t.Fatal(err)
				}
				if got := doc["dateModified"]; got != tc.want {
					t.Errorf("dateModified = %v, want %s", got, tc.want)
				}
			})
		}
	}
}

// Without the new keys the three emitters keep today's output.
func TestStructuredDefaultsUnchanged(t *testing.T) {
	for path, ent := range structuredPaths(`{}`, `{"reviewed":"2023-01-01"}`) {
		t.Run(path, func(t *testing.T) {
			doc, err := GenerateJSONLD(ent, structBase)
			if err != nil {
				t.Fatal(err)
			}
			if doc["dateModified"] != "2025-06-07T08:09:10Z" {
				t.Errorf("dateModified = %v", doc["dateModified"])
			}
			for _, k := range []string{"inDefinedTermSet"} {
				if _, ok := doc[k]; ok {
					t.Errorf("%s must be absent: %v", k, doc)
				}
			}
			if path != "article" {
				for _, k := range []string{"publisher", "isPartOf"} {
					if _, ok := doc[k]; ok {
						t.Errorf("%s must be absent without keys: %v", k, doc)
					}
				}
			}
		})
	}
	// Exact settings-typed document, as before the change.
	doc, _ := GenerateJSONLD(structuredPaths(`{}`, `{"description":"Def","image":"/a.png"}`)["settings-typed"], structBase)
	want := map[string]any{
		"@context": "https://schema.org", "@type": "DefinedTerm", "name": "Glossary term",
		"url": structBase + "/term", "@id": structBase + "/term#definedterm",
		"description": "Def", "image": "/a.png",
		"datePublished": "2024-02-01T00:00:00Z", "dateModified": "2025-06-07T08:09:10Z",
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("got %v\nwant %v", doc, want)
	}
}

func TestDefinedTermSetAndSiteLinks(t *testing.T) {
	for _, set := range []string{"/glossary", structBase + "/glossary"} {
		ent := overrideEntity("glossary", `{"schema_org_type":"DefinedTerm","term_set_url":"`+set+`"}`, `{"type":"object"}`, `{}`)
		doc, err := GenerateJSONLD(ent, structBase)
		if err != nil {
			t.Fatal(err)
		}
		wantSet := map[string]any{"@type": "DefinedTermSet", "@id": structBase + "/glossary"}
		if !reflect.DeepEqual(doc["inDefinedTermSet"], wantSet) {
			t.Errorf("inDefinedTermSet = %v", doc["inDefinedTermSet"])
		}
		// DefinedTerm is an Intangible, not a CreativeWork: no publisher/isPartOf.
		if doc["publisher"] != nil || doc["isPartOf"] != nil {
			t.Errorf("site links on a DefinedTerm: %v / %v", doc["publisher"], doc["isPartOf"])
		}
	}
	// A CreativeWork type gets the site links once a key is configured.
	cw := overrideEntity("glossary", `{"schema_org_type":"CreativeWork","term_set_url":"/glossary"}`, `{"type":"object"}`, `{}`)
	if d, _ := GenerateJSONLD(cw, structBase); !reflect.DeepEqual(d["publisher"], map[string]any{"@id": structBase + "#organization"}) ||
		!reflect.DeepEqual(d["isPartOf"], map[string]any{"@id": structBase + "#website"}) {
		t.Errorf("site links = %v / %v", d["publisher"], d["isPartOf"])
	}
	// Annotated path gets the refs as well once a key is configured.
	ent := overrideEntity("docs", `{"modified_from":"reviewed"}`, annotatedSchema, `{}`)
	doc, _ := GenerateJSONLD(ent, structBase)
	if doc["isPartOf"] == nil || doc["publisher"] == nil {
		t.Errorf("annotated doc lacks site links: %v", doc)
	}
}

// The plugin enrich hook still runs last, on the final document: its
// overwrite of a structured-data field survives.
func TestEnrichRunsAfterStructuredData(t *testing.T) {
	SetJSONLDEnricher(func(_ *Entity, _ string, doc map[string]any) {
		if doc["dateModified"] != "2025-09-01T00:00:00Z" {
			t.Errorf("enricher saw dateModified %v, want the structured-data value", doc["dateModified"])
		}
		if doc["inDefinedTermSet"] == nil {
			t.Error("enricher did not see the term set")
		}
		doc["dateModified"] = "plugin"
	})
	t.Cleanup(func() { SetJSONLDEnricher(nil) })
	ent := overrideEntity("glossary",
		`{"schema_org_type":"DefinedTerm","modified_from":"reviewed","term_set_url":"/glossary"}`,
		`{"type":"object"}`, `{"reviewed":"2025-09-01"}`)
	doc, err := GenerateJSONLD(ent, structBase)
	if err != nil {
		t.Fatal(err)
	}
	if doc["dateModified"] != "plugin" {
		t.Errorf("dateModified = %v, want the enricher's value", doc["dateModified"])
	}
}

// publisher and isPartOf belong to CreativeWork types: a switch never adds
// them, and a Person or Organization never gets them.
func TestStructuredLinksOnlyOnCreativeWorks(t *testing.T) {
	for _, tc := range []struct {
		name, typ, settings string
		want                bool
	}{
		{"person with breadcrumb off", "Person", `{"emit_breadcrumb":"off"}`, false},
		{"person with webpage on", "Person", `{"emit_webpage":"on"}`, false},
		{"person with modified_from", "Person", `{"modified_from":"reviewed"}`, false},
		{"organization with author_from", "Organization", `{"author_from":"writer"}`, false},
		{"article with only a switch", "Article", `{"emit_breadcrumb":"off"}`, false},
		{"article with modified_from", "Article", `{"modified_from":"reviewed"}`, true},
		{"course with term_set_url", "Course", `{"term_set_url":"/x"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ent := overrideEntity("things", mergeTypeSetting(tc.settings, tc.typ), `{"type":"object"}`, `{}`)
			doc, err := GenerateJSONLD(ent, structBase)
			if err != nil {
				t.Fatal(err)
			}
			if got := doc["publisher"] != nil || doc["isPartOf"] != nil; got != tc.want {
				t.Errorf("publisher/isPartOf present = %v, want %v: %v", got, tc.want, doc)
			}
		})
	}
}
