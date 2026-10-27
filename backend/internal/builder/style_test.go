package builder

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func styled(bp, k, v string) *Doc {
	return &Doc{V: 2, Nodes: []*Node{{ID: "a", T: "heading", S: map[string]map[string]string{bp: {k: v}}}}}
}

// Style values that could break out of a declaration, rule or <style> element.
var injections = []string{
	"expression(alert(1))",
	"red;}body{x:y",
	"red;}</style><script>alert(1)</script>",
	"</style>",
	"url(javascript:alert(1))",
	"url(//evil.example/x.png)",
	`\75rl(x)`,
	`\000075rl(x)`,
	"red !important",
	"#fff;",
	"#ggg",
	"var(--x)",
	"calc(1px+1px)",
	"1px\n",
	" 1px",
	"1e3px",
	"12345px",
	"",
	"\u0455pace-1",
	"space-99",
}

func TestStyleInjectionRejected(t *testing.T) {
	for _, key := range []string{"pt", "bg", "color", "maxw", "w", "minh", "radius", "fs", "gap", "cols", "dir", "ai", "hide"} {
		for _, v := range injections {
			if _, err := styleDecl(key, v); err == nil {
				t.Errorf("styleDecl(%q, %q) accepted", key, v)
			}
			err := styled("base", key, v).Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate accepted %s=%q: %v", key, v, err)
			}
		}
	}
}

func TestStyleValidateRejectsFromJSON(t *testing.T) {
	bad := []string{
		`{"v":2,"nodes":[{"id":"a","t":"heading","s":{"base":{"pt":"expression(alert(1))"}}}]}`,
		`{"v":2,"nodes":[{"id":"a","t":"heading","s":{"base":{"bg":"red;}body{x:y"}}}]}`,
		`{"v":2,"nodes":[{"id":"a","t":"heading","s":{"base":{"unknown":"space-1"}}}]}`,
		`{"v":2,"nodes":[{"id":"a","t":"heading","s":{"md":{"cols":"03"}}}]}`,
	}
	for _, s := range bad {
		if err := ValidateRaw([]byte(s)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted: %s (%v)", s, err)
		}
	}
}

func TestStyleAccepts(t *testing.T) {
	ok := map[string]string{
		"pt": "space-3", "bg": "#fff", "color": "primary", "maxw": "container", "w": "50%",
		"minh": "100vh", "radius": "md", "fs": "2xl", "gap": "12px", "cols": "3",
		"dir": "column", "ai": "center", "hide": "1", "ta": "right", "mt": "0",
	}
	for k, v := range ok {
		if err := styled("md", k, v).Validate(); err != nil {
			t.Errorf("%s=%s rejected: %v", k, v, err)
		}
	}
}

func TestNodeCSSSafeAndDeterministic(t *testing.T) {
	n := &Node{ID: "x", T: "text", S: map[string]map[string]string{
		"base": {"bg": "#fff", "pt": "space-2", "cols": "2"},
		"sm":   {"pt": "space-1"},
		"md":   {"pt": "space-3"},
	}}
	want := ".hm-n-x{background-color:#fff;grid-template-columns:repeat(2,minmax(0,1fr));padding-top:var(--hm-space-2,0.5rem);}" +
		"@media (max-width:1024px){.hm-n-x{padding-top:var(--hm-space-3,0.75rem);}}" +
		"@media (max-width:640px){.hm-n-x{padding-top:var(--hm-space-1,0.25rem);}}"
	for i := 0; i < 20; i++ {
		if got := NodeCSS(n); got != want {
			t.Fatalf("run %d:\n got %s\nwant %s", i, got, want)
		}
	}
	if strings.ContainsAny(want, "<>\"'\\") {
		t.Fatal("unsafe characters in compiled css")
	}
}

