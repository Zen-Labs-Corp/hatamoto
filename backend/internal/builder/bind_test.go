package builder

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBindSourcesAllValid(t *testing.T) {
	srcs := BindSources()
	if len(srcs) == 0 {
		t.Fatal("empty vocabulary")
	}
	for _, s := range srcs {
		src := strings.NewReplacer("<path>", "a.b", "<key>", "k").Replace(s.Source)
		class, err := ValidBind(src)
		if err != nil {
			t.Errorf("%s: %v", s.Source, err)
		}
		if class != s.Class {
			t.Errorf("%s: class %q, listed %q", s.Source, class, s.Class)
		}
	}
	srcs[0].Source = "changed"
	if BindSources()[0].Source == "changed" {
		t.Error("BindSources aliases the registry")
	}
}

func TestValidBindRefuses(t *testing.T) {
	for _, src := range []string{
		"", "entity", "entity.nope", "ENTITY.NAME", "entity.name.x",
		"entity.attributes.", "entity.attributes.page_doc", "entity.attributes.content.x",
		"entity.attributes.page_sections", "site.settings.", "site.settings.a.b.c.d.e",
		"entity.attributes.a b", "entity.attributes.a..b", "loop.item.attributes.page_doc",
		"site.address", "term.nope",
	} {
		if _, err := ValidBind(src); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidBind(%q) = %v, want ErrInvalid", src, err)
		}
	}
	for _, src := range []string{"entity.attributes.Price_1", "site.settings.a-b.c.d.e", "loop.item.attributes.x"} {
		if _, err := ValidBind(src); err != nil {
			t.Errorf("ValidBind(%q): %v", src, err)
		}
	}
}

func bindDoc(b map[string]string, kind string, p map[string]any) *Doc {
	return &Doc{V: 2, Nodes: []*Node{{ID: "n1", T: kind, P: p, B: b}}}
}

func TestBindClassMatrix(t *testing.T) {
	cases := []struct {
		kind, key, src string
		ok             bool
	}{
		{"heading", "text", "entity.name", true},
		{"heading", "text", "entity.url", true},
		{"heading", "text", "entity.image", true},
		{"heading", "text", "site.settings.x", true},
		{"button", "href", "entity.url", true},
		{"button", "href", "term.url", true},
		{"button", "href", "entity.attributes.link", true},
		{"button", "href", "entity.name", false},
		{"button", "href", "entity.image", false},
		{"image", "src", "entity.image", true},
		{"image", "src", "site.logo", true},
		{"image", "src", "entity.attributes.hero", true},
		{"image", "src", "entity.url", false},
		{"image", "src", "entity.title", false},
		{"image", "alt", "entity.title", true},
		{"heading", "level", "entity.name", false},
		{"heading", "hidden", "entity.name", false},
		{"html", "html", "entity.name", false},
		{"richtext", "html", "entity.excerpt", false},
		{"stat", "value", "entity.name", true},
		{"table", "caption", "entity.name", true},
		{"button", "nope", "entity.name", false},
		{"heading", "text", "entity.attributes.page_doc", false},
		{"heading", "text", "unknown.source", false},
	}
	for _, c := range cases {
		err := bindDoc(map[string]string{c.key: c.src}, c.kind, nil).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s.%s <- %s: err = %v, want ok=%v", c.kind, c.key, c.src, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s.%s <- %s: %v does not wrap ErrInvalid", c.kind, c.key, c.src, err)
		}
	}
}

func TestBindNeverOnStructuralKinds(t *testing.T) {
	for _, kind := range []string{"container", "component", "ref", "slot", "spacer", "divider"} {
		p := map[string]any(nil)
		switch kind {
		case "component":
			p = map[string]any{"type": "hero"}
		case "ref":
			p = map[string]any{"block": uuidA}
		}
		if err := bindDoc(map[string]string{"text": "entity.name"}, kind, p).Validate(); err == nil {
			t.Errorf("%s accepted a binding", kind)
		}
	}
}

