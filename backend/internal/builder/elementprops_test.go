package builder

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// nodeWith builds a one-node doc of kind with props p (inside its required host).
func soloDoc(kind string, p map[string]any) *Doc {
	n := &Node{ID: "a", T: kind, P: p}
	if kind == "component" {
		p["type"] = "hero"
	}
	if kind == "ref" {
		p["block"] = "11111111-2222-3333-4444-555555555555"
	}
	d := &Doc{V: 2, Nodes: []*Node{n}}
	if k, _ := kindOf(kind); k.meta != nil && k.meta.OnlyIn != "" {
		d.Nodes = []*Node{{ID: "host", T: k.meta.OnlyIn, C: []*Node{n}}}
	}
	return d
}

func TestElementPropsPerKind(t *testing.T) {
	for name := range reg.Load().kinds {
		takes := !noElementProps[name]
		for _, p := range []map[string]any{{"anchor": "pricing"}, {"aria": "Buy now"}, {"tip": "Hint"}} {
			err := soloDoc(name, cloneProps(p)).Validate()
			if _, aria := p["aria"]; aria && noAriaKinds[name] {
				if err == nil {
					t.Errorf("kind %s accepts an accessible name", name)
				}
				continue
			}
			if takes && err != nil {
				t.Errorf("kind %s rejects %v: %v", name, p, err)
			}
			if !takes && err == nil {
				t.Errorf("kind %s accepts %v", name, p)
			}
		}
	}
	for _, kind := range []string{"component", "ref", "slot", "spacer"} {
		if !noElementProps[kind] {
			t.Errorf("%s must be excluded", kind)
		}
	}
}

func cloneProps(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestAnchorShape(t *testing.T) {
	for _, a := range []string{"Pricing", "1st", "a_b", "", "-x", strings.Repeat("a", 65)} {
		if err := soloDoc("container", map[string]any{"anchor": a}).Validate(); err == nil {
			t.Errorf("anchor %q accepted", a)
		}
	}
	if err := soloDoc("container", map[string]any{"anchor": strings.Repeat("a", 64)}).Validate(); err != nil {
		t.Errorf("64-char anchor: %v", err)
	}
}

func TestLinkOptionShape(t *testing.T) {
	ok := []map[string]any{
		{"target": "_blank", "rel": "nofollow sponsored ugc"},
		{"rel": "ugc"},
		{"rel": "ugc ugc"},
	}
	bad := []map[string]any{
		{"target": "_self"}, {"rel": "noopener"}, {"rel": "nofollow  ugc"}, {"rel": "nofollow sponsored ugc nofollow"}, {"rel": ""},
	}
	for _, kind := range []string{"button", "image", "container"} {
		for _, p := range ok {
			if err := soloDoc(kind, cloneProps(p)).Validate(); err != nil {
				t.Errorf("%s %v: %v", kind, p, err)
			}
		}
		for _, p := range bad {
			if err := soloDoc(kind, cloneProps(p)).Validate(); err == nil {
				t.Errorf("%s accepts %v", kind, p)
			}
		}
	}
	if err := soloDoc("image", map[string]any{"src": "/a.jpg", "href": "/x", "caption": "c", "lightbox": true}).Validate(); err != nil {
		t.Errorf("image settings: %v", err)
	}
	if err := soloDoc("image", map[string]any{"caption": strings.Repeat("x", 301)}).Validate(); err == nil {
		t.Error("301-char caption accepted")
	}
	if err := soloDoc("container", map[string]any{"href": "/x"}).Validate(); err != nil {
		t.Errorf("container href: %v", err)
	}
	if err := soloDoc("gallery", map[string]any{"lightbox": true}).Validate(); err != nil {
		t.Errorf("gallery lightbox: %v", err)
	}
	if err := soloDoc("heading", map[string]any{"target": "_blank"}).Validate(); err == nil {
		t.Error("heading accepts target")
	}
}

func TestUniversalHiddenFromProps(t *testing.T) {
	for _, k := range Kinds() {
		for _, p := range k.Props {
			for _, u := range UniversalKeys {
				if p.Key == u {
					t.Errorf("kind %s lists universal prop %s as a field", k.Kind, u)
				}
			}
		}
		want := 5
		if noElementProps[k.Kind] {
			want = 2
		} else if noAriaKinds[k.Kind] {
			want = 4
		}
		if len(k.Universal) != want {
			t.Errorf("kind %s Universal = %v", k.Kind, k.Universal)
		}
	}
}

func TestPackRejectsReservedProps(t *testing.T) {
	for _, key := range reservedBefore {
		schema := `{"type":"object","additionalProperties":false,
			"x-hm":{"label":"Promo","hint":"h","group":"Layout"},
			"properties":{"` + key + `":{"type":"string"}}}`
		_, err := ParsePackWidget("acme", "promo", []byte(schema), map[string]bool{"acme.promo": true, "promo": true})
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("pack declaring %q: err = %v, want ErrInvalid", key, err)
		}
	}
}

