package builder

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidateRejects(t *testing.T) {
	bad := map[string]string{
		"bad breakpoint":     `{"v":2,"nodes":[{"id":"a","t":"heading","s":{"xl":{"pt":"space-1"}}}]}`,
		"id with space":      `{"v":2,"nodes":[{"id":"a b","t":"heading"}]}`,
		"id too long":        `{"v":2,"nodes":[{"id":"` + strings.Repeat("a", 65) + `","t":"heading"}]}`,
		"unknown kind":       `{"v":2,"nodes":[{"id":"a","t":"script"}]}`,
		"ref kind":           `{"v":2,"nodes":[{"id":"a","t":"ref"}]}`,
		"children on widget": `{"v":2,"nodes":[{"id":"a","t":"heading","c":[{"id":"b","t":"text"}]}]}`,
		"tag script":         `{"v":2,"nodes":[{"id":"a","t":"container","p":{"tag":"script"}}]}`,
		"layout bad":         `{"v":2,"nodes":[{"id":"a","t":"container","p":{"layout":"float"}}]}`,
		"duplicate id":       `{"v":2,"nodes":[{"id":"a","t":"text"},{"id":"a","t":"text"}]}`,
		"duplicate nested":   `{"v":2,"nodes":[{"id":"a","t":"container","c":[{"id":"a","t":"text"}]}]}`,
		"wrong version":      `{"v":1,"nodes":[]}`,
		"null node":          `{"v":2,"nodes":[null]}`,
		"heading level 7":    `{"v":2,"nodes":[{"id":"a","t":"heading","p":{"level":7}}]}`,
		"heading level 1.5":  `{"v":2,"nodes":[{"id":"a","t":"heading","p":{"level":1.5}}]}`,
		"unknown prop":       `{"v":2,"nodes":[{"id":"a","t":"text","p":{"onclick":"x"}}]}`,
		"text not string":    `{"v":2,"nodes":[{"id":"a","t":"text","p":{"text":5}}]}`,
		"text too long":      `{"v":2,"nodes":[{"id":"a","t":"text","p":{"text":"` + strings.Repeat("x", maxText+1) + `"}}]}`,
		"component no type":  `{"v":2,"nodes":[{"id":"a","t":"component","p":{"content":{}}}]}`,
		"component bad body": `{"v":2,"nodes":[{"id":"a","t":"component","p":{"type":"hero","content":"x"}}]}`,
		"unknown field":      `{"v":2,"nodes":[],"extra":1}`,
		"trailing data":      `{"v":2,"nodes":[]} {}`,
	}
	for name, s := range bad {
		err := ValidateRaw([]byte(s))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error does not wrap ErrInvalid: %v", name, err)
		}
	}
}

func TestValidateAccepts(t *testing.T) {
	ok := []string{
		`{"v":2,"nodes":[]}`,
		`{"v":2,"nodes":[{"id":"c","t":"container","p":{"tag":"main","layout":"grid"},"c":[
			{"id":"h","t":"heading","p":{"text":"Hi","level":2}},
			{"id":"t","t":"text","p":{"text":"x"}},
			{"id":"b","t":"button","p":{"text":"Go","href":"/x"}},
			{"id":"i","t":"image","p":{"src":"/a.png","alt":"a"}},
			{"id":"s","t":"spacer"},{"id":"d","t":"divider"},
			{"id":"k","t":"component","p":{"type":"hero","title":"T","content":{"a":1}}}]}]}`,
	}
	for _, s := range ok {
		if err := ValidateRaw([]byte(s)); err != nil {
			t.Errorf("rejected: %v\n%s", err, s)
		}
	}
}

func chain(depth int) *Doc {
	root := &Node{ID: "d0", T: "container"}
	cur := root
	for i := 1; i < depth; i++ {
		c := &Node{ID: "d" + strings.Repeat("x", i), T: "container"}
		cur.C = []*Node{c}
		cur = c
	}
	return &Doc{V: 2, Nodes: []*Node{root}}
}

func TestValidateDepthLimit(t *testing.T) {
	if err := chain(MaxDepth).Validate(); err != nil {
		t.Errorf("depth %d rejected: %v", MaxDepth, err)
	}
	err := chain(MaxDepth + 1).Validate()
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("depth %d accepted: %v", MaxDepth+1, err)
	}
}

func TestValidateNodeLimit(t *testing.T) {
	d := &Doc{V: 2}
	for i := 0; i < MaxNodes; i++ {
		d.Nodes = append(d.Nodes, &Node{ID: "n" + strconv.Itoa(i), T: "spacer"})
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("%d nodes rejected: %v", MaxNodes, err)
	}
	d.Nodes = append(d.Nodes, &Node{ID: "extra", T: "spacer"})
	if !errors.Is(d.Validate(), ErrInvalid) {
		t.Error("accepted more than MaxNodes")
	}
}

func TestValidateRawSizeCap(t *testing.T) {
	// A valid doc made oversize by one huge component body.
	big := `{"v":2,"nodes":[{"id":"a","t":"component","p":{"type":"x","content":{"b":"` +
		strings.Repeat("x", MaxBytes) + `"}}}]}`
	err := ValidateRaw([]byte(big))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("want size error, got %v", err)
	}
}

func TestGoBuiltDocValidates(t *testing.T) {
	d := &Doc{V: 2, Nodes: []*Node{{ID: "h", T: "heading", P: map[string]any{"level": 2, "text": "x"}}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFindCount(t *testing.T) {
	d := chain(3)
	n, p := d.Find("dxx")
	if n == nil || p == nil || p.ID != "dx" {
		t.Fatalf("Find: %v %v", n, p)
	}
	if n, p := d.Find("d0"); n == nil || p != nil {
		t.Fatal("top-level find")
	}
	if n, _ := d.Find("nope"); n != nil {
		t.Fatal("found missing")
	}
	if d.Count() != 3 {
		t.Fatalf("count %d", d.Count())
	}
}
