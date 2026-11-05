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
