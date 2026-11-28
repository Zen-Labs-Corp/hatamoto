package wpimport

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

const acfBaseSchema = `{"type":"object","properties":{"content":{"type":"string","title":"Content"},"price":{"type":"number","title":"Cost"}}}`

func schemaWith(t *testing.T, props []propNode) jsontext.Value {
	t.Helper()
	merged, _, _, _, err := mergeProps(jsontext.Value(acfBaseSchema), props)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.ValidateSchemaShape(merged); err != nil {
		t.Fatalf("schema refused: %v\n%s", err, merged)
	}
	return merged
}

func TestACFSchemaNodeTable(t *testing.T) {
	cases := []struct {
		field ACFField
		kind  acfKind
		check func(n map[string]any) bool
	}{
		{ACFField{Type: "text"}, acfKindText, func(n map[string]any) bool { return n["type"] == "string" }},
		{ACFField{Type: "password"}, acfKindText, func(n map[string]any) bool { return n["type"] == "string" }},
		{ACFField{Type: "color_picker"}, acfKindText, func(n map[string]any) bool { return n["type"] == "string" }},
		{ACFField{Type: "textarea"}, acfKindText, func(n map[string]any) bool { return n["maxLength"] == 65535 }},
		{ACFField{Type: "wysiwyg"}, acfKindRichtext, func(n map[string]any) bool { return n["x-hm-field"] == "richtext" }},
		{ACFField{Type: "email"}, acfKindText, func(n map[string]any) bool { return n["format"] == "email" }},
		{ACFField{Type: "url"}, acfKindText, func(n map[string]any) bool { return n["format"] == "uri" }},
		{ACFField{Type: "oembed"}, acfKindText, func(n map[string]any) bool { return n["format"] == "uri" }},
		{ACFField{Type: "link"}, acfKindLink, func(n map[string]any) bool { return n["format"] == "uri" }},
		{ACFField{Type: "number"}, acfKindNumber, func(n map[string]any) bool { return n["type"] == "number" }},
		{ACFField{Type: "range"}, acfKindNumber, func(n map[string]any) bool { return n["type"] == "number" }},
		{ACFField{Type: "true_false"}, acfKindBool, func(n map[string]any) bool { return n["type"] == "boolean" }},
		{ACFField{Type: "select", Choices: []ACFChoice{{"small", "Small"}, {"large", "Large"}}}, acfKindChoice,
			func(n map[string]any) bool { e, _ := n["enum"].([]string); return len(e) == 2 && e[0] == "small" }},
		{ACFField{Type: "radio"}, acfKindChoice, func(n map[string]any) bool { _, has := n["enum"]; return n["type"] == "string" && !has }},
		{ACFField{Type: "button_group", Choices: []ACFChoice{{"a", "A"}}}, acfKindChoice, func(n map[string]any) bool { return n["type"] == "string" }},
		{ACFField{Type: "select", Multiple: true}, acfKindMulti, func(n map[string]any) bool { return n["type"] == "array" }},
		{ACFField{Type: "checkbox"}, acfKindMulti, func(n map[string]any) bool { return n["type"] == "array" }},
		{ACFField{Type: "date_picker"}, acfKindDate, func(n map[string]any) bool { return n["format"] == "date" }},
		{ACFField{Type: "date_time_picker"}, acfKindDateTime, func(n map[string]any) bool { return n["format"] == "date-time" }},
		{ACFField{Type: "time_picker"}, acfKindTime, func(n map[string]any) bool { return n["type"] == "string" && n["format"] == nil }},
		{ACFField{Type: "image"}, acfKindMedia, func(n map[string]any) bool { return n["x-media"] == true && n["format"] == "uri" }},
		{ACFField{Type: "file"}, acfKindMedia, func(n map[string]any) bool { return n["x-media"] == true }},
		{ACFField{Type: "gallery"}, acfKindGallery, func(n map[string]any) bool {
			it, _ := n["items"].(map[string]any)
			return n["type"] == "array" && it["x-media"] == true
		}},
		{ACFField{Type: "relationship"}, acfKindRelation, func(n map[string]any) bool { return n["type"] == "array" && n["x-multiple"] == true }},
		{ACFField{Type: "post_object"}, acfKindRelation, func(n map[string]any) bool { return n["x-multiple"] == false }},
		{ACFField{Type: "post_object", Multiple: true}, acfKindRelation, func(n map[string]any) bool { return n["x-multiple"] == true }},
		{ACFField{Type: "page_link"}, acfKindRelation, func(n map[string]any) bool { return n["type"] == "array" }},
	}
	for _, c := range cases {
		c.field.Label = "L"
		n, kind, lossy, warn := acfNode(c.field, false)
		if n == nil || kind != c.kind || lossy || warn != "" || n["title"] != "L" || !c.check(n) {
			t.Errorf("%s: node=%v kind=%q lossy=%v warn=%q", c.field.Type, n, kind, lossy, warn)
			continue
		}
		schemaWith(t, []propNode{{"field", n}})
	}
	for _, typ := range []string{"message", "tab", "accordion"} {
		if n, kind, _, _ := acfNode(ACFField{Type: typ}, false); n != nil || kind != acfKindNone {
			t.Errorf("%s gave a property", typ)
		}
	}
}