// A plugin that used anchor/aria/tip before the core did keeps its own prop: the
// kind validates it by the plugin's schema and the core skips that setting.
func TestPackKeepsItsOwnElementSettingNames(t *testing.T) {
	schema := `{"type":"object","additionalProperties":false,
		"x-hm":{"label":"Promo","hint":"h","group":"Layout"},
		"properties":{"anchor":{"type":"string"},"aria":{"type":"string"}}}`
	w := packOf(t, "acme", "promo", schema, "promo")
	if got := w.Owned(); len(got) != 2 || got[0] != "anchor" || got[1] != "aria" {
		t.Fatalf("Owned = %v", got)
	}
	withPack(t, w)
	if err := soloDoc(w.Name, map[string]any{"anchor": "Any Text!", "aria": "x", "tip": "z"}).Validate(); err != nil {
		t.Errorf("own props and the core tip: %v", err)
	}
	if !PackOwns(w.Name, "anchor") || PackOwns(w.Name, "tip") || PackOwns("button", "anchor") {
		t.Error("PackOwns wrong")
	}
	var info KindInfo
	for _, k := range Kinds() {
		if k.Kind == w.Name {
			info = k
		}
	}
	if slices.Contains(info.Universal, "anchor") || slices.Contains(info.Universal, "aria") || !slices.Contains(info.Universal, "tip") {
		t.Errorf("universal = %v", info.Universal)
	}
	fields := map[string]bool{}
	for _, p := range info.Props {
		fields[p.Key] = true
	}
	if !fields["anchor"] || !fields["aria"] {
		t.Errorf("own props are not fields: %v", info.Props)
	}
	// its anchor value is not an anchor of the page
	d := soloDoc(w.Name, map[string]any{"anchor": "dup"})
	d.Nodes = append(d.Nodes, &Node{ID: "b", T: "container", P: map[string]any{"anchor": "dup"}})
	if err := d.CheckSettings(); err != nil {
		t.Errorf("a pack's own anchor prop counted as a page anchor: %v", err)
	}
}

func TestPackKindTakesElementProps(t *testing.T) {
	w := packOf(t, "acme", "promo", promoSchema, "promo")
	withPack(t, w)
	if err := soloDoc(w.Name, map[string]any{"anchor": "x", "aria": "y", "tip": "z"}).Validate(); err != nil {
		t.Errorf("pack kind rejects element props: %v", err)
	}
}

// An older binary has schemas without the new props and additionalProperties
// false: a page using one is refused, not half rendered (rollback floor).
func TestOldSchemaRefusesNewProps(t *testing.T) {
	old := `{"type":"object","additionalProperties":false,"properties":{"text":{"type":"string"},"href":{"type":"string"},"hidden":{"type":"boolean"}}}`
	c := jsonschema.NewCompiler()
	s, err := compileKind(c, "button", old)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"target", "rel"} {
		if s.Validate(map[string]any{k: "_blank"}) == nil {
			t.Errorf("old schema accepts %q", k)
		}
	}
}

