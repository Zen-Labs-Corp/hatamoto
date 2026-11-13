package entity

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestStructuredSettingsWrite(t *testing.T) {
	cases := []struct {
		name, raw, field string
	}{
		{"all valid", `{"modified_from":"updated","author_from":"writer","term_set_url":"/glossary","emit_webpage":"on","emit_breadcrumb":"off"}`, ""},
		{"absolute term set", `{"term_set_url":"https://example.test/glossary"}`, ""},
		{"empty values", `{"modified_from":"","emit_webpage":""}`, ""},
		{"null values", `{"modified_from":null,"emit_webpage":null}`, ""},
		{"bad modified_from", `{"modified_from":"has space"}`, "modified_from"},
		{"padded modified_from", `{"modified_from":" updated"}`, "modified_from"},
		{"padded author_from", `{"author_from":"writer "}`, "author_from"},
		{"padded term set", `{"term_set_url":"/glossary "}`, "term_set_url"},
		{"padded switch", `{"emit_webpage":"off "}`, "emit_webpage"},
		{"bad author_from", `{"author_from":"a.b"}`, "author_from"},
		{"non-string modified_from", `{"modified_from":3}`, "modified_from"},
		{"relative term set", `{"term_set_url":"glossary"}`, "term_set_url"},
		{"protocol-relative term set", `{"term_set_url":"//example.test/g"}`, "term_set_url"},
		{"ftp term set", `{"term_set_url":"ftp://example.test/g"}`, "term_set_url"},
		{"bad webpage switch", `{"emit_webpage":"yes"}`, "emit_webpage"},
		{"bad breadcrumb switch", `{"emit_breadcrumb":true}`, "emit_breadcrumb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchemaSettingsForWrite(jsontext.Value(tc.raw))
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			se, ok := err.(*SettingsError)
			if !ok || se.Field != tc.field {
				t.Fatalf("err = %v, want SettingsError on %q", err, tc.field)
			}
		})
	}
}

func TestStructuredSettingsLenientRead(t *testing.T) {
	s := &EntitySchema{Settings: jsontext.Value(`{"url_pattern":"/x/{slug}","modified_from":7,"author_from":{},"term_set_url":[],"emit_webpage":"maybe","emit_breadcrumb":false}`)}
	st, err := s.URLSettings()
	if err != nil {
		t.Fatalf("URLSettings failed on garbage: %v", err)
	}
	if st.URLPattern != "/x/{slug}" {
		t.Errorf("url_pattern lost: %q", st.URLPattern)
	}
	if st.StructuredDataConfigured() {
		t.Errorf("garbage must read as unset: %+v", st)
	}
}

func TestStructuredSettingsRoundTrip(t *testing.T) {
	s := &EntitySchema{Settings: jsontext.Value(`{"url_pattern":"/x/{slug}","schema_org_type":"Article"}`)}
	st, _ := s.URLSettings()
	if st.StructuredDataConfigured() {
		t.Fatal("no keys set, must not be configured")
	}
	out, err := jsonx.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"modified_from", "author_from", "term_set_url", "emit_webpage", "emit_breadcrumb"} {
		if strings.Contains(string(out), `"`+k+`"`) {
			t.Errorf("unset %s serialised: %s", k, out)
		}
	}
	sw := &EntitySchema{Settings: jsontext.Value(`{"emit_webpage":"off","emit_breadcrumb":"on"}`)}
	if stw, _ := sw.URLSettings(); stw.StructuredDataConfigured() {
		t.Error("the node switches alone must not count as configured")
	}
	s2 := &EntitySchema{Settings: jsontext.Value(`{"modified_from":"updated","emit_webpage":"off"}`)}
	st2, _ := s2.URLSettings()
	if st2.ModifiedFrom != "updated" || st2.EmitWebPage != NodeSwitchOff || !st2.StructuredDataConfigured() {
		t.Errorf("decode: %+v", st2)
	}
}