func TestBindLoopItemContext(t *testing.T) {
	x := &Node{ID: "h", T: "heading", B: map[string]string{"text": "loop.item.title"}}
	if err := validateBinds(reg.Load(), x, false); err == nil || !strings.Contains(err.Error(), "inside a loop") {
		t.Errorf("outside a loop: %v", err)
	}
	if err := validateBinds(reg.Load(), x, true); err != nil {
		t.Errorf("inside a loop: %v", err)
	}
	if err := bindDoc(map[string]string{"text": "loop.item.title"}, "heading", nil).Validate(); err == nil {
		t.Error("doc with loop.item at top level validated")
	}
	if err := validateBinds(reg.Load(), &Node{ID: "h", T: "heading", B: map[string]string{"text": "entity.name"}}, false); err != nil {
		t.Errorf("entity source outside a loop: %v", err)
	}
}

func TestSetBindApplyUndo(t *testing.T) {
	d := opsFixture(t)
	before := string(mustJSON(t, d))

	inv, err := d.Apply(Op{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`"entity.name"`)})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := d.Find("h1"); n.B["text"] != "entity.name" {
		t.Fatalf("B = %v", n.B)
	}
	inv2, err := d.Apply(Op{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`"entity.title"`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(inv2.Value) != `"entity.name"` {
		t.Errorf("inverse value = %s", inv2.Value)
	}
	if _, err := d.Apply(inv2); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(inv); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.Find("h1"); n.B != nil {
		t.Errorf("B after undo = %v, want nil", n.B)
	}
	if got := string(mustJSON(t, d)); got != before {
		t.Errorf("undo changed the doc:\n%s\n%s", got, before)
	}
}

func TestSetBindRejectedLeavesDoc(t *testing.T) {
	d := opsFixture(t)
	before := string(mustJSON(t, d))
	for _, op := range []Op{
		{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`"entity.nope"`)},
		{Op: "setBind", ID: "h1", Key: "level", Value: json.RawMessage(`"entity.name"`)},
		{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`"loop.item.title"`)},
		{Op: "setBind", ID: "k", Key: "title", Value: json.RawMessage(`"entity.name"`)},
		{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`5`)},
		{Op: "setBind", ID: "zz", Key: "text", Value: json.RawMessage(`"entity.name"`)},
	} {
		if _, err := d.Apply(op); err == nil {
			t.Errorf("%+v accepted", op)
		}
		if got := string(mustJSON(t, d)); got != before {
			t.Fatalf("doc changed after %+v", op)
		}
	}
	// Unbinding a node with no binding is a no-op, not an error.
	if _, err := d.Apply(Op{Op: "setBind", ID: "h1", Key: "text", Value: json.RawMessage(`null`)}); err != nil {
		t.Errorf("unbind of nothing: %v", err)
	}
}

