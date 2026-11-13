package entity

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

var updateJSONLD = flag.Bool("update", false, "rewrite the JSON-LD golden files")

func checkJSONLDGolden(t *testing.T, name string, doc map[string]any) {
	t.Helper()
	got, err := jsonx.Indent(doc)
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", name+".json")
	if *updateJSONLD {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch (run with -update)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestGoldenJSONLD(t *testing.T) {
	cases := []struct {
		name string
		ent  *Entity
		faq  bool
	}{
		{"jsonld-article",
			overrideEntity("post", `{"modified_from":"reviewed"}`, `{"type":"object"}`,
				`{"description":"A short guide","reviewed":"2025-09-01","author":"Alex Example"}`), false},
		{"jsonld-definedterm",
			overrideEntity("glossary", `{"schema_org_type":"DefinedTerm","term_set_url":"/glossaire"}`, `{"type":"object"}`,
				`{"description":"Une définition"}`), false},
		{"jsonld-person",
			overrideEntity("person", `{"schema_org_type":"Person"}`, `{"type":"object"}`,
				`{"description":"Writer","jobTitle":"Editor"}`), false},
		{"jsonld-faqpage",
			overrideEntity("page", `{}`, `{"type":"object"}`,
				`{"faq":[{"question":"What is it?","answer":"A thing."}],"page_doc":{"v":2,"nodes":[{"id":"a","t":"component","p":{"content":{"faq":[{"question":"Où ?","answer":"Ici."}]}}}]}}`), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				doc map[string]any
				err error
			)
			if tc.faq {
				attrs := faqAttrs(t, string(tc.ent.Attributes))
				doc, err = GenerateJSONLDWithFAQ(tc.ent, structBase, FAQItemsFromAttributes(attrs))
			} else {
				doc, err = GenerateJSONLD(tc.ent, structBase)
			}
			if err != nil {
				t.Fatal(err)
			}
			checkJSONLDGolden(t, tc.name, doc)
		})
	}
}
