package schemaform

import (
	"encoding/json"
	"strings"
	"testing"
)

func renderGroups(t *testing.T, schema string) string {
	t.Helper()
	out, err := Render(json.RawMessage(schema), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGroupHeadingsRenderOncePerGroupInOrder(t *testing.T) {
	html := renderGroups(t, `{"type":"object","x-hm-order":["a","b","c","d","e"],"properties":{
		"a":{"type":"string","x-hm-group":"Profile"},
		"b":{"type":"string","x-hm-group":"Links"},
		"c":{"type":"string","x-hm-group":"Profile"},
		"d":{"type":"string"},
		"e":{"type":"string","x-hm-group":"Links"}}}`)
	if n := strings.Count(html, `<h3 class="hm-sf-group">`); n != 2 {
		t.Fatalf("headings = %d, want 2:\n%s", n, html)
	}
	idx := func(s string) int { return strings.Index(html, s) }
	d, prof, links := idx(`id="sf-d"`), idx(">Profile</h3>"), idx(">Links</h3>")
	if !(d >= 0 && d < prof && prof < links) {
		t.Errorf("order wrong: ungrouped %d, Profile %d, Links %d", d, prof, links)
	}
	// a and c sit together under Profile, b and e under Links.
	if !(idx(`id="sf-a"`) > prof && idx(`id="sf-c"`) > idx(`id="sf-a"`) && idx(`id="sf-c"`) < links) {
		t.Errorf("Profile members not contiguous")
	}
	if !(idx(`id="sf-b"`) > links && idx(`id="sf-e"`) > idx(`id="sf-b"`)) {
		t.Errorf("Links members not contiguous")
	}
}

func TestUngroupedSchemaHasNoHeading(t *testing.T) {
	html := renderGroups(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string","x-hm-group":"  "}}}`)
	if strings.Contains(html, "hm-sf-group") {
		t.Errorf("unexpected heading:\n%s", html)
	}
}

func TestUnknownGroupKeyIgnored(t *testing.T) {
	html := renderGroups(t, `{"type":"object","properties":{"a":{"type":"string","x-hm-groupe":"Oops"}}}`)
	if strings.Contains(html, "Oops") || strings.Contains(html, "hm-sf-group") {
		t.Errorf("unknown key rendered:\n%s", html)
	}
}