func TestBindParseExactRoundTrip(t *testing.T) {
	raw := `{"v":2,"nodes":[{"id":"s","t":"stat","p":{"value":"1","label":"x"},"b":{"label":"entity.name"}},` +
		`{"id":"h","t":"heading","p":{"level":2},"b":{"text":"entity.attributes.price"}},` +
		`{"id":"k","t":"component","p":{"type":"hero","content":{"n":12345678901234567890,"price":9.90}}}]}`
	d, err := ParseExact([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, digits := range []string{`12345678901234567890`, `9.90`} {
		if !strings.Contains(string(out), digits) {
			t.Errorf("round trip lost %s: %s", digits, out)
		}
	}
	var a, b any
	json.Unmarshal([]byte(raw), &a)
	json.Unmarshal(out, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("round trip:\n%s\n%s", out, raw)
	}
	if n, _ := d.Find("h"); n.B["text"] != "entity.attributes.price" {
		t.Errorf("B = %v", n.B)
	}
	// An empty b is omitted.
	d.Nodes[0].B = nil
	out, _ = json.Marshal(d)
	if strings.Contains(string(out), `"b"`) && !strings.Contains(string(out), `"b":{"text"`) {
		t.Errorf("empty b serialised: %s", out)
	}
	// The field is known: an unknown sibling is still refused.
	if _, err := Parse([]byte(`{"v":2,"nodes":[{"id":"h","t":"heading","bind":{}}]}`)); err == nil {
		t.Error("unknown field accepted")
	}
}

func TestBindCopiesAreIndependent(t *testing.T) {
	orig := &Node{ID: "a", T: "container", C: []*Node{{ID: "b", T: "heading", B: map[string]string{"text": "entity.name"}}}}

	c := orig.clone()
	c.C[0].B["text"] = "entity.title"
	if orig.C[0].B["text"] != "entity.name" {
		t.Error("clone aliases B")
	}

	p := PrefixIDs([]*Node{orig}, "x-")
	p[0].C[0].B["text"] = "entity.title"
	if orig.C[0].B["text"] != "entity.name" || p[0].C[0].ID != "x-b" {
		t.Error("PrefixIDs aliases B or lost ids")
	}

	n := 0
	f := FreshIDs([]*Node{orig}, func() string { n++; return "id" + string(rune('0'+n)) })
	if f[0].C[0].B["text"] != "entity.name" {
		t.Error("FreshIDs dropped B")
	}
	f[0].C[0].B["text"] = "entity.title"
	if orig.C[0].B["text"] != "entity.name" {
		t.Error("FreshIDs aliases B")
	}
}

func TestBoundDocIsNotV1Expressible(t *testing.T) {
	d := &Doc{V: 2, Nodes: []*Node{{ID: "h", T: "heading", B: map[string]string{"text": "entity.name"}}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if V1Expressible(d) {
		t.Error("a bound heading is v1 expressible")
	}
	// A v1-shaped wrapper stays v1-shaped until a binding is forced onto it.
	w := &Node{ID: "w", T: "container", P: map[string]any{"tag": "section"}, C: []*Node{{ID: "c", T: "component", P: map[string]any{"type": "hero"}}}}
	if _, ok := V1Shaped(w); !ok {
		t.Fatal("fixture not v1 shaped")
	}
	w.B = map[string]string{"x": "entity.name"}
	if _, ok := V1Shaped(w); ok {
		t.Error("container with b is v1 shaped")
	}
	w.B = nil
	w.C[0].B = map[string]string{"x": "entity.name"}
	if _, ok := V1Shaped(w); ok {
		t.Error("component with b is v1 shaped")
	}
}

func TestKindsExposeBind(t *testing.T) {
	byKind := map[string]KindInfo{}
	for _, k := range Kinds() {
		byKind[k.Kind] = k
	}
	bind := func(kind, key string) (string, bool) {
		for _, p := range byKind[kind].Props {
			if p.Key == key {
				return p.Bind, true
			}
		}
		return "", false
	}
	for _, c := range []struct{ kind, key, want string }{
		{"heading", "text", "text"}, {"text", "text", "text"}, {"button", "text", "text"},
		{"button", "href", "url"}, {"image", "src", "image"}, {"image", "alt", "text"},
		{"stat", "value", "text"}, {"quote", "cite", "text"}, {"map", "query", "text"},
		{"html", "html", ""}, {"heading", "level", ""}, {"heading", "hidden", ""},
		{"richtext", "html", ""}, {"video", "url", ""},
	} {
		got, ok := bind(c.kind, c.key)
		if !ok && c.want != "" {
			t.Errorf("%s has no prop %s", c.kind, c.key)
		}
		if got != c.want {
			t.Errorf("%s.%s Bind = %q, want %q", c.kind, c.key, got, c.want)
		}
	}
	// Never on a rich/free-form kind prop or structural kinds.
	for _, k := range []string{"container", "component", "ref", "slot", "html", "richtext"} {
		for _, p := range byKind[k].Props {
			if p.Bind != "" {
				t.Errorf("%s.%s is bindable", k, p.Key)
			}
		}
	}
}

func TestParseWidgetRejectsBadBindClass(t *testing.T) {
	_, err := parseWidget("zed", []byte(`{"type":"object","x-hm":{"label":"Z","group":"Content"},
		"properties":{"a":{"type":"string","x-hm-bind":"html"}}}`))
	if err == nil {
		t.Error("bad x-hm-bind class accepted")
	}
}
