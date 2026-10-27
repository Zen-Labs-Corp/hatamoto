package builder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSKey(t *testing.T) {
	good := map[string][2]string{
		"base": {"base", ""}, "md": {"md", ""}, "sm": {"sm", ""},
		"base:hover": {"base", "hover"}, "md:focus": {"md", "focus"},
		"sm:active": {"sm", "active"}, "base:parent-hover": {"base", "parent-hover"},
	}
	for k, want := range good {
		bp, st, ok := ParseSKey(k)
		if !ok || bp != want[0] || st != want[1] {
			t.Errorf("ParseSKey(%q) = %q,%q,%v", k, bp, st, ok)
		}
	}
	for _, k := range []string{"", ":", "base:", ":hover", "xl", "xl:hover", "base:visited", "base:HOVER",
		"base:hover:focus", "base::hover", "base :hover", "base:hover ", "Base", "base:hover}{", "base:parent_hover",
		"base:hover\n", "md:hover,body", "base;x"} {
		if bp, st, ok := ParseSKey(k); ok {
			t.Errorf("ParseSKey(%q) accepted as %q,%q", k, bp, st)
		}
	}
	if len(sKeyOrder) != 15 {
		t.Fatalf("sKeyOrder has %d keys", len(sKeyOrder))
	}
	for _, k := range sKeyOrder {
		if _, _, ok := ParseSKey(k); !ok {
			t.Errorf("sKeyOrder key %q does not parse", k)
		}
	}
}

func TestSKeysOrder(t *testing.T) {
	s := map[string]map[string]string{"zz": {}, "sm": {}, "base:hover": {}, "base": {}, "md:focus": {}, "aa": {}}
	got := strings.Join(sKeys(s), ",")
	if want := "base,base:hover,md:focus,sm,aa,zz"; got != want {
		t.Errorf("sKeys = %s, want %s", got, want)
	}
}

func TestStatesFlagAllowList(t *testing.T) {
	var on []string
	for k, sk := range styleKeys {
		if sk.states {
			on = append(on, k)
		}
	}
	if len(on) != 34 {
		t.Errorf("%d state keys: %v", len(on), on)
	}
	for _, k := range []string{"display", "hide", "pos", "top", "cols", "gap", "order", "w", "h", "pt", "mt", "ml", "bgimg", "fs"} {
		if styleKeys[k].states {
			t.Errorf("layout key %s must not allow states", k)
		}
	}
	for _, e := range StyleSpecTable() {
		if e.States != styleKeys[e.Key].states {
			t.Errorf("table %s states = %v", e.Key, e.States)
		}
	}
}

func statesDoc(t *testing.T, s string) error {
	t.Helper()
	_, err := Parse([]byte(`{"v":2,"nodes":[` + s + `]}`))
	return err
}

