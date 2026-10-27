package builder

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"
)

func mustJSON(t testing.TB, d *Doc) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opsFixture(t testing.TB) *Doc {
	t.Helper()
	d, err := Parse([]byte(`{"v":2,"nodes":[
		{"id":"hero","t":"container","p":{"tag":"section","layout":"grid"},"s":{"base":{"cols":"2","pt":"space-4"}},"c":[
			{"id":"col1","t":"container","c":[
				{"id":"h1","t":"heading","p":{"text":"Hello","level":1}},
				{"id":"t1","t":"text","p":{"text":"Body"}}]},
			{"id":"col2","t":"container","c":[{"id":"img","t":"image","p":{"src":"/a.png","alt":"a"}}]}]},
		{"id":"c-k","t":"container","p":{"tag":"section","v1":true},"c":[
			{"id":"k","t":"component","p":{"type":"hero","title":"T","content":{"a":[1,2,{"b":"c"}]}}}]},
		{"id":"sp","t":"spacer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestApplyInverse(t *testing.T) {
	ops := []Op{
		{Op: "insert", Parent: "col1", Index: 1, Node: &Node{ID: "new", T: "text", P: map[string]any{"text": "n"}}},
		{Op: "insert", Index: 0, Node: &Node{ID: "top", T: "divider"}},
		{Op: "move", ID: "t1", Parent: "col2", Index: 0},
		{Op: "move", ID: "sp", Parent: "", Index: 0},
		{Op: "move", ID: "h1", Parent: "col1", Index: 1},
		{Op: "setProp", ID: "h1", Key: "text", Value: json.RawMessage(`"World"`)},
		{Op: "setProp", ID: "h1", Key: "level", Value: json.RawMessage(`null`)},
		{Op: "setProp", ID: "sp", Key: "hidden", Value: json.RawMessage(`true`)},
		{Op: "setProp", ID: "t1", Key: "text", Value: json.RawMessage(`null`)},
		{Op: "setStyle", ID: "hero", BP: "base", Key: "pt", Value: json.RawMessage(`null`)},
		{Op: "setStyle", ID: "hero", BP: "md", Key: "pt", Value: json.RawMessage(`"space-1"`)},
		{Op: "setStyle", ID: "hero", BP: "base", Key: "cols", Value: json.RawMessage(`null`)},
		{Op: "setStyle", ID: "hero", BP: "base", Key: "pt", Value: json.RawMessage(`null`)},
		{Op: "remove", ID: "col1"},
		{Op: "remove", ID: "sp"},
	}
	d := opsFixture(t)
	orig := mustJSON(t, d)
	var invs []Op
	for i, op := range ops {
		before := mustJSON(t, d)
		inv, err := d.Apply(op)
		if err != nil {
			// Ops referencing nodes removed earlier are allowed to fail, but must not change the doc.
			if !bytes.Equal(before, mustJSON(t, d)) {
				t.Fatalf("op %d failed (%v) but changed the doc", i, err)
			}
			continue
		}
		if err := d.Validate(); err != nil {
			t.Fatalf("op %d left an invalid doc: %v", i, err)
		}
		after := mustJSON(t, d)
		// Each inverse restores the state right before its op.
		back, err := d.Apply(inv)
		if err != nil {
			t.Fatalf("op %d inverse: %v", i, err)
		}
		if !bytes.Equal(before, mustJSON(t, d)) {
			t.Fatalf("op %d (%s): inverse did not restore\n got %s\nwant %s", i, op.Op, mustJSON(t, d), before)
		}
		if _, err := d.Apply(back); err != nil || !bytes.Equal(after, mustJSON(t, d)) {
			t.Fatalf("op %d: redo mismatch (%v)", i, err)
		}
		invs = append(invs, inv)
	}
	if len(invs) < 10 {
		t.Fatalf("only %d ops applied", len(invs))
	}
	for i := len(invs) - 1; i >= 0; i-- {
		if _, err := d.Apply(invs[i]); err != nil {
			t.Fatalf("undo %d: %v", i, err)
		}
	}
	if !bytes.Equal(orig, mustJSON(t, d)) {
		t.Fatal("undo-all did not restore the doc")
	}
}

func TestApplyErrorsLeaveDocUnchanged(t *testing.T) {
	bad := map[string]Op{
		"unknown verb":         {Op: "explode"},
		"insert nil":           {Op: "insert"},
		"insert bad index":     {Op: "insert", Index: 99, Node: &Node{ID: "z", T: "spacer"}},
		"insert neg index":     {Op: "insert", Index: -1, Node: &Node{ID: "z", T: "spacer"}},
		"insert dup id":        {Op: "insert", Node: &Node{ID: "h1", T: "spacer"}},
		"insert bad kind":      {Op: "insert", Node: &Node{ID: "z", T: "marquee"}},
		"insert bad props":     {Op: "insert", Node: &Node{ID: "z", T: "heading", P: map[string]any{"level": 9}}},
		"insert bad style":     {Op: "insert", Node: &Node{ID: "z", T: "spacer", S: map[string]map[string]string{"base": {"bg": "red;}"}}}},
		"insert under widget":  {Op: "insert", Parent: "h1", Node: &Node{ID: "z", T: "spacer"}},
		"insert missing par":   {Op: "insert", Parent: "nope", Node: &Node{ID: "z", T: "spacer"}},
		"remove missing":       {Op: "remove", ID: "nope"},
		"move missing":         {Op: "move", ID: "nope"},
		"move into self":       {Op: "move", ID: "hero", Parent: "col1"},
		"move into own id":     {Op: "move", ID: "hero", Parent: "hero"},
		"move bad index":       {Op: "move", ID: "t1", Parent: "col2", Index: 50},
		"move to widget":       {Op: "move", ID: "t1", Parent: "h1"},
		"setProp missing":      {Op: "setProp", ID: "nope", Key: "text", Value: json.RawMessage(`"x"`)},
		"setProp bad json":     {Op: "setProp", ID: "t1", Key: "text", Value: json.RawMessage(`{`)},
		"setProp empty value":  {Op: "setProp", ID: "t1", Key: "text"},
		"setProp unknown key":  {Op: "setProp", ID: "t1", Key: "onclick", Value: json.RawMessage(`"x"`)},
		"setProp wrong type":   {Op: "setProp", ID: "h1", Key: "level", Value: json.RawMessage(`"1"`)},
		"setProp comp no type": {Op: "setProp", ID: "k", Key: "type", Value: json.RawMessage(`null`)},
		"setStyle missing":     {Op: "setStyle", ID: "nope", BP: "base", Key: "pt", Value: json.RawMessage(`"space-1"`)},
		"setStyle bad bp":      {Op: "setStyle", ID: "h1", BP: "xl", Key: "pt", Value: json.RawMessage(`"space-1"`)},
		"setStyle bad key":     {Op: "setStyle", ID: "h1", BP: "base", Key: "zzz", Value: json.RawMessage(`"space-1"`)},
		"setStyle bad value":   {Op: "setStyle", ID: "h1", BP: "base", Key: "bg", Value: json.RawMessage(`"red;}"`)},
		"setStyle not string":  {Op: "setStyle", ID: "h1", BP: "base", Key: "pt", Value: json.RawMessage(`5`)},
	}
	for name, op := range bad {
		d := opsFixture(t)
		before := mustJSON(t, d)
		if _, err := d.Apply(op); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if !bytes.Equal(before, mustJSON(t, d)) {
			t.Errorf("%s: doc changed on error", name)
		}
	}
}

func TestApplyDepthLimitRollsBack(t *testing.T) {
	d := &Doc{V: 2, Nodes: []*Node{{ID: "c0", T: "container"}}}
	parent := "c0"
	for i := 1; i < MaxDepth; i++ {
		id := "c" + strconv.Itoa(i)
		if _, err := d.Apply(Op{Op: "insert", Parent: parent, Node: &Node{ID: id, T: "container"}}); err != nil {
			t.Fatal(err)
		}
		parent = id
	}
	before := mustJSON(t, d)
	_, err := d.Apply(Op{Op: "insert", Parent: parent, Node: &Node{ID: "toodeep", T: "spacer"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if !bytes.Equal(before, mustJSON(t, d)) {
		t.Fatal("doc changed")
	}
}

func TestApplyDoesNotAliasInsertedNode(t *testing.T) {
	d := opsFixture(t)
	n := &Node{ID: "z", T: "text", P: map[string]any{"text": "a"}}
	if _, err := d.Apply(Op{Op: "insert", Node: n}); err != nil {
		t.Fatal(err)
	}
	n.P["text"] = "mutated"
	got, _ := d.Find("z")
	if got.P["text"] != "a" {
		t.Fatal("inserted node aliases caller memory")
	}
}

// Random op sequences: every state validates, errors never change the doc, and
// undoing everything restores the doc byte for byte.
func TestOpsUndoFuzz(t *testing.T) {
	d := opsFixture(t)
	orig := mustJSON(t, d)
	rng := rand.New(rand.NewPCG(1, 2))
	next := 0
	for round := 0; round < 200; round++ {
		var invs []Op
		for i := 0; i < 30; i++ {
			op := randomOp(rng, d, &next)
			before := mustJSON(t, d)
			inv, err := d.Apply(op)
			if err != nil {
				if !bytes.Equal(before, mustJSON(t, d)) {
					t.Fatalf("round %d: failed %s changed the doc", round, op.Op)
				}
				continue
			}
			if err := d.Validate(); err != nil {
				t.Fatalf("round %d: invalid state after %s: %v", round, op.Op, err)
			}
			invs = append(invs, inv)
		}
		for i := len(invs) - 1; i >= 0; i-- {
			if _, err := d.Apply(invs[i]); err != nil {
				t.Fatalf("round %d undo %d: %v", round, i, err)
			}
		}
		if !bytes.Equal(mustJSON(t, d), orig) {
			t.Fatalf("round %d: undo did not restore the doc", round)
		}
	}
}

func allIDs(d *Doc) (ids, containers []string) {
	var walk func(ns []*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			ids = append(ids, n.ID)
			if n.T == "container" {
				containers = append(containers, n.ID)
			}
			walk(n.C)
		}
	}
	walk(d.Nodes)
	return
}

func randomOp(rng *rand.Rand, d *Doc, next *int) Op {
	ids, cs := allIDs(d)
	pick := func(l []string) string { return l[rng.IntN(len(l))] }
	parent := ""
	n := len(d.Nodes)
	if len(cs) > 0 && rng.IntN(3) > 0 {
		parent = pick(cs)
		p, _ := d.Find(parent)
		n = len(p.C)
	}
	newID := func() string { *next++; return "f" + strconv.Itoa(*next) }
	pickID := func() string {
		if len(ids) == 0 {
			return "none"
		}
		return pick(ids)
	}
	switch rng.IntN(7) {
	case 0:
		return Op{Op: "insert", Parent: parent, Index: rng.IntN(n + 1), Node: &Node{ID: newID(), T: "text", P: map[string]any{"text": "x"}}}
	case 1:
		// Containers with a child, to exercise depth and subtree moves.
		return Op{Op: "insert", Parent: parent, Index: rng.IntN(n + 1), Node: &Node{ID: newID(), T: "container",
			C: []*Node{{ID: newID(), T: "spacer"}}}}
	case 2:
		return Op{Op: "remove", ID: pickID()}
	case 3:
		return Op{Op: "move", ID: pickID(), Parent: parent, Index: rng.IntN(n + 1)}
	case 4:
		vals := []string{`"y"`, `null`, `5`}
		return Op{Op: "setProp", ID: pickID(), Key: "text", Value: json.RawMessage(vals[rng.IntN(3)])}
	default:
		vals := []string{`"space-3"`, `null`, `"12px"`, `"bad;}"`}
		return Op{Op: "setStyle", ID: pickID(), BP: []string{"base", "md", "sm"}[rng.IntN(3)], Key: "pt", Value: json.RawMessage(vals[rng.IntN(4)])}
	}
}
