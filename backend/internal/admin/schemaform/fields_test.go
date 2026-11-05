package schemaform

import (
	"encoding/json/jsontext"
	"hatamoto.dev/core/internal/jsonx"
	"reflect"
	"testing"
)

func TestTopFields(t *testing.T) {
	t.Parallel()

	const schema = `{"type":"object","properties":{
	 "eyebrow":{"type":"string"},
	 "style":{"type":"string","x-hm-group":"Look"},
	 "headline":{"type":"string","title":"Main heading"},
	 "body":{"type":"string","x-hm-field":"richtext"},
	 "raw":{"type":"string","x-hm-field":"html"},
	 "photo":{"type":"string","x-hm-field":"image"},
	 "media":{"type":"string","format":"uri","x-media":true},
	 "cta_url":{"type":"string","x-hm-field":"url"},
	 "site":{"type":"string","format":"uri"},
	 "snippet":{"type":"string","x-hm-field":"code"},
	 "sheet":{"type":"string","x-hm-field":"css"},
	 "ctas":{"type":"array","items":{"type":"object"}},
	 "strip":{"type":"array","x-hm-field":"lines","items":{"type":"string"}},
	 "panel":{"type":"object","properties":{"title":{"type":"string"}}}}}`
	got := TopFields(jsontext.Value(schema))
	byKey := map[string]TopField{}
	var keys []string
	for _, f := range got {
		byKey[f.Key] = f
		keys = append(keys, f.Key)
	}
	// Ungrouped properties come first, the grouped one last.
	if len(keys) != 14 || keys[len(keys)-1] != "style" || keys[0] != "eyebrow" {
		t.Fatalf("order = %v, want eyebrow first and style (grouped) last", keys)
	}
	want := map[string][2]string{
		"eyebrow": {"Eyebrow", "text"}, "headline": {"Main heading", "text"}, "style": {"Style", "text"},
		"body": {"Body", "richtext"}, "raw": {"Raw", "richtext"}, "photo": {"Photo", "image"}, "media": {"Media", "image"},
		"cta_url": {"Cta url", "link"}, "site": {"Site", "link"}, "snippet": {"Snippet", "code"}, "sheet": {"Sheet", "code"},
		"ctas": {"Ctas", "list"}, "strip": {"Strip", "list"}, "panel": {"Panel", "object"},
	}
	for k, w := range want {
		if g := [2]string{byKey[k].Label, byKey[k].Kind}; !reflect.DeepEqual(g, w) {
			t.Errorf("%s = %v, want %v", k, g, w)
		}
	}
}

func TestTopFieldsAlias(t *testing.T) {
	t.Parallel()

	got := TopFields(jsontext.Value(`{"type":"object","properties":{"title":{"type":"string","x-hm-alias":["heading","h1"]},"sub":{"type":"string"}}}`))
	if len(got) != 2 || !reflect.DeepEqual(got[0].Alias, []string{"heading", "h1"}) || got[1].Alias != nil {
		t.Fatalf("aliases = %+v, want title [heading h1], sub none", got)
	}
	b, _ := jsonx.Marshal(got[1])
	if string(b) != `{"key":"sub","label":"Sub","kind":"text"}` {
		t.Errorf("no alias must be omitted: %s", b)
	}
}

func TestTopFieldsNone(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`not json`, `{"type":"object"}`, ``} {
		if got := TopFields(jsontext.Value(raw)); got != nil {
			t.Errorf("TopFields(%q) = %v, want nil", raw, got)
		}
	}
}

func TestTopFieldsSetting(t *testing.T) {
	t.Parallel()

	const schema = `{"type":"object","properties":{
	 "layout":{"type":"string","enum":["a","b"]},
	 "flag":{"type":"boolean"},
	 "count":{"type":"integer"},
	 "ratio":{"type":"number"},
	 "href":{"type":"string","x-hm-field":"url"},
	 "plain":{"type":"string","x-hm-field":"plaintext"},
	 "body":{"type":"string","x-hm-field":"richtext"},
	 "photo":{"type":"string","x-hm-field":"image"},
	 "items":{"type":"array","items":{"type":"object"}},
	 "panel":{"type":"object","properties":{"title":{"type":"string"}}},
	 "code":{"type":"string","x-hm-field":"code"},
	 "eyebrow":{"type":"string"}}}`
	want := map[string]bool{"layout": true, "flag": true, "count": true, "ratio": true, "href": true}
	for _, f := range TopFields(jsontext.Value(schema)) {
		if f.Setting != want[f.Key] {
			t.Errorf("%s Setting = %v, want %v", f.Key, f.Setting, want[f.Key])
		}
	}
}
