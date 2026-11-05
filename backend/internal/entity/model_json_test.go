package entity

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// An entity without an org byline keeps the key off the wire; v2 omitempty
// would write "byline_org":false.
func TestEntityJSONOmitsFalseBylineOrg(t *testing.T) {
	for name, v := range map[string]any{
		"entity": Entity{},
		"input":  CreateEntityInput{},
	} {
		b, err := jsonx.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "byline_org") {
			t.Errorf("%s: %s", name, b)
		}
	}
	b, _ := jsonx.Marshal(Entity{BylineOrg: true})
	if !strings.Contains(string(b), `"byline_org":true`) {
		t.Errorf("set value missing: %s", b)
	}
}

// v2 omitempty drops "{}" and "null" raw values; only absent ones may go.
func TestEntityJSONKeepsEmptyRawObjects(t *testing.T) {
	b, err := jsonx.Marshal(Entity{Metadata: []byte(`{}`), Editorial: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"metadata":{}`, `"editorial":{}`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("%s missing: %s", key, b)
		}
	}
	b, _ = jsonx.Marshal(Entity{})
	if strings.Contains(string(b), "metadata") {
		t.Errorf("nil metadata written: %s", b)
	}
}
