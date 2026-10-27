package builder

import (
	"fmt"
	"strings"
	"testing"
)

const uuidA = "11111111-2222-3333-4444-555555555555"

func refDoc(t *testing.T, raw string) (*Doc, error) {
	t.Helper()
	return Parse([]byte(raw))
}

func TestRefKind(t *testing.T) {
	ok := `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"` + uuidA + `"},"s":{"base":{"mt":"space-4"}}},{"id":"s1","t":"slot"}]}`
	d, err := refDoc(t, ok)
	if err != nil {
		t.Fatal(err)
	}
	if V1Expressible(d) {
		t.Error("a doc with a ref must not be v1-expressible")
	}
	for name, raw := range map[string]string{
		"bad uuid":    `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"Bad"}}]}`,
		"upper uuid":  `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"` + "AAAAAAAA-2222-3333-4444-555555555555" + `"}}]}`,
		"no block":    `{"v":2,"nodes":[{"id":"r1","t":"ref"}]}`,
		"children":    `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"` + uuidA + `"},"c":[{"id":"x","t":"text"}]}]}`,
		"extra prop":  `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":"` + uuidA + `","x":1}}]}`,
		"slot prop":   `{"v":2,"nodes":[{"id":"s1","t":"slot","p":{"x":1}}]}`,
		"slot child":  `{"v":2,"nodes":[{"id":"s1","t":"slot","c":[{"id":"x","t":"text"}]}]}`,
		"empty block": `{"v":2,"nodes":[{"id":"r1","t":"ref","p":{"block":""}}]}`,
	} {
		if _, err := refDoc(t, raw); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRefIDsAndSlotCount(t *testing.T) {
	d, err := refDoc(t, `{"v":2,"nodes":[{"id":"c","t":"container","c":[{"id":"r1","t":"ref","p":{"block":"`+uuidA+`"}},{"id":"s","t":"slot"}]},{"id":"r2","t":"ref","p":{"block":"`+uuidA+`"}},{"id":"r3","t":"ref","p":{"block":"aaaaaaaa-2222-3333-4444-555555555555"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := RefIDs(d); len(got) != 2 || got[0] != uuidA || got[1][:2] != "aa" {
		t.Errorf("RefIDs = %v", got)
	}
	if SlotCount(d) != 1 || SlotCount(nil) != 0 || RefIDs(nil) != nil {
		t.Error("SlotCount/RefIDs nil handling")
	}
}

func sample() []*Node {
	return []*Node{{ID: "a", T: "container", P: map[string]any{"tag": "div"}, S: map[string]map[string]string{"base": {"mt": "space-4"}},
		C: []*Node{{ID: "b", T: "text", P: map[string]any{"text": "x"}}}}, {ID: "c", T: "divider"}}
}

func TestPrefixIDs(t *testing.T) {
	in := sample()
	out := PrefixIDs(in, "bx_")
	if out[0].ID != "bx_a" || out[0].C[0].ID != "bx_b" || out[1].ID != "bx_c" {
		t.Fatalf("ids = %s %s %s", out[0].ID, out[0].C[0].ID, out[1].ID)
	}
	if in[0].ID != "a" || in[0].C[0].ID != "b" {
		t.Error("input mutated")
	}
	out[0].P["tag"] = "section"
	out[0].S["base"]["mt"] = "9"
	if in[0].P["tag"] != "div" || in[0].S["base"]["mt"] != "space-4" {
		t.Error("copy shares maps with the input")
	}
	long := []*Node{{ID: strings.Repeat("x", 64), T: "divider"}, {ID: strings.Repeat("x", 63) + "y", T: "divider"}}
	p1, p2 := PrefixIDs(long, "bdeadbeef_"), PrefixIDs(long, "bdeadbeef_")
	for i, n := range p1 {
		if !ValidID(n.ID) || n.ID != p2[i].ID || len(n.ID) > 64 {
			t.Errorf("long id %q", n.ID)
		}
	}
	if p1[0].ID == p1[1].ID {
		t.Error("distinct long ids collide")
	}
	d := &Doc{V: 2, Nodes: p1}
	if err := d.Validate(); err != nil {
		t.Errorf("prefixed doc invalid: %v", err)
	}
}

func TestFreshIDs(t *testing.T) {
	n := 0
	gen := func() string { n++; return fmt.Sprintf("n%d", n/2) } // repeats every id once
	out := FreshIDs(sample(), gen)
	seen := map[string]bool{}
	walkNodes(out, func(x *Node) {
		if seen[x.ID] || !ValidID(x.ID) {
			t.Errorf("id %q repeated or invalid", x.ID)
		}
		seen[x.ID] = true
	})
	if len(seen) != 3 {
		t.Errorf("ids = %v", seen)
	}
}

func TestCheckRefs(t *testing.T) {
	ids := []string{"b0000000-0000-0000-0000-000000000000", "b1000000-0000-0000-0000-000000000000", "b2000000-0000-0000-0000-000000000000", "b3000000-0000-0000-0000-000000000000", "b4000000-0000-0000-0000-000000000000"}
	to := func(id string) *Doc {
		if id == "" {
			return &Doc{V: 2, Nodes: []*Node{{ID: "t", T: "text"}}}
		}
		return &Doc{V: 2, Nodes: []*Node{{ID: "r", T: "ref", P: map[string]any{"block": id}}}}
	}
	chain := func(links ...string) func(string) (*Doc, bool) {
		// block i refs links[i] ("" = no ref); unknown ids are missing
		return func(id string) (*Doc, bool) {
			for i, b := range ids {
				if b == id && i < len(links) {
					return to(links[i]), true
				}
			}
			return nil, false
		}
	}
	for _, c := range []struct {
		name, self string
		doc        *Doc
		lookup     func(string) (*Doc, bool)
		want       string
	}{
		{"self", ids[0], to(ids[0]), chain(), "cycle"},
		{"A->B->A", ids[0], to(ids[1]), chain("", ids[0]), "cycle"},
		{"page via loop", "page", to(ids[0]), chain(ids[1], ids[0]), "cycle"},
		{"block->B->C ok (3 blocks on the render stack)", ids[0], to(ids[1]), chain("", ids[2], ""), ""},
		{"block->B->C->D refused (render would stop at D)", ids[0], to(ids[1]), chain("", ids[2], ids[3], ""), "deeper"},
		{"missing target", "page", to(ids[0]), chain(), ""},
		{"no refs", "page", &Doc{V: 2}, chain(), ""},
	} {
		err := CheckRefs(c.self, c.doc, c.lookup)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if err != nil && !strings.Contains(c.name, "refused") && !strings.Contains(err.Error(), "->") {
			t.Errorf("%s: error should name the path: %v", c.name, err)
		}
	}
}

func TestCheckRefsDeepAfterShallow(t *testing.T) {
	// X is reachable shallow and deep; the deep path must still be refused.
	ids := []string{"b0000000-0000-0000-0000-000000000000", "b1000000-0000-0000-0000-000000000000", "b2000000-0000-0000-0000-000000000000", "b3000000-0000-0000-0000-000000000000"}
	ref := func(ids ...string) *Doc {
		d := &Doc{V: 2}
		for i, id := range ids {
			d.Nodes = append(d.Nodes, &Node{ID: fmt.Sprintf("r%d", i), T: "ref", P: map[string]any{"block": id}})
		}
		return d
	}
	docs := map[string]*Doc{ids[0]: ref(ids[3], ids[1]), ids[1]: ref(ids[2]), ids[2]: ref(ids[3]), ids[3]: ref("c0000000-0000-0000-0000-000000000000")}
	err := CheckRefs("page", ref(ids[0]), func(id string) (*Doc, bool) { d, ok := docs[id]; return d, ok })
	if err == nil || !strings.Contains(err.Error(), "deeper") {
		t.Fatalf("err = %v", err)
	}
}