func TestNodeCSSNeverEmitsInvalid(t *testing.T) {
	n := &Node{ID: "x", T: "text", S: map[string]map[string]string{"base": {"pt": "red;}body{x:y", "bg": "#fff"}}}
	css := NodeCSS(n)
	if strings.Contains(css, "body") || !strings.Contains(css, "background-color:#fff") {
		t.Fatalf("css: %s", css)
	}
	if NodeCSS(&Node{ID: "x} y{", T: "text", S: map[string]map[string]string{"base": {"bg": "#fff"}}}) != "" {
		t.Fatal("emitted rule for an unsafe id")
	}
	if NodeCSS(&Node{ID: "x", T: "text"}) != "" {
		t.Fatal("css for unstyled node")
	}
}

func TestTreeCSS(t *testing.T) {
	d := &Doc{V: 2, Nodes: []*Node{
		{ID: "a", T: "container", S: map[string]map[string]string{"base": {"pt": "space-1"}}, C: []*Node{
			{ID: "b", T: "text", S: map[string]map[string]string{"base": {"bg": "primary"}}},
			{ID: "c", T: "text"},
		}},
	}}
	out := map[string]string{}
	TreeCSS(d.Nodes, out)
	if len(out) != 2 || out["a"] == "" || out["b"] == "" {
		t.Fatalf("%v", out)
	}
}

func TestStyleSpecTable(t *testing.T) {
	tbl := StyleSpecTable()
	if len(tbl) != len(styleKeys) {
		t.Fatalf("%d entries for %d keys", len(tbl), len(styleKeys))
	}
	for i := 1; i < len(tbl); i++ {
		if tbl[i-1].Key >= tbl[i].Key {
			t.Fatal("not sorted by key")
		}
	}
	raw, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back []StyleSpecEntry
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(tbl, back) {
		t.Fatalf("JSON round trip: %v", err)
	}
	// Every advertised token/enum value is accepted by the compiler.
	for _, e := range tbl {
		for tok := range e.Tokens {
			if _, err := styleDecl(e.Key, tok); err != nil {
				t.Errorf("%s token %s: %v", e.Key, tok, err)
			}
		}
		for v := range e.Enum {
			if _, err := styleDecl(e.Key, v); err != nil {
				t.Errorf("%s enum %s: %v", e.Key, v, err)
			}
		}
	}
	// Mutating the copy must not alter the compiler.
	for _, e := range tbl {
		if e.Tokens != nil {
			e.Tokens["injected"] = "x;}"
		}
	}
	if _, err := styleDecl("pt", "injected"); err == nil {
		t.Fatal("table copy aliases compiler state")
	}
}

func TestStyleCountAndNewKeys(t *testing.T) {
	good := []struct{ key, val, css string }{
		{"cols", "1", "grid-template-columns:repeat(1,minmax(0,1fr))"},
		{"cols", "12", "grid-template-columns:repeat(12,minmax(0,1fr))"},
		{"rows", "1", "grid-template-rows:repeat(1,auto)"},
		{"rows", "12", "grid-template-rows:repeat(12,auto)"},
		{"span", "1", "grid-column:span 1"},
		{"span", "12", "grid-column:span 12"},
		{"as", "start", "align-self:flex-start"},
		{"as", "center", "align-self:center"},
		{"as", "end", "align-self:flex-end"},
		{"as", "stretch", "align-self:stretch"},
		{"jc", "around", "justify-content:space-around"},
		{"jc", "evenly", "justify-content:space-evenly"},
		{"ai", "baseline", "align-items:baseline"},
	}
	for _, c := range good {
		ds, err := styleDecl(c.key, c.val)
		if err != nil || len(ds) != 1 || ds[0].prop+":"+ds[0].val != c.css {
			t.Errorf("%s=%s: %v, %v; want %s", c.key, c.val, ds, err, c.css)
		}
	}
	for _, key := range []string{"cols", "rows", "span"} {
		for _, v := range []string{"0", "13", "-1", "03", "1.5", "1e1", "+2", " 2", "2 ", "x", "", "2;}"} {
			if _, err := styleDecl(key, v); err == nil {
				t.Errorf("%s=%q accepted", key, v)
			}
		}
	}
	for _, c := range [][2]string{{"as", "baseline"}, {"as", "between"}, {"jc", "baseline"}, {"ai", "around"}, {"as", ""}} {
		if _, err := styleDecl(c[0], c[1]); err == nil {
			t.Errorf("%s=%q accepted", c[0], c[1])
		}
	}
}