func TestACFSchemaUnknownTypeIsText(t *testing.T) {
	n, kind, _, warn := acfNode(ACFField{Type: "google_map", Label: "Map"}, false)
	if n["type"] != "string" || kind != acfKindText || !strings.Contains(warn, `"google_map"`) {
		t.Errorf("node=%v kind=%q warn=%q", n, kind, warn)
	}
}

func TestACFSchemaRepeaterAndGroup(t *testing.T) {
	staff := ACFField{Type: "repeater", Label: "Staff", Sub: []ACFField{
		{Type: "text", Name: "name", Label: "Name"}, {Type: "image", Name: "Photo-URL", Label: "Photo"}, {Type: "text", Name: "name", Label: "Again"}}}
	n, kind, lossy, _ := acfNode(staff, false)
	if kind != acfKindRepeater || lossy || n["x-hm-item-label"] != "Staff" {
		t.Fatalf("repeater = %v kind=%q lossy=%v", n, kind, lossy)
	}
	raw := schemaWith(t, []propNode{{"staff", n}})
	got := string(raw)
	if i, j := strings.Index(got, `"name"`), strings.Index(got, `"photo_url"`); i < 0 || j < i || !strings.Contains(got, `"name_2"`) {
		t.Errorf("row properties lost order or names: %s", got)
	}
	g, kind, _, _ := acfNode(ACFField{Type: "group", Label: "Box", Sub: []ACFField{{Type: "text", Name: "a", Label: "A"}}}, false)
	if kind != acfKindGroup || g["type"] != "object" {
		t.Errorf("group = %v", g)
	}
	schemaWith(t, []propNode{{"box", g}})
}

func TestACFSchemaNestedIsLossyText(t *testing.T) {
	inner := ACFField{Type: "repeater", Name: "items", Label: "Items", Sub: []ACFField{{Type: "text", Name: "x"}}}
	n, kind, lossy, _ := acfNode(inner, true)
	if kind != acfKindRowText || !lossy || n["type"] != "string" {
		t.Errorf("nested in a row = %v kind=%q lossy=%v", n, kind, lossy)
	}
	outer := ACFField{Type: "repeater", Label: "Outer", Sub: []ACFField{inner}}
	n, kind, lossy, _ = acfNode(outer, false)
	if kind != acfKindRepeater || !lossy {
		t.Errorf("outer lossy = %v", lossy)
	}
	schemaWith(t, []propNode{{"outer", n}})
}

