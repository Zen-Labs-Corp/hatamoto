package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func ip(i int) *int { return &i }

func mustDoc(t *testing.T, s string) *builder.Doc {
	t.Helper()
	d, err := builder.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// ids lists the node ids of ns depth first, with child lists in brackets.
func shape(ns []*builder.Node) string {
	var parts []string
	for _, n := range ns {
		s := n.ID
		if len(n.C) > 0 {
			s += "[" + shape(n.C) + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

const baseDoc = `{"v":2,"nodes":[
 {"id":"a","t":"heading","p":{"text":"A"}},
 {"id":"b","t":"text","p":{"text":"B"}},
 {"id":"c","t":"container","c":[{"id":"c1","t":"text","p":{"text":"1"}},{"id":"c2","t":"text","p":{"text":"2"}}]},
 {"id":"d","t":"text","p":{"text":"D"}}]}`

func TestApplyOpsCoreVerbs(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	ids, err := applyOps(doc, []opIn{
		{Op: "insert", Parent: "c", Node: object{"id": "c3", "t": "text", "p": object{"text": "3"}}},                                        // appended
		{Op: "insert", Parent: "", Index: ip(0), Node: object{"t": "container", "c": []any{object{"t": "text", "p": object{"text": "x"}}}}}, // ids generated
		{Op: "move", ID: "d", Parent: "c", Index: ip(0)},
		{Op: "setProp", ID: "a", Key: "text", Value: "Hello"},
		{Op: "setStyle", ID: "a", BP: "base", Key: "ta", Value: "center"},
		{Op: "remove", ID: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].Op != 1 || ids[0].Path != "node" || ids[1].Path != "node.c.0" || ids[0].ID == ids[1].ID || ids[0].ID == "" {
		t.Fatalf("generated ids: %+v", ids)
	}
	want := ids[0].ID + "[" + ids[1].ID + "],a,c[d,c1,c2,c3]"
	if got := shape(doc.Nodes); got != want {
		t.Errorf("shape %s, want %s", got, want)
	}
	a, _ := doc.Find("a")
	if a.P["text"] != "Hello" || a.S["base"]["ta"] != "center" {
		t.Errorf("a = %+v", a)
	}
}

func TestApplyOpsSetBind(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	if _, err := applyOps(doc, []opIn{{Op: "setBind", ID: "a", Key: "text", Value: "entity.title"}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := doc.Find("a"); n.B["text"] != "entity.title" {
		t.Errorf("bind: %+v", n.B)
	}
}

func TestApplyOpsWrap(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	ids, err := applyOps(doc, []opIn{{Op: "wrap", IDs: []string{"b", "c"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].Path != "node" {
		t.Fatalf("ids %+v", ids)
	}
	if want := "a," + ids[0].ID + "[b,c[c1,c2]],d"; shape(doc.Nodes) != want {
		t.Errorf("shape %s, want %s", shape(doc.Nodes), want)
	}
	// An id given by the caller is kept; ids may come in any order.
	doc = mustDoc(t, baseDoc)
	if _, err := applyOps(doc, []opIn{{Op: "wrap", IDs: []string{"d", "c"}, Node: object{"id": "w", "t": "container", "p": object{"tag": "section"}}}}); err != nil {
		t.Fatal(err)
	}
	if want := "a,b,w[c[c1,c2],d]"; shape(doc.Nodes) != want {
		t.Errorf("shape %s, want %s", shape(doc.Nodes), want)
	}
}

func TestApplyOpsWrapRefusals(t *testing.T) {
	for name, in := range map[string]opIn{
		"gap":          {Op: "wrap", IDs: []string{"a", "c"}},
		"two parents":  {Op: "wrap", IDs: []string{"b", "c1"}},
		"unknown":      {Op: "wrap", IDs: []string{"zz"}},
		"none":         {Op: "wrap"},
		"wrapper kids": {Op: "wrap", IDs: []string{"a"}, Node: object{"t": "container", "c": []any{object{"t": "text", "p": object{"text": "x"}}}}},
	} {
		if _, err := applyOps(mustDoc(t, baseDoc), []opIn{in}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	ref := mustDoc(t, `{"v":2,"nodes":[{"id":"r","t":"ref","p":{"block":"00000000-0000-0000-0000-000000000001"}}]}`)
	if _, err := applyOps(ref, []opIn{{Op: "wrap", IDs: []string{"r"}}}); err == nil || !strings.Contains(err.Error(), "cannot be wrapped") {
		t.Errorf("ref: %v", err)
	}
}

func TestApplyOpsUnwrap(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	if _, err := applyOps(doc, []opIn{{Op: "unwrap", ID: "c"}}); err != nil {
		t.Fatal(err)
	}
	if want := "a,b,c1,c2,d"; shape(doc.Nodes) != want {
		t.Errorf("shape %s, want %s", shape(doc.Nodes), want)
	}
	for name, id := range map[string]string{"not a container": "a", "no children": ""} {
		d := mustDoc(t, baseDoc)
		if id == "" {
			d = mustDoc(t, `{"v":2,"nodes":[{"id":"e","t":"container"}]}`)
			id = "e"
		}
		if _, err := applyOps(d, []opIn{{Op: "unwrap", ID: id}}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestApplyOpsDuplicate(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	ids, err := applyOps(doc, []opIn{{Op: "duplicate", ID: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0].Path != "node" || ids[1].Path != "node.c.0" || ids[2].Path != "node.c.1" {
		t.Fatalf("ids %+v", ids)
	}
	want := "a,b,c[c1,c2]," + ids[0].ID + "[" + ids[1].ID + "," + ids[2].ID + "],d"
	if shape(doc.Nodes) != want {
		t.Errorf("shape %s, want %s", shape(doc.Nodes), want)
	}
	cp, _ := doc.Find(ids[1].ID)
	if cp == nil || cp.P["text"] != "1" {
		t.Errorf("copy lost its props: %+v", cp)
	}
}

func TestApplyOpsErrors(t *testing.T) {
	for name, in := range map[string]opIn{
		"unknown verb":    {Op: "explode"},
		"remove no id":    {Op: "remove"},
		"remove unknown":  {Op: "remove", ID: "zz"},
		"insert no node":  {Op: "insert"},
		"insert bad kind": {Op: "insert", Node: object{"t": "nope"}},
		"unknown field":   {Op: "insert", Node: object{"t": "text", "bogus": 1}},
		"setProp no key":  {Op: "setProp", ID: "a"},
		"setStyle no bp":  {Op: "setStyle", ID: "a", Key: "ta", Value: "center"},
		"bad style":       {Op: "setStyle", ID: "a", BP: "base", Key: "ta", Value: "}"},
		"index range":     {Op: "insert", Index: ip(99), Node: object{"t": "text", "p": object{"text": "x"}}},
		"move into self":  {Op: "move", ID: "c", Parent: "c1"},
	} {
		if _, err := applyOps(mustDoc(t, baseDoc), []opIn{in}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// A failing op reports its position; the caller's copy is the only thing touched.
func TestApplyOpsErrorNamesTheOp(t *testing.T) {
	_, err := applyOps(mustDoc(t, baseDoc), []opIn{{Op: "remove", ID: "b"}, {Op: "remove", ID: "zz"}})
	if err == nil || !strings.HasPrefix(err.Error(), "op 1 (remove)") {
		t.Fatalf("err %v", err)
	}
}

func TestCheckOpLimits(t *testing.T) {
	if err := checkOpLimits(nil); err == nil {
		t.Error("empty ops accepted")
	}
	many := make([]opIn, maxOps+1)
	if err := checkOpLimits(many); err == nil || !strings.HasPrefix(err.Error(), "limit:") {
		t.Errorf("501 ops: %v", err)
	}
	if err := checkOpLimits(many[:maxOps]); err != nil {
		t.Errorf("500 ops: %v", err)
	}
	big := []opIn{{Op: "setProp", ID: "a", Key: "text", Value: strings.Repeat("x", maxOpsBytes)}}
	if err := checkOpLimits(big); err == nil || !strings.HasPrefix(err.Error(), "limit:") {
		t.Errorf("1 MiB ops: %v", err)
	}
}

func TestOpInDecodes(t *testing.T) {
	var ops []opIn
	if err := json.Unmarshal([]byte(`[{"op":"insert","parent":"c","index":0,"node":{"t":"text"}},{"op":"setProp","id":"a","key":"n","value":3}]`), &ops); err != nil {
		t.Fatal(err)
	}
	if *ops[0].Index != 0 || ops[1].Value != float64(3) {
		t.Errorf("%+v", ops)
	}
}

func TestApplyOpsMoveWithoutIndexAppends(t *testing.T) {
	doc := mustDoc(t, baseDoc)
	if _, err := applyOps(doc, []opIn{{Op: "move", ID: "c1", Parent: "c"}}); err != nil {
		t.Fatalf("same parent: %v", err)
	}
	if want := "a,b,c[c2,c1],d"; shape(doc.Nodes) != want {
		t.Errorf("same parent: shape %s, want %s", shape(doc.Nodes), want)
	}
	if _, err := applyOps(doc, []opIn{{Op: "move", ID: "a"}}); err != nil {
		t.Fatalf("top level: %v", err)
	}
	if want := "b,c[c2,c1],d,a"; shape(doc.Nodes) != want {
		t.Errorf("top level: shape %s, want %s", shape(doc.Nodes), want)
	}
	if _, err := applyOps(doc, []opIn{{Op: "move", ID: "b", Parent: "c"}, {Op: "move", ID: "c1"}}); err != nil {
		t.Fatalf("other parent: %v", err)
	}
	if want := "c[c2,b],d,a,c1"; shape(doc.Nodes) != want {
		t.Errorf("other parent: shape %s, want %s", shape(doc.Nodes), want)
	}
}

// A converted v1 page: sections are container(tag=section) > one component
// (the editor's moveSubject and topV1, clipops.js and screen.js).
const v1Doc = `{"v":2,"nodes":[
 {"id":"s1","t":"container","p":{"tag":"section"},"c":[{"id":"k1","t":"component","p":{"type":"hero"}}]},
 {"id":"s2","t":"container","p":{"tag":"section"},"c":[{"id":"k2","t":"component","p":{"type":"hero"}}]},
 {"id":"x","t":"container","c":[{"id":"k3","t":"component","p":{"type":"hero"}}]},
 {"id":"t","t":"text","p":{"text":"T"}}]}`

func TestApplyOpsLegacySections(t *testing.T) {
	doc := mustDoc(t, v1Doc)
	ids, err := applyOps(doc, []opIn{{Op: "duplicate", ID: "k1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids %+v", ids)
	}
	want := "s1[k1]," + ids[0].ID + "[" + ids[1].ID + "],s2[k2],x[k3],t"
	if shape(doc.Nodes) != want {
		t.Errorf("duplicate component: shape %s, want %s", shape(doc.Nodes), want)
	}
	for name, in := range map[string]opIn{
		"wrap section":          {Op: "wrap", IDs: []string{"s1"}},
		"wrap section sibling":  {Op: "wrap", IDs: []string{"s1", "s2"}},
		"unwrap section":        {Op: "unwrap", ID: "s2"},
		"unwrap by component":   {Op: "unwrap", ID: "k2"},
		"unwrap bare component": {Op: "unwrap", ID: "x"},
	} {
		d := mustDoc(t, v1Doc)
		if _, err := applyOps(d, []opIn{in}); err == nil {
			t.Errorf("%s: want an error", name)
		} else if shape(d.Nodes) != "s1[k1],s2[k2],x[k3],t" {
			t.Errorf("%s: doc changed: %s", name, shape(d.Nodes))
		}
	}
	d := mustDoc(t, v1Doc)
	if _, err := applyOps(d, []opIn{{Op: "wrap", IDs: []string{"x", "t"}}}); err != nil {
		t.Errorf("wrap of plain top-level nodes: %v", err)
	}
}