func TestValidateStateKeys(t *testing.T) {
	card := func(s string) string {
		return `{"id":"top","t":"container","c":[{"id":"card","t":"container","s":` + s + `}]}`
	}
	ok := []string{
		`{"base":{"bg":"primary"},"base:hover":{"bg":"accent","shadow":"lg"},"md:focus":{"bc":"accent"},"sm:active":{"op":"0.5"},"base:parent-hover":{"color":"accent"}}`,
	}
	for _, s := range ok {
		if err := statesDoc(t, card(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := map[string]string{
		`{"base:visited":{"bg":"primary"}}`:     "unknown breakpoint",
		`{"xl:hover":{"bg":"primary"}}`:         "unknown breakpoint",
		`{"base:":{"bg":"primary"}}`:            "unknown breakpoint",
		`{":hover":{"bg":"primary"}}`:           "unknown breakpoint",
		`{"base:hover:focus":{"bg":"primary"}}`: "unknown breakpoint",
		`{"base:hover":{"bg":"red;}"}}`:         "bg",
		`{"base:hover":{"display":"none"}}`:     "not allowed in the hover state",
		`{"md:focus":{"pt":"space-1"}}`:         "not allowed in the focus state",
		`{"base:active":{"hide":"1"}}`:          "not allowed in the active state",
		`{"base:hover":{"nope":"1"}}`:           "unknown style key",
		`{"base:parent-hover":{"cols":"2"}}`:    "not allowed in the parent-hover state",
	}
	for s, want := range bad {
		err := statesDoc(t, card(s))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", s, err, want)
		}
	}
}

func TestParentHoverNeedsParent(t *testing.T) {
	ph := `"s":{"base:parent-hover":{"bg":"accent"}}`
	if err := statesDoc(t, `{"id":"top","t":"container",`+ph+`}`); err == nil || !strings.Contains(err.Error(), "parent-hover") {
		t.Errorf("top-level parent-hover: %v", err)
	}
	if err := statesDoc(t, `{"id":"p","t":"container","c":[{"id":"x","t":"container",`+ph+`}]}`); err != nil {
		t.Errorf("nested parent-hover: %v", err)
	}
	for _, p := range []*Node{nil, {T: "ref"}, {T: "component"}} {
		if canParentHover(p) {
			t.Errorf("canParentHover(%v) = true", p)
		}
	}
	if !canParentHover(&Node{T: "container"}) {
		t.Error("container parent refused")
	}
}

func TestOrphanStylesAreChecked(t *testing.T) {
	for _, s := range []string{`{"xl":{"bg":"primary"}}`, `{"base:hover":{"display":"none"}}`, `{"base:visited":{"bg":"primary"}}`} {
		err := statesDoc(t, `{"id":"o","t":"pack/gone","s":`+s+`}`)
		if err == nil {
			t.Errorf("orphan with s=%s accepted", s)
		}
	}
}

func TestSetStyleStates(t *testing.T) {
	d := opsFixture(t)
	set := func(id, bp, k, v string) error {
		_, err := d.Apply(Op{Op: "setStyle", ID: id, BP: bp, Key: k, Value: json.RawMessage(v)})
		return err
	}
	if err := set("col1", "base:hover", "bg", `"accent"`); err != nil {
		t.Fatal(err)
	}
	if err := set("col1", "md:parent-hover", "color", `"accent"`); err != nil {
		t.Fatal(err)
	}
	inv, err := d.Apply(Op{Op: "setStyle", ID: "col1", BP: "base:hover", Key: "bg", Value: json.RawMessage(`"primary"`)})
	if err != nil || string(inv.Value) != `"accent"` {
		t.Fatalf("inverse = %s, %v", inv.Value, err)
	}
	for name, c := range map[string][4]string{
		"top-level parent-hover": {"hero", "base:parent-hover", "bg", `"accent"`},
		"bad state":              {"col1", "base:visited", "bg", `"accent"`},
		"bad bp":                 {"col1", "xl:hover", "bg", `"accent"`},
		"empty state":            {"col1", "base:", "bg", `"accent"`},
		"layout key in state":    {"col1", "base:hover", "display", `"none"`},
		"bad value in state":     {"col1", "base:hover", "bg", `"red;}"`},
	} {
		if err := set(c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Clearing a state key removes the empty state map.
	if err := set("col1", "base:hover", "bg", `null`); err != nil {
		t.Fatal(err)
	}
	n, _ := d.Find("col1")
	if _, ok := n.S["base:hover"]; ok {
		t.Error("empty state map kept")
	}
}

// Moving a node that holds parent-hover to the top level is refused by the
// post-op validation of Apply (accepted limitation, documented).
func TestMoveParentHoverToTopLevelRefused(t *testing.T) {
	d := opsFixture(t)
	if _, err := d.Apply(Op{Op: "setStyle", ID: "col1", BP: "base:parent-hover", Key: "bg", Value: json.RawMessage(`"accent"`)}); err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	n, _ := d.Find("col1")
	moved := &Doc{V: 2, Nodes: []*Node{n}}
	if err := moved.Validate(); err == nil || !strings.Contains(err.Error(), "parent-hover") {
		t.Errorf("moved to top level: %v", err)
	}
}

func TestPresetStates(t *testing.T) {
	p := Preset{S: map[string]map[string]string{"base": {"bg": "primary"}, "base:hover": {"bg": "accent"}, "sm:focus": {"bc": "accent"}}}
	if err := ValidatePreset("card", p); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]map[string]map[string]string{
		"parent-hover": {"base:parent-hover": {"bg": "accent"}},
		"bad key":      {"base:visited": {"bg": "accent"}},
		"layout":       {"base:hover": {"pt": "space-1"}},
	} {
		if ValidatePreset("card", Preset{S: s}) == nil {
			t.Errorf("preset %s accepted", name)
		}
	}
	// parsePreset drops unknown keys loudly instead of skipping them.
	got, errs := parsePreset(map[string]any{"s": map[string]any{
		"xl":         map[string]any{"bg": "primary"},
		"base:hover": map[string]any{"bg": "accent"},
	}})
	if len(errs) != 1 || got.S["base:hover"]["bg"] != "accent" || got.S["xl"] != nil {
		t.Errorf("parsePreset = %+v, %v", got, errs)
	}
}

// An older binary only knows plain breakpoints; its parse error for any other
// key is the rollback floor of this release.
func TestRollbackFloorMessage(t *testing.T) {
	err := statesDoc(t, `{"id":"a","t":"container","s":{"xl":{"bg":"primary"}}}`)
	if err == nil || !strings.Contains(err.Error(), "unknown breakpoint") {
		t.Errorf("err = %v", err)
	}
}

func TestComposedGroups(t *testing.T) {
	cases := []struct {
		name string
		s    map[string]map[string]string
		want string
	}{
		{"transform order", map[string]map[string]string{"base": {"sc": "1.02", "ty": "-4px", "rot": "-15deg", "tx": "2px", "skx": "5deg", "sky": "3deg"}},
			".hm-n-g{transform:translateX(2px) translateY(-4px) rotate(-15deg) skewX(5deg) skewY(3deg) scale(1.02);}"},
		{"md keeps base rotation", map[string]map[string]string{"base": {"rot": "10deg", "ty": "-4px"}, "md": {"ty": "0"}},
			".hm-n-g{transform:translateY(-4px) rotate(10deg);}@media (max-width:1024px){.hm-n-g{transform:translateY(0) rotate(10deg);}}"},
		{"md without own part emits nothing", map[string]map[string]string{"base": {"rot": "10deg"}, "md": {"pt": "space-1"}},
			".hm-n-g{transform:rotate(10deg);}@media (max-width:1024px){.hm-n-g{padding-top:var(--hm-space-1,0.25rem);}}"},
		{"filter order", map[string]map[string]string{"base": {"hue": "90deg", "blur": "4px", "gray": "50%", "sat": "80%", "bright": "120%", "contrast": "90%"}},
			".hm-n-g{filter:blur(4px) brightness(120%) contrast(90%) saturate(80%) grayscale(50%) hue-rotate(90deg);}"},
		{"backdrop and blend", map[string]map[string]string{"base": {"bblur": "8px", "blend": "multiply", "torigin": "top-left"}},
			".hm-n-g{backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);mix-blend-mode:multiply;transform-origin:left top;}"},
		{"transition", map[string]map[string]string{"base": {"trans": "transform-shadow", "tdur": "200", "tease": "ease-out"}, "sm": {"tdur": "100"}},
			"@media (prefers-reduced-motion:no-preference){.hm-n-g{transition-property:transform,box-shadow;transition-duration:200ms;transition-timing-function:ease-out;transition-delay:0ms;}}" +
				"@media (max-width:640px) and (prefers-reduced-motion:no-preference){.hm-n-g{transition-property:transform,box-shadow;transition-duration:100ms;transition-timing-function:ease-out;transition-delay:0ms;}}"},
		{"transition none", map[string]map[string]string{"base": {"trans": "none"}},
			"@media (prefers-reduced-motion:no-preference){.hm-n-g{transition-property:none;transition-duration:200ms;transition-timing-function:ease;transition-delay:0ms;}}"},
		{"transition keeps plain rule first", map[string]map[string]string{"base": {"trans": "opacity", "op": "0.5"}},
			".hm-n-g{opacity:0.5;}@media (prefers-reduced-motion:no-preference){.hm-n-g{transition-property:opacity;transition-duration:200ms;transition-timing-function:ease;transition-delay:0ms;}}"},
	}
	for _, c := range cases {
		got := NodeCSS(&Node{ID: "g", T: "container", S: c.s})
		want := strings.ReplaceAll(c.want, "backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);", "backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);")
		if got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// Every transition declaration is inside a prefers-reduced-motion media block.
func TestTransitionsOnlyInReducedMotionMedia(t *testing.T) {
	css := NodeCSS(&Node{ID: "g", T: "container", S: map[string]map[string]string{
		"base": {"trans": "all", "tdur": "300", "tdelay": "75", "op": "1"},
		"md":   {"tease": "linear"}, "sm": {"trans": "none"}}})
	for _, part := range strings.Split(css, "@media")[1:] {
		if strings.Contains(part, "transition") && !strings.Contains(part, "prefers-reduced-motion:no-preference") {
			t.Errorf("transition outside reduced-motion media: %s", part)
		}
	}
	if strings.Count(css, "@media") != strings.Count(css, "(prefers-reduced-motion:no-preference)") {
		t.Errorf("css = %s", css)
	}
}

// The cascade of a state rule: hover adds ty and keeps the rotation of Normal,
// and md:hover walks base:hover then md:hover over the plain chain.
func TestCascadeStates(t *testing.T) {
	s := map[string]map[string]string{
		"base":       {"rot": "10deg", "ty": "0"},
		"md":         {"rot": "20deg"},
		"base:hover": {"ty": "-4px"},
		"md:hover":   {"sc": "1.1"},
	}
	keys := transformGroup.keys
	vals, own := cascade(s, "base", "hover", keys, CSSOpts{})
	if got := transformGroup.css(vals); !own || got != "translateY(-4px) rotate(10deg)" {
		t.Errorf("base:hover = %q own=%v", got, own)
	}
	vals, own = cascade(s, "md", "hover", keys, CSSOpts{})
	if got := transformGroup.css(vals); !own || got != "translateY(-4px) rotate(20deg) scale(1.1)" {
		t.Errorf("md:hover = %q own=%v", got, own)
	}
	if _, own := cascade(s, "sm", "hover", keys, CSSOpts{}); own {
		t.Error("sm:hover is not own")
	}
}

const (
	shSm = "var(--hm-shadow-sm,0 1px 2px rgba(0,0,0,.08))"
	shLg = "var(--hm-shadow-lg,0 12px 32px rgba(0,0,0,.14))"
	rm   = "@media (prefers-reduced-motion:no-preference){"
	tr   = "transition-property:transform,box-shadow;transition-duration:200ms;transition-timing-function:ease-out;transition-delay:0ms;"
)

type sm = map[string]map[string]string

func under(n, parent *Node, o CSSOpts) string { return nodeCSSIn(n, parent, o) }

func TestStateRuleGoldens(t *testing.T) {
	g := func(s sm) *Node { return &Node{ID: "g", T: "container", S: s} }
	p := &Node{ID: "p", T: "container"}
	cases := []struct {
		name   string
		n      *Node
		parent *Node
		o      CSSOpts
		want   string
	}{
		{"hover-lift card", g(sm{
			"base":       {"shadow": "sm", "trans": "transform-shadow", "tdur": "200", "tease": "ease-out"},
			"base:hover": {"ty": "-4px", "shadow": "lg"}}), nil, CSSOpts{},
			".hm-n-g{box-shadow:" + shSm + ";}" + rm + ".hm-n-g{" + tr + "}}" +
				".hm-n-g:hover{box-shadow:" + shLg + ";transform:translateY(-4px);}"},
		{"focus is focus-visible", g(sm{"base:focus": {"bc": "accent"}}), nil, CSSOpts{},
			".hm-n-g:focus-visible{border-color:var(--hm-accent,#c8102e);}"},
		{"active", g(sm{"base:active": {"sc": "0.98"}}), nil, CSSOpts{},
			".hm-n-g:active{transform:scale(0.98);}"},
		{"parent-hover uses the actual parent", g(sm{"base:parent-hover": {"op": "0.5"}}), p, CSSOpts{},
			".hm-n-p:hover .hm-n-g{opacity:0.5;}"},
		{"parent-hover without parent compiles to nothing", g(sm{"base:parent-hover": {"op": "0.5"}}), nil, CSSOpts{}, ""},
		{"parent-hover under a ref compiles to nothing", g(sm{"base:parent-hover": {"op": "0.5"}}), &Node{ID: "r", T: "ref"}, CSSOpts{}, ""},
		{"order per breakpoint: plain, hover, focus, active, parent-hover", g(sm{
			"base:parent-hover": {"op": "0.1"}, "base:active": {"op": "0.2"}, "base:focus": {"op": "0.3"},
			"base:hover": {"op": "0.4"}, "base": {"op": "0.5"}, "md": {"op": "0.6"}, "md:hover": {"op": "0.7"}}), p, CSSOpts{},
			".hm-n-g{opacity:0.5;}.hm-n-g:hover{opacity:0.4;}.hm-n-g:focus-visible{opacity:0.3;}.hm-n-g:active{opacity:0.2;}.hm-n-p:hover .hm-n-g{opacity:0.1;}" +
				"@media (max-width:1024px){.hm-n-g{opacity:0.6;}}@media (max-width:1024px){.hm-n-g:hover{opacity:0.7;}}"},
		{"transform: hover ty keeps base rot, md cascades", g(sm{
			"base": {"rot": "10deg"}, "base:hover": {"ty": "-4px"}, "md": {"rot": "20deg"}, "md:hover": {"sc": "1.1"}}), nil, CSSOpts{},
			".hm-n-g{transform:rotate(10deg);}.hm-n-g:hover{transform:translateY(-4px) rotate(10deg);}" +
				"@media (max-width:1024px){.hm-n-g{transform:rotate(20deg);}}" +
				"@media (max-width:1024px){.hm-n-g:hover{transform:translateY(-4px) rotate(20deg) scale(1.1);}}"},
		{"hover keeps a narrower Normal value of its group", g(sm{
			"base": {"ty": "0"}, "md": {"rot": "5deg"}, "base:hover": {"ty": "-4px"}}), nil, CSSOpts{},
			".hm-n-g{transform:translateY(0);}.hm-n-g:hover{transform:translateY(-4px);}" +
				"@media (max-width:1024px){.hm-n-g{transform:translateY(0) rotate(5deg);}}" +
				"@media (max-width:1024px){.hm-n-g:hover{transform:translateY(-4px) rotate(5deg);}}"},
		{"hover grad keeps a narrower Normal image", g(sm{
			"md": {"bgimg": "/media/a.jpg"}, "base:hover": {"grad": "radial-fff-000"}}), nil, CSSOpts{},
			`.hm-n-g:hover{background-image:radial-gradient(#fff,#000);}@media (max-width:1024px){.hm-n-g{background-image:url("/media/a.jpg");}}` +
				`@media (max-width:1024px){.hm-n-g:hover{background-image:radial-gradient(#fff,#000),url("/media/a.jpg");}}`},
		{"filter and backdrop in a state", g(sm{
			"base": {"blur": "2px"}, "base:hover": {"bright": "120%", "bblur": "8px"}}), nil, CSSOpts{},
			".hm-n-g{filter:blur(2px);}.hm-n-g:hover{backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);filter:blur(2px) brightness(120%);}"},
		{"hover grad keeps the base image", g(sm{
			"base": {"bgimg": "/media/a.jpg"}, "base:hover": {"grad": "radial-fff-000"}}), nil, CSSOpts{},
			`.hm-n-g{background-image:url("/media/a.jpg");}.hm-n-g:hover{background-image:radial-gradient(#fff,#000),url("/media/a.jpg");}`},
		{"state map with a layout key is ignored", g(sm{"base:hover": {"display": "none", "op": "0.5"}}), nil, CSSOpts{},
			".hm-n-g:hover{opacity:0.5;}"},
		{"transition keys in a state are ignored", g(sm{"base:hover": {"trans": "all"}}), nil, CSSOpts{}, ""},
		{"force lists the forced twin", g(sm{"base:hover": {"op": "0.5"}, "base:focus": {"op": "0.4"}, "base:active": {"op": "0.3"}, "base:parent-hover": {"op": "0.2"}}), p, CSSOpts{Force: true},
			`.hm-n-g:hover,.hm-n-g[data-hm-force="hover"]{opacity:0.5;}` +
				`.hm-n-g:focus-visible,.hm-n-g[data-hm-force="focus"]{opacity:0.4;}` +
				`.hm-n-g:active,.hm-n-g[data-hm-force="active"]{opacity:0.3;}` +
				`.hm-n-p:hover .hm-n-g,.hm-n-p[data-hm-force="parent-hover"] .hm-n-g{opacity:0.2;}`},
	}
	for _, c := range cases {
		if got := under(c.n, c.parent, c.o); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestStatesNeverEmitForceWithoutOption(t *testing.T) {
	n := &Node{ID: "g", T: "container", S: sm{"base:hover": {"op": "0.5"}, "sm:focus": {"op": "0.5"}}}
	if css := NodeCSSWith(n, CSSOpts{Vars: true}); strings.Contains(css, "data-hm-force") {
		t.Errorf("css = %s", css)
	}
}

// A preset states selector is doubled so its hover beats the node's doubled
// base rule; the node's own hover ties with it and wins by source order.
func TestPresetStateGoldens(t *testing.T) {
	p := Preset{S: sm{"base": {"bg": "primary"}, "base:hover": {"bg": "accent"}, "sm:focus": {"bc": "accent"}}}
	want := ".hm-p-card{background-color:var(--hm-primary);}.hm-p-card.hm-p-card:hover{background-color:var(--hm-accent,#c8102e);}" +
		"@media (max-width:640px){.hm-p-card.hm-p-card:focus-visible{border-color:var(--hm-accent,#c8102e);}}"
	if got := PresetCSS("card", p, CSSOpts{}); got != want {
		t.Errorf("preset:\n got %s\nwant %s", got, want)
	}
	forced := PresetCSS("card", p, CSSOpts{Force: true})
	if !strings.Contains(forced, `.hm-p-card.hm-p-card:hover,.hm-p-card.hm-p-card[data-hm-force="hover"]{`) {
		t.Errorf("forced preset: %s", forced)
	}
	n := &Node{ID: "g", T: "container", P: map[string]any{"preset": "card"}, S: sm{"base": {"bg": "muted"}, "base:hover": {"bg": "surface"}}}
	got := NodeCSS(n)
	want = ".hm-n-g.hm-n-g{background-color:var(--hm-muted,#eef0f4);}.hm-n-g.hm-n-g:hover{background-color:var(--hm-surface,#fff);}"
	if got != want {
		t.Errorf("node with preset:\n got %s\nwant %s", got, want)
	}
}

func TestTreeCSSInPassesTheActualParent(t *testing.T) {
	doc := &Doc{V: 2, Nodes: []*Node{{ID: "outer", T: "container", C: []*Node{
		{ID: "inner", T: "container", S: sm{"base:parent-hover": {"op": "0.5"}}, C: []*Node{
			{ID: "leaf", T: "text", S: sm{"md:parent-hover": {"op": "0.2"}}}}}}}}}
	out := map[string]string{}
	TreeCSS(doc.Nodes, out)
	if out["inner"] != ".hm-n-outer:hover .hm-n-inner{opacity:0.5;}" {
		t.Errorf("inner = %q", out["inner"])
	}
	if out["leaf"] != "@media (max-width:1024px){.hm-n-inner:hover .hm-n-leaf{opacity:0.2;}}" {
		t.Errorf("leaf = %q", out["leaf"])
	}
	// A subtree compiled from a nested node needs the parent it sits under.
	inner, outer := doc.Find("inner")
	part := map[string]string{}
	TreeCSSIn([]*Node{inner}, outer, part, CSSOpts{})
	if part["inner"] != out["inner"] {
		t.Errorf("TreeCSSIn = %q", part["inner"])
	}
}
