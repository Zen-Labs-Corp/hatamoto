package entity

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateSchemaSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty", ``, false},
		{"empty object", `{}`, false},
		{"full", `{"url_pattern":"/docs/{slug}","archive_path":"/docs","sitemap_group":"docs","aliases":["doc"],"overrides":[{"attribute":"format","equals":"report","url_pattern":"/reports/{slug}","sitemap_group":"misc"}],"sitemap":{"priority":0.7,"changefreq":"weekly"},"section_pages":true}`, false},
		{"unknown keys ignored", `{"x":1}`, false},
		{"not object", `[]`, true},
		{"archive title ok", `{"archive_title":"Our team","archive_description":"About"}`, false},
		{"archive title too long", `{"archive_title":"` + strings.Repeat("a", 201) + `"}`, true},
		{"archive title 200 padded ok", `{"archive_title":"  ` + strings.Repeat("a", 200) + `  "}`, false},
		{"archive description too long", `{"archive_description":"` + strings.Repeat("a", 501) + `"}`, true},
		{"crumb label ok", `{"archive_crumb_label":"Team"}`, false},
		{"crumb label too long", `{"archive_crumb_label":"` + strings.Repeat("a", 201) + `"}`, true},
		{"archive title wrong type", `{"archive_title":5}`, true},
		{"pattern no slash", `{"url_pattern":"post/{slug}"}`, true},
		{"pattern no slug", `{"url_pattern":"/post"}`, true},
		{"unknown token", `{"url_pattern":"/{city}/{slug}"}`, true},
		{"unterminated", `{"url_pattern":"/{slug"}`, true},
		{"mixed segment", `{"url_pattern":"/x-{slug}"}`, true},
		{"override missing attribute", `{"overrides":[{"equals":"a","url_pattern":"/a/{slug}"}]}`, true},
		{"override bad pattern", `{"overrides":[{"attribute":"a","equals":"b","url_pattern":"/a"}]}`, true},
		{"archive no slash", `{"archive_path":"post"}`, true},
		{"empty alias", `{"aliases":[" "]}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateSchemaSettings(json.RawMessage(tc.in))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidSchema) {
				t.Fatalf("error should wrap ErrInvalidSchema: %v", err)
			}
		})
	}
}

func TestURLSettings(t *testing.T) {
	t.Parallel()
	var nilSchema *EntitySchema
	if st, err := nilSchema.URLSettings(); err != nil || st.URLPattern != "" {
		t.Fatalf("nil schema: %+v %v", st, err)
	}
	s := &EntitySchema{Settings: json.RawMessage(`{"url_pattern":"/x/{slug}","aliases":["y"],"sitemap":{"exclude":true}}`)}
	st, err := s.URLSettings()
	if err != nil || st.URLPattern != "/x/{slug}" || len(st.Aliases) != 1 || !st.Sitemap.Exclude {
		t.Fatalf("got %+v %v", st, err)
	}
}

func TestValidateSchemaSettingsForWrite_SchemaOrgType(t *testing.T) {
	if err := ValidateSchemaSettingsForWrite([]byte(`{"schema_org_type":"BlogPosting"}`)); err != nil {
		t.Errorf("allowed type rejected: %v", err)
	}
	for _, bad := range []string{"Spaceship", "FAQPage", "Product", "Event"} {
		if err := ValidateSchemaSettingsForWrite([]byte(`{"schema_org_type":"` + bad + `"}`)); err == nil {
			t.Errorf("%s accepted on write", bad)
		}
	}
	// Load-side validation ignores the type.
	if err := ValidateSchemaSettings([]byte(`{"schema_org_type":"Spaceship"}`)); err != nil {
		t.Errorf("load-side validation rejected unknown type: %v", err)
	}
}

// An unknown schema_org_type must not reset the schema's URL config.
func TestBuildURLSnapshot_UnknownSchemaOrgTypeKeepsURLConfig(t *testing.T) {
	schemas := []*EntitySchema{{
		Slug:     "glossary",
		Settings: json.RawMessage(`{"url_pattern":"/glossary/{slug}","schema_org_type":"Spaceship"}`),
	}}
	snap, err := BuildURLSnapshot(schemas, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.EntityPath(&Entity{Slug: "x", Schema: schemas[0]}, nil)
	if got != "/glossary/x" {
		t.Errorf("path = %q, want /glossary/x", got)
	}
	// And JSON-LD ignores the unknown type.
	ent := &Entity{Name: "X", Slug: "x", Schema: schemas[0], Attributes: json.RawMessage(`{}`)}
	doc, err := GenerateJSONLD(ent, "https://e.com")
	if err != nil {
		t.Fatal(err)
	}
	if doc["@type"] != "Thing" {
		t.Errorf("@type = %v, want Thing", doc["@type"])
	}
}

func TestValidateSchemaSettingsForWrite_Editor(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{`{"editor":""}`, `{"editor":"post"}`, `{}`} {
		if err := ValidateSchemaSettingsForWrite([]byte(ok)); err != nil {
			t.Errorf("%s: unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{`{"editor":"tiptap"}`, `{"editor":"Post"}`, `{"editor":true}`} {
		err := ValidateSchemaSettingsForWrite([]byte(bad))
		var se *SettingsError
		if !errors.As(err, &se) || se.Field != "editor" {
			t.Errorf("%s: want SettingsError on field editor, got %v", bad, err)
		}
		// Load-side validation ignores it.
		if err := ValidateSchemaSettings([]byte(bad)); err != nil {
			t.Errorf("%s: load-side validation rejected it: %v", bad, err)
		}
	}
}

// A bad stored editor value must not reset the schema's URL config.
func TestBuildURLSnapshot_BadEditorKeepsURLConfig(t *testing.T) {
	for _, ed := range []string{`"Post"`, `"tiptap"`, `true`} {
		schemas := []*EntitySchema{{
			Slug:     "guide",
			Settings: json.RawMessage(`{"url_pattern":"/guides/{slug}","editor":` + ed + `}`),
		}}
		snap, err := BuildURLSnapshot(schemas, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := snap.EntityPath(&Entity{Slug: "x", Schema: schemas[0]}, nil); got != "/guides/x" {
			t.Errorf("editor %s: path = %q, want /guides/x", ed, got)
		}
	}
}

func TestValidateEditorSetting(t *testing.T) {
	t.Parallel()
	withContent := json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)
	cases := []struct {
		name, slug, settings string
		schema               json.RawMessage
		ok                   bool
	}{
		{"post on a schema with content", "guide", `{"editor":"post"}`, withContent, true},
		{"no editor setting", "person", `{}`, json.RawMessage(`{}`), true},
		{"person is refused", "person", `{"editor":"post"}`, withContent, false},
		{"no content property", "guide", `{"editor":"post"}`, json.RawMessage(`{"properties":{"body":{"type":"string"}}}`), false},
		{"content is not a string", "guide", `{"editor":"post"}`, json.RawMessage(`{"properties":{"content":{"type":"array"}}}`), false},
	}
	for _, c := range cases {
		err := ValidateEditorSetting(c.slug, c.schema, json.RawMessage(c.settings))
		var se *SettingsError
		switch {
		case c.ok && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case !c.ok && (!errors.As(err, &se) || se.Field != "editor"):
			t.Errorf("%s: want SettingsError on editor, got %v", c.name, err)
		}
	}
}

func TestFAQEnabledDefaultsOn(t *testing.T) {
	t.Parallel()
	var st SchemaURLSettings
	if !st.FAQEnabled() {
		t.Error("absent key must read as on")
	}
	if err := json.Unmarshal([]byte(`{"faq_from_content":false}`), &st); err != nil || st.FAQEnabled() {
		t.Errorf("false must read as off: %v", err)
	}
}
