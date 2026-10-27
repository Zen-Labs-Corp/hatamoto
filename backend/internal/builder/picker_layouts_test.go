package builder

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// The "+" picker (web/admin/js/builder/picker.js) starts a section from these layouts. The node
// test pins testdata/picker_layouts.json to the JS; this one proves every layout is a valid doc,
// so an insert never fails at save.
func TestPickerLayoutsValidate(t *testing.T) {
	raw, err := os.ReadFile("testdata/picker_layouts.json")
	if err != nil {
		t.Fatal(err)
	}
	var layouts []struct {
		ID   string `json:"id"`
		Node *Node  `json:"node"`
	}
	if err := json.Unmarshal(raw, &layouts); err != nil {
		t.Fatal(err)
	}
	if len(layouts) != 7 {
		t.Fatalf("layouts = %d, want 7", len(layouts))
	}
	for _, l := range layouts {
		n := 0
		var name func(*Node)
		name = func(x *Node) {
			n++
			x.ID = "n" + strconv.Itoa(n)
			for _, c := range x.C {
				name(c)
			}
		}
		name(l.Node)
		doc := &Doc{V: 2, Nodes: []*Node{l.Node}}
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(out); err != nil {
			t.Errorf("layout %s: Parse: %v", l.ID, err)
		}
		if err := doc.Validate(); err != nil {
			t.Errorf("layout %s: Validate: %v", l.ID, err)
		}
	}
}