func TestACFSchemaFlexible(t *testing.T) {
	f := ACFField{Type: "flexible_content", Label: "Sections", Layouts: []ACFLayout{
		{Key: "k1", Name: "hero", Sub: []ACFField{{Type: "text", Name: "title"}, {Type: "image", Name: "media"}}},
		{Key: "k2", Name: "text_block", Sub: []ACFField{{Type: "wysiwyg", Name: "title"}, {Type: "image", Name: "media"}}},
		{Key: "k3", Name: "columns", Sub: []ACFField{{Type: "flexible_content", Name: "cols", Layouts: []ACFLayout{{Name: "c"}}}}},
	}}
	n, kind, lossy, _ := acfNode(f, false)
	if kind != acfKindFlex || !lossy {
		t.Fatalf("kind=%q lossy=%v", kind, lossy)
	}
	names := layoutNames(f.Layouts)
	if names[0][0] != "title" || names[1][0] != "text_block_title" || names[1][1] != "media" || names[2][0] != "cols" {
		t.Errorf("names = %v", names)
	}
	raw := schemaWith(t, []propNode{{"sections", n}})
	var doc struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := jsonx.Unmarshal(raw, &doc, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	row := doc.Properties["sections"].Items.Properties
	enum, _ := row["layout"]["enum"].([]any)
	if len(enum) != 3 || enum[0] != "hero" {
		t.Errorf("layout enum = %v", row["layout"])
	}
	if row["cols"]["type"] != "string" || row["text_block_title"]["x-hm-field"] != "richtext" {
		t.Errorf("row = %v", row)
	}
}

func TestACFSchemaMergeProps(t *testing.T) {
	text := map[string]any{"type": "string", "title": "Phone"}
	merged, added, existing, clashes, err := mergeProps(jsontext.Value(acfBaseSchema), []propNode{
		{"phone", text},
		{"content", map[string]any{"type": "string", "title": "Another title", "maxLength": 10}}, // same shape, decoration differs
		{"price", map[string]any{"type": "string"}},                                              // retype
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "phone" || len(existing) != 1 || existing[0] != "content" || len(clashes) != 1 || clashes[0] != "price" {
		t.Errorf("added=%v existing=%v clashes=%v", added, existing, clashes)
	}
	entries, _ := jsonx.ObjectEntries(merged)
	var props []jsonx.Entry
	for _, e := range entries {
		if e.Key == "properties" {
			props, _ = jsonx.ObjectEntries(e.Value)
		}
	}
	var keys []string
	for _, p := range props {
		keys = append(keys, p.Key)
	}
	if strings.Join(keys, ",") != "content,price,phone" {
		t.Errorf("keys = %v", keys)
	}
	if !strings.Contains(string(merged), `"price":{"type":"number","title":"Cost"}`) {
		t.Errorf("an existing property changed: %s", merged)
	}
	// A second merge of the same property changes nothing.
	again, added2, existing2, _, _ := mergeProps(merged, []propNode{{"phone", text}})
	if len(added2) != 0 || len(existing2) != 1 || string(again) != string(merged) {
		t.Errorf("not idempotent: added=%v existing=%v", added2, existing2)
	}
	if _, _, _, _, err := mergeProps(jsontext.Value(`[1]`), nil); err == nil {
		t.Error("a non-object schema was accepted")
	}
}

func TestACFSchemaPropName(t *testing.T) {
	taken := map[string]bool{}
	for in, want := range map[string]string{"Opening-Date": "opening_date", "1st": "f_1st", "content": "acf_content", "title": "acf_title",
		"constructor": "acf_constructor", "": "field", "price": "price"} {
		if got := propName(in, taken); got != want {
			t.Errorf("propName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := propName("price", taken); got != "price_2" {
		t.Errorf("duplicate = %q", got)
	}
	if got := propName("Price", taken); got != "price_3" {
		t.Errorf("duplicate = %q", got)
	}
	if got := rowName("title", map[string]bool{}); got != "title" {
		t.Errorf("rowName(title) = %q", got)
	}
	if got := propName(strings.Repeat("a", 100), map[string]bool{}); len(got) != 64 {
		t.Errorf("long name = %d bytes", len(got))
	}
}

func TestACFSchemaFixtureGroupsAreValid(t *testing.T) {
	for _, x := range []string{acfDirectoryWXR, acfFlexibleWXR} {
		d := parseFixtureWXR(t, x)
		taken := map[string]bool{}
		var props []propNode
		for _, g := range d.ACF {
			for _, f := range g.Fields {
				n, _, _, _ := acfNode(f, false)
				if n != nil {
					props = append(props, propNode{propName(f.Name, taken), n})
				}
			}
		}
		if len(props) == 0 {
			t.Fatal("no properties")
		}
		schemaWith(t, props)
	}
}