func TestNodeCSSNewKeysPerBreakpoint(t *testing.T) {
	n := &Node{ID: "g", T: "container", S: map[string]map[string]string{
		"base": {"rows": "2", "span": "3", "as": "center"},
		"md":   {"hide": "1"},
		"sm":   {"cols": "1", "hide": "1"},
	}}
	want := ".hm-n-g{align-self:center;grid-template-rows:repeat(2,auto);grid-column:span 3;}" +
		"@media (max-width:1024px){.hm-n-g{display:none;}}" +
		"@media (max-width:640px){.hm-n-g{grid-template-columns:repeat(1,minmax(0,1fr));display:none;}}"
	if got := NodeCSS(n); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if err := (&Doc{V: 2, Nodes: []*Node{n}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStyleSpecTableCount(t *testing.T) {
	by := map[string]StyleSpecEntry{}
	for _, e := range StyleSpecTable() {
		by[e.Key] = e
	}
	for key, tpl := range map[string]string{"cols": "repeat(%d,minmax(0,1fr))", "rows": "repeat(%d,auto)", "span": "span %d"} {
		e := by[key]
		if e.Kind != "count" || e.Min == nil || e.Max == nil || *e.Min != 1 || *e.Max != 12 || e.Tpl != tpl {
			t.Errorf("%s = %+v", key, e)
		}
	}
	if len(by["as"].Enum) != 4 || by["jc"].Enum["evenly"] == "" || by["ai"].Enum["baseline"] == "" {
		t.Error("enum values missing from the table")
	}
}

// snapshot of NodeCSS taken before the design-system compile mode existed:
// default-mode bytes must never change (themes without the opt-in).
var nodeCSSSnapshot = []struct{ key, val, css string }{
	{"ai", "baseline", ".hm-n-a1{align-items:baseline;}"},
	{"as", "center", ".hm-n-a1{align-self:center;}"},
	{"bg", "accent", ".hm-n-a1{background-color:var(--hm-accent,#c8102e);}"},
	{"color", "accent", ".hm-n-a1{color:var(--hm-accent,#c8102e);}"},
	{"cols", "3", ".hm-n-a1{grid-template-columns:repeat(3,minmax(0,1fr));}"},
	{"dir", "column", ".hm-n-a1{flex-direction:column;}"},
	{"fs", "lg", ".hm-n-a1{font-size:1.25rem;}"},
	{"gap", "space-4", ".hm-n-a1{gap:var(--hm-space-4,1rem);}"},
	{"hide", "1", ".hm-n-a1{display:none;}"},
	{"jc", "around", ".hm-n-a1{justify-content:space-around;}"},
	{"maxw", "container", ".hm-n-a1{max-width:var(--hm-container,72rem);}"},
	{"mb", "space-4", ".hm-n-a1{margin-bottom:var(--hm-space-4,1rem);}"},
	{"minh", "container", ".hm-n-a1{min-height:var(--hm-container,72rem);}"},
	{"mt", "space-4", ".hm-n-a1{margin-top:var(--hm-space-4,1rem);}"},
	{"pb", "space-4", ".hm-n-a1{padding-bottom:var(--hm-space-4,1rem);}"},
	{"pl", "space-4", ".hm-n-a1{padding-left:var(--hm-space-4,1rem);}"},
	{"pr", "space-4", ".hm-n-a1{padding-right:var(--hm-space-4,1rem);}"},
	{"pt", "space-4", ".hm-n-a1{padding-top:var(--hm-space-4,1rem);}"},
	{"radius", "md", ".hm-n-a1{border-radius:var(--hm-radius-md);}"},
	{"rows", "3", ".hm-n-a1{grid-template-rows:repeat(3,auto);}"},
	{"span", "3", ".hm-n-a1{grid-column:span 3;}"},
	{"ta", "center", ".hm-n-a1{text-align:center;}"},
	{"w", "container", ".hm-n-a1{width:var(--hm-container,72rem);}"},
	{"wrap", "nowrap", ".hm-n-a1{flex-wrap:nowrap;}"},
}

func TestNodeCSSDefaultModeByteIdentical(t *testing.T) {
	for _, c := range nodeCSSSnapshot {
		n := &Node{ID: "a1", S: map[string]map[string]string{"base": {c.key: c.val}}}
		if got := NodeCSS(n); got != c.css {
			t.Errorf("NodeCSS %s=%s: got %s want %s", c.key, c.val, got, c.css)
		}
		if got := NodeCSSWith(n, CSSOpts{}); got != c.css {
			t.Errorf("NodeCSSWith %s=%s: got %s want %s", c.key, c.val, got, c.css)
		}
	}
	for _, c := range nodeCSSSnapshot {
		if styleKeys[c.key].kind == "fs" {
			continue
		}
		n := &Node{ID: "a1", S: map[string]map[string]string{"base": {c.key: c.val}}}
		if got := NodeCSSWith(n, CSSOpts{Vars: true}); got != c.css {
			t.Errorf("Vars changed %s: %s", c.key, got)
		}
	}
}

func TestNodeCSSVarsFontSize(t *testing.T) {
	n := &Node{ID: "a1", S: map[string]map[string]string{"base": {"fs": "md"}, "sm": {"fs": "1.1rem"}}}
	want := ".hm-n-a1{font-size:var(--hm-fs-md,1rem);}@media (max-width:640px){.hm-n-a1{font-size:1.1rem;}}"
	if got := NodeCSSWith(n, CSSOpts{Vars: true}); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if got := NodeCSS(n); strings.Contains(got, "--hm-fs") {
		t.Fatalf("default mode leaked a var: %s", got)
	}
	out := map[string]string{}
	TreeCSSWith([]*Node{{ID: "a1", S: n.S}}, out, CSSOpts{Vars: true})
	if out["a1"] != want {
		t.Fatalf("TreeCSSWith: %s", out["a1"])
	}
}

func TestShadowKey(t *testing.T) {
	for v, want := range map[string]string{
		"none": "box-shadow:none;",
		"md":   "box-shadow:var(--hm-shadow-md,0 4px 12px rgba(0,0,0,.1));",
	} {
		got := NodeCSS(&Node{ID: "a", S: map[string]map[string]string{"base": {"shadow": v}}})
		if got != ".hm-n-a{"+want+"}" {
			t.Errorf("shadow=%s: %s", v, got)
		}
	}
	for _, bad := range []string{"xl", "0 1px red", "none;", ""} {
		if _, err := styleDecl("shadow", bad); err == nil {
			t.Errorf("shadow %q accepted", bad)
		}
	}
}

func TestNodeCSSPresetDoublesSelector(t *testing.T) {
	s := map[string]map[string]string{"base": {"pt": "space-1"}, "md": {"pt": "space-2"}}
	with := NodeCSS(&Node{ID: "x", S: s, P: map[string]any{"preset": "card"}})
	if !strings.Contains(with, ".hm-n-x.hm-n-x{") || strings.Count(with, ".hm-n-x.hm-n-x{") != 2 {
		t.Fatalf("selector not doubled at every breakpoint: %s", with)
	}
	for _, p := range []any{nil, "Bad_Name", "", 3} {
		got := NodeCSS(&Node{ID: "x", S: s, P: map[string]any{"preset": p}})
		if strings.Contains(got, ".hm-n-x.hm-n-x") {
			t.Errorf("preset %v doubled the selector", p)
		}
	}
}

func TestPresetPropOnEveryKind(t *testing.T) {
	for name := range reg.Load().kinds {
		p := map[string]any{"preset": "card"}
		if name == "component" {
			p["type"] = "hero"
		}
		if name == "ref" {
			p["block"] = "11111111-2222-3333-4444-555555555555"
		}
		n := &Node{ID: "a", T: name, P: p}
		d := &Doc{V: 2, Nodes: []*Node{n}}
		k, _ := kindOf(name)
		if m := k.meta; m != nil && m.OnlyIn != "" {
			d.Nodes = []*Node{{ID: "host", T: m.OnlyIn, C: []*Node{n}}}
		}
		if err := d.Validate(); err != nil {
			t.Errorf("kind %s rejects preset: %v", name, err)
		}
		p["preset"] = "Bad_Name"
		if err := d.Validate(); err == nil {
			t.Errorf("kind %s accepts a bad preset name", name)
		}
	}
}

func TestPresetHiddenFromKindProps(t *testing.T) {
	for _, k := range Kinds() {
		for _, p := range k.Props {
			if p.Key == "preset" {
				t.Errorf("kind %s lists the preset prop", k.Kind)
			}
		}
	}
}

var updateOldGolden = flag.Bool("update-old-golden", false, "rewrite testdata/style_old_golden.txt (only ever from a commit that predates StyleSpec v2)")

// oldKeyValues lists, for each of the 25 keys that existed before StyleSpec v2,
// the values the golden covers: every token, a literal where one is allowed and
// every enum value that existed then. It is deliberately hard-coded so growing
// the table cannot move the baseline.
var oldKeyValues = map[string][]string{
	"pt":     {"space-0", "space-4", "space-12", "1.5rem", "0", "12px"},
	"pb":     {"space-1", "space-6", "2rem"},
	"pl":     {"space-2", "space-8", "5%"},
	"pr":     {"space-3", "space-10", "1em"},
	"mt":     {"space-5", "space-7", "10vh"},
	"mb":     {"space-9", "space-11", "0.5rem"},
	"gap":    {"space-0", "space-4", "1.25rem"},
	"bg":     {"primary", "bg", "text", "surface", "muted", "accent", "#fff", "#c8102e", "#11223344"},
	"color":  {"primary", "text", "muted", "#000", "#abcdef"},
	"dir":    {"row", "column"},
	"ai":     {"start", "center", "end", "stretch", "baseline"},
	"jc":     {"start", "center", "end", "between", "around", "evenly"},
	"wrap":   {"wrap", "nowrap"},
	"ta":     {"left", "center", "right"},
	"as":     {"start", "center", "end", "stretch"},
	"hide":   {"1"},
	"cols":   {"1", "3", "12"},
	"rows":   {"1", "2", "12"},
	"span":   {"1", "3", "12"},
	"maxw":   {"container", "full", "40rem", "90%"},
	"w":      {"container", "full", "100%", "320px"},
	"minh":   {"container", "full", "50vh"},
	"radius": {"sm", "md", "lg", "full", "4px"},
	"fs":     {"xs", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl", "1.1rem"},
	"shadow": {"none", "sm", "md", "lg"},
}

// oldOutputSnapshot compiles the old-key corpus into one deterministic text:
// each key/value at base, md and sm, with and without a preset (doubled
// selector), with the type-scale vars, a mixed multi-key node per breakpoint
// and a PresetCSS rule.
func oldOutputSnapshot() string {
	keys := make([]string, 0, len(oldKeyValues))
	for k := range oldKeyValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	line := func(name, css string) { fmt.Fprintf(&b, "%s\t%s\n", name, css) }
	for _, k := range keys {
		for _, v := range oldKeyValues[k] {
			for _, bp := range breakpointOrder {
				s := map[string]map[string]string{bp: {k: v}}
				name := k + "=" + v + "@" + bp
				line(name, NodeCSSWith(&Node{ID: "g", T: "container", S: s}, CSSOpts{}))
				line(name+"+preset", NodeCSSWith(&Node{ID: "g", T: "container", S: s, P: map[string]any{"preset": "card"}}, CSSOpts{}))
				line(name+"+vars", NodeCSSWith(&Node{ID: "g", T: "container", S: s}, CSSOpts{Vars: true}))
			}
		}
	}
	mixed := map[string]map[string]string{"base": {}, "md": {}, "sm": {}}
	for i, k := range keys {
		vs := oldKeyValues[k]
		mixed["base"][k] = vs[i%len(vs)]
		mixed["md"][k] = vs[(i+1)%len(vs)]
		mixed["sm"][k] = vs[(i+2)%len(vs)]
	}
	line("mixed", NodeCSSWith(&Node{ID: "m", T: "container", S: mixed}, CSSOpts{}))
	line("mixed+preset", NodeCSSWith(&Node{ID: "m", T: "container", S: mixed, P: map[string]any{"preset": "card"}}, CSSOpts{}))
	line("mixed+vars", NodeCSSWith(&Node{ID: "m", T: "container", S: mixed}, CSSOpts{Vars: true}))
	line("preset", PresetCSS("card", Preset{Label: "Card", S: mixed}, CSSOpts{}))
	line("preset+vars", PresetCSS("card", Preset{Label: "Card", S: mixed}, CSSOpts{Vars: true}))
	return b.String()
}

// TestOldKindsByteIdenticalV2 pins the compiled output of every pre-v2 style
// key. testdata/style_old_golden.txt was captured from the commit before
// StyleSpec v2; documents that use no new key must compile to the same bytes.
func TestOldKindsByteIdenticalV2(t *testing.T) {
	const path = "testdata/style_old_golden.txt"
	got := oldOutputSnapshot()
	if *updateOldGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == string(want) {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if gl[i] != wl[i] {
			t.Fatalf("old output changed at line %d:\n got  %s\n want %s", i+1, gl[i], wl[i])
		}
	}
	t.Fatalf("old output changed: %d lines, want %d", len(gl), len(wl))
}

func TestImageStyleSplit(t *testing.T) {
	s := map[string]map[string]string{
		"base":       {"aspect": "1/1", "fit": "cover", "radius": "md", "mt": "4", "w": "50%"},
		"base:hover": {"radius": "lg"},
	}
	n := &Node{ID: "im", T: "image", P: map[string]any{"src": "/a.jpg"}, S: s}
	css := NodeCSSWith(n, CSSOpts{})
	for _, want := range []string{
		"width:50%", // the node element keeps width and the rest
		".hm-n-im img,img.hm-n-im{aspect-ratio:1 / 1;object-fit:cover;}", // picture: wrapped or bare
		".hm-n-im img{border-radius:var(--hm-radius-md);width:100%;}",    // rounds with the wrapper, fills it
		".hm-n-im:hover img{border-radius:",                              // the img fills a sized wrapper
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in %s", want, css)
		}
	}
	if strings.Contains(css, ".hm-n-im img:hover") {
		t.Errorf("state pseudo-class on the img: %s", css)
	}
	if strings.Contains(strings.SplitN(css, ".hm-n-im img", 2)[0], "aspect-ratio") {
		t.Errorf("aspect stayed on the node element: %s", css)
	}
	forced := NodeCSSWith(n, CSSOpts{Force: true})
	if !strings.Contains(forced, `.hm-n-im:hover img,.hm-n-im[data-hm-force="hover"] img{border-radius:`) {
		t.Errorf("forced state: %s", forced)
	}
}
