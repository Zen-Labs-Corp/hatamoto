package schemaform

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestWithoutPropertiesKeepsOrderAndRequired(t *testing.T) {
	in := json.RawMessage(`{"type":"object","title":"T","properties":{"z":{"type":"string"},"meta_title":{"type":"string"},"a":{"type":"string"}},"required":["meta_title","a"]}`)
	out, err := WithoutProperties(in, "meta_title", "og_image")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != `{"type":"object","title":"T","properties":{"z":{"type":"string"},"a":{"type":"string"}},"required":["a"]}` {
		t.Errorf("got %s", got)
	}
	root, _ := ParseSchema(out)
	if len(root.Props) != 2 || root.Props[0].Name != "z" {
		t.Errorf("props %+v", root.Props)
	}
	// nothing left required: the key goes away
	out, _ = WithoutProperties(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`), "a")
	if strings.Contains(string(out), "required") {
		t.Errorf("empty required kept: %s", out)
	}
	// no properties at all is fine
	if _, err := WithoutProperties(json.RawMessage(`{"type":"object"}`), "a"); err != nil {
		t.Error(err)
	}
}

func TestXHmOrderSetsFieldOrder(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","x-hm-order":["c","a","gone"],"properties":{"a":{"type":"string"},"b":{"type":"string"},"c":{"type":"string"}}}`)
	root, err := ParseSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range root.Props {
		got = append(got, p.Name)
	}
	if strings.Join(got, ",") != "c,a,b" {
		t.Errorf("order %v, want c,a,b (listed first, unlisted after, unknown ignored)", got)
	}
	html, err := Render(schema, map[string]any{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !(strings.Index(s, `name="c"`) < strings.Index(s, `name="a"`) && strings.Index(s, `name="a"`) < strings.Index(s, `name="b"`)) {
		t.Error("render ignores x-hm-order")
	}
	out, errs := Decode(schema, url.Values{"a": {"1"}, "b": {"2"}, "c": {"3"}}, map[string]any{}, Options{})
	if len(errs) > 0 || out["c"] != "3" {
		t.Errorf("decode: %v %v", out, errs)
	}
}