// A page_sections (v1) section keeps converting; an anchor leaves the v1 shape
// like any other prop the section partial cannot express.
func TestV1ShapeAndAnchor(t *testing.T) {
	comp := func() *Node { return &Node{ID: "c", T: "component", P: map[string]any{"type": "hero"}} }
	plain := &Node{ID: "s", T: "container", P: map[string]any{"tag": "section", "v1": true}, C: []*Node{comp()}}
	if _, ok := V1Shaped(plain); !ok {
		t.Fatal("plain v1 section is not v1-shaped")
	}
	anchored := &Node{ID: "s", T: "container", P: map[string]any{"tag": "section", "v1": true, "anchor": "top-hero"}, C: []*Node{comp()}}
	if _, ok := V1Shaped(anchored); ok {
		t.Error("section with an anchor is still v1-shaped")
	}
	if err := (&Doc{V: 2, Nodes: []*Node{plain}}).Validate(); err != nil {
		t.Error("plain section invalid")
	}
}

func TestAriaKinds(t *testing.T) {
	for _, kind := range []string{"text", "icon", "menu", "breadcrumbs", "tabpanel"} {
		if !noAriaKinds[kind] {
			t.Errorf("%s must take no accessible name", kind)
		}
		if u := universalFor(kind, nil); slices.Contains(u, "aria") || !slices.Contains(u, "anchor") {
			t.Errorf("%s universal = %v", kind, u)
		}
	}
	if u := universalFor("button", nil); !slices.Contains(u, "aria") {
		t.Errorf("button universal = %v", u)
	}
}

// A stale aria on a div container (its link or tag was removed) is ignored at
// render, never refused on save: the inspector no longer shows the field.
func TestContainerAriaOnlyWithALinkOrALandmark(t *testing.T) {
	cases := []struct {
		p       map[string]any
		allowed bool
	}{
		{map[string]any{"aria": "Plans"}, false},
		{map[string]any{"aria": "Plans", "tag": "div"}, false},
		{map[string]any{"aria": "Plans", "tag": "section"}, true},
		{map[string]any{"aria": "Plans", "tag": "nav"}, true},
		{map[string]any{"aria": "Plans", "href": "/plans"}, true},
		{map[string]any{"tag": "div"}, false},
	}
	for _, c := range cases {
		d := soloDoc("container", cloneProps(c.p))
		if err := d.CheckSettings(); err != nil {
			t.Errorf("container %v: save refused: %v", c.p, err)
		}
		if got := AriaAllowed(d.Nodes[0]); got != c.allowed {
			t.Errorf("container %v: AriaAllowed = %v, want %v", c.p, got, c.allowed)
		}
	}
	bound := soloDoc("container", map[string]any{"aria": "Plans"})
	bound.Nodes[0].B = map[string]string{"href": "item.url"}
	if err := bound.CheckSettings(); err != nil || !AriaAllowed(bound.Nodes[0]) {
		t.Errorf("bound link: %v", err)
	}
}

func TestLightboxUnnamed(t *testing.T) {
	img := func(id string, p map[string]any) *Node {
		p["src"] = "/media/a.jpg"
		return &Node{ID: id, T: "image", P: p}
	}
	d := &Doc{V: 2, Nodes: []*Node{
		img("bare", map[string]any{"lightbox": true}),
		img("deco", map[string]any{"lightbox": true, "decorative": true, "alt": "ignored when decorative"}),
		img("decocap", map[string]any{"lightbox": true, "decorative": true, "caption": "Cat"}),
		img("alt", map[string]any{"lightbox": true, "alt": "Cat"}),
		img("aria", map[string]any{"lightbox": true, "aria": "Open the cat"}),
		img("plain", map[string]any{}),
		img("linked", map[string]any{"href": "/x"}),
		{ID: "g", T: "gallery", P: map[string]any{"lightbox": true}, C: []*Node{
			img("g1", map[string]any{}),
			img("g2", map[string]any{"alt": "Dog"}),
		}},
	}}
	got := d.LightboxUnnamed()
	if want := []string{"bare", "deco", "g1"}; !slices.Equal(got, want) {
		t.Errorf("LightboxUnnamed = %v, want %v", got, want)
	}
}
