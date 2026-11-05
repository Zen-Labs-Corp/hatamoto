package pluginhost

import (
	"encoding/json/jsontext"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

const testSchema = `{
  "type": "object",
  "properties": {
    "greeting": {"type": "string", "default": "hi", "maxLength": 10},
    "retries":  {"type": "integer", "minimum": 0, "default": 3},
    "api_key":  {"type": "string", "writeOnly": true}
  },
  "required": ["greeting"],
  "additionalProperties": false
}`

func TestCompileSchemaErrors(t *testing.T) {
	if _, err := CompileSchema([]byte(`{not json`)); err == nil {
		t.Fatal("want parse error")
	}
	if _, err := CompileSchema([]byte(`{"type": 12}`)); err == nil {
		t.Fatal("want compile error")
	}
}

func TestValidate(t *testing.T) {
	sch, err := CompileSchema([]byte(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(sch, jsontext.Value(`{"greeting":"yo","retries":1}`)); errs != nil {
		t.Fatalf("valid doc rejected: %v", errs)
	}
	errs := Validate(sch, jsontext.Value(`{"greeting":"this is far too long","retries":-1}`))
	paths := map[string]bool{}
	for _, e := range errs {
		if e.Message == "" {
			t.Errorf("empty message for %s", e.Path)
		}
		paths[e.Path] = true
	}
	if !paths["/greeting"] || !paths["/retries"] {
		t.Fatalf("want errors on /greeting and /retries, got %+v", errs)
	}
	if errs := Validate(sch, jsontext.Value(`{}`)); len(errs) == 0 {
		t.Fatal("missing required property accepted")
	}
	if errs := Validate(sch, jsontext.Value(`nope`)); len(errs) != 1 || errs[0].Path != "/" {
		t.Fatalf("invalid JSON: %+v", errs)
	}
}

func TestMergeDefaults(t *testing.T) {
	got := MergeDefaults([]byte(testSchema), jsontext.Value(`{"retries":9}`))
	var m map[string]any
	_ = jsonx.UnmarshalPinned(got, &m)
	if m["greeting"] != "hi" || m["retries"] != float64(9) {
		t.Fatalf("got %s", got)
	}
	got = MergeDefaults(nil, nil)
	if string(got) != `{}` {
		t.Fatalf("got %s", got)
	}
}

func TestMaskAndPreserveWriteOnly(t *testing.T) {
	schema := []byte(testSchema)
	masked := MaskWriteOnly(schema, jsontext.Value(`{"greeting":"a","api_key":"secret"}`))
	var m map[string]any
	_ = jsonx.UnmarshalPinned(masked, &m)
	if m["api_key"] != "" || m["greeting"] != "a" {
		t.Fatalf("masked = %s", masked)
	}

	old := jsontext.Value(`{"greeting":"a","api_key":"secret"}`)
	for _, in := range []string{`{"greeting":"b"}`, `{"greeting":"b","api_key":""}`} {
		out := PreserveWriteOnly(schema, old, jsontext.Value(in))
		_ = jsonx.UnmarshalPinned(out, &m)
		if m["api_key"] != "secret" || m["greeting"] != "b" {
			t.Fatalf("in=%s out=%s", in, out)
		}
	}
	out := PreserveWriteOnly(schema, old, jsontext.Value(`{"greeting":"b","api_key":"new"}`))
	_ = jsonx.UnmarshalPinned(out, &m)
	if m["api_key"] != "new" {
		t.Fatalf("explicit new value lost: %s", out)
	}
	// Nothing stored and nothing sent: stays absent (not an empty string).
	out = PreserveWriteOnly(schema, jsontext.Value(`{}`), jsontext.Value(`{"greeting":"b","api_key":""}`))
	m = nil
	_ = jsonx.UnmarshalPinned(out, &m)
	if _, ok := m["api_key"]; ok {
		t.Fatalf("empty api_key should be dropped: %s", out)
	}
}

func TestNullClearsAWriteOnlyValueAndWriteOnlySetReportsStored(t *testing.T) {
	schema := []byte(testSchema)
	old := jsontext.Value(`{"greeting":"a","api_key":"secret"}`)
	out := PreserveWriteOnly(schema, old, jsontext.Value(`{"greeting":"b","api_key":null}`))
	var m map[string]any
	_ = jsonx.UnmarshalPinned(out, &m)
	if _, ok := m["api_key"]; ok || m["greeting"] != "b" {
		t.Fatalf("null must clear the secret: %s", out)
	}
	if set := WriteOnlySet(schema, old); !set["api_key"] || set["greeting"] {
		t.Fatalf("WriteOnlySet = %v", set)
	}
	if set := WriteOnlySet(schema, jsontext.Value(`{"api_key":""}`)); len(set) != 0 {
		t.Fatalf("an empty value is not stored: %v", set)
	}
}
