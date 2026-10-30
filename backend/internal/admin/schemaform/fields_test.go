package schemaform

import (
	"encoding/json"
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
	got := TopFields(json.RawMessage(schema))
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

func TestTopFieldsNone(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`not json`, `{"type":"object"}`, ``} {
		if got := TopFields(json.RawMessage(raw)); got != nil {
			t.Errorf("TopFields(%q) = %v, want nil", raw, got)
		}
	}
}
