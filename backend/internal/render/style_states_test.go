package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

// statesDoc is a hover-lift card (shadow, transition, hover lift), a preset
// with a hover state and a child that reacts to its parent's hover.
func statesDoc() (*builder.Doc, builder.Design) {
	s := func(m map[string]map[string]string) map[string]map[string]string { return m }
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{
		ID: "row", T: "container", C: []*builder.Node{
			{ID: "card", T: "container", S: s(map[string]map[string]string{
				"base":       {"shadow": "sm", "trans": "transform-shadow", "tdur": "200", "tease": "ease-out"},
				"base:hover": {"ty": "-4px", "shadow": "lg"},
				"md:focus":   {"bc": "accent"},
				"sm:active":  {"sc": "0.98"},
			}), C: []*builder.Node{
				{ID: "label", T: "text", P: map[string]any{"text": "Hi"}, S: s(map[string]map[string]string{"base:parent-hover": {"color": "accent"}})},
			}},
			{ID: "tile", T: "container", P: map[string]any{"preset": "lift"}, S: s(map[string]map[string]string{"base": {"bg": "muted"}, "base:hover": {"bg": "surface"}})},
		},
	}}}
	design := builder.Design{Presets: map[string]builder.Preset{"lift": {Label: "Lift", S: s(map[string]map[string]string{
		"base": {"bg": "surface"}, "base:hover": {"bg": "accent"}})}}}
	return d, design
}

func TestStatesCanvasHasForcedSelectorsPublicHasNone(t *testing.T) {
	d, design := statesDoc()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	pub := renderDesign(t, optInTheme(), d, design, false)
	if strings.Contains(pub, "data-hm-force") {
		t.Error("public render carries a forced selector")
	}
	css := styleBody(t, pub)
	for _, want := range []string{
		".hm-n-card:hover{box-shadow:", "transform:translateY(-4px);",
		"@media (max-width:1024px){.hm-n-card:focus-visible{border-color:",
		"@media (max-width:640px){.hm-n-card:active{transform:scale(0.98);}}",
		".hm-n-card:hover .hm-n-label{color:",
		".hm-p-lift.hm-p-lift:hover{background-color:",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("public css lacks %q:\n%s", want, css)
		}
	}
	for _, part := range strings.Split(css, "}")[:len(strings.Split(css, "}"))-1] {
		if strings.Contains(part, "transition-") && !strings.Contains(part, "prefers-reduced-motion:no-preference") {
			t.Errorf("transition outside reduced-motion media: %s", part)
		}
	}

	canvas := renderDesign(t, optInTheme(), d, design, true)
	for _, want := range []string{
		`.hm-n-card:hover,.hm-n-card[data-hm-force="hover"]{`,
		`.hm-n-card:focus-visible,.hm-n-card[data-hm-force="focus"]{`,
		`.hm-n-card:active,.hm-n-card[data-hm-force="active"]{`,
		`.hm-n-card:hover .hm-n-label,.hm-n-card[data-hm-force="parent-hover"] .hm-n-label{`,
		`.hm-p-lift.hm-p-lift:hover,.hm-p-lift.hm-p-lift[data-hm-force="hover"]{`,
		`.hm-n-tile.hm-n-tile:hover`, // node with a preset: doubled state selector too
	} {
		if !strings.Contains(canvas, want) {
			t.Errorf("canvas lacks %q", want)
		}
	}
}

// The canvas patch CSS of a nested node knows its parent and forces states.
func TestNodeCSSForNestedKnowsItsParent(t *testing.T) {
	d, _ := statesDoc()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	card, row := d.Find("card")
	css := e.NodeCSSFor(minimalView(), card, row)
	if !strings.Contains(css["card"], `.hm-n-card[data-hm-force="hover"]`) {
		t.Errorf("card css = %s", css["card"])
	}
	// label sits under card inside the subtree: its parent-hover names card.
	if !strings.Contains(css["label"], `.hm-n-card:hover .hm-n-label,`) {
		t.Errorf("label css = %s", css["label"])
	}
	label, card2 := d.Find("label")
	if got := e.NodeCSSFor(minimalView(), label, card2)["label"]; !strings.Contains(got, `.hm-n-card:hover .hm-n-label`) {
		t.Errorf("nested label css = %s", got)
	}
	if got := e.NodeCSS(label)["label"]; got != "" {
		t.Errorf("label without a parent compiled parent-hover: %s", got)
	}
}

const statesFixture = "states.json"

func loadStates(t *testing.T) *builder.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(styleGoldenDir, statesFixture))
	if err != nil {
		t.Fatal(err)
	}
	d, err := builder.Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", statesFixture, err)
	}
	return d
}

func statesFixtureDesign() builder.Design {
	return builder.Design{Presets: map[string]builder.Preset{"lift": {Label: "Lift", S: map[string]map[string]string{
		"base": {"bg": "surface", "radius": "md"}, "base:hover": {"bg": "accent", "ty": "-2px"}}}}}
}

// The states page: hover-lift cards, parent-hover children, a preset with a
// hover, a button with hover, focus and a composed transform. Public output
// has every transition inside the reduced-motion media block and no forced
// selector.
func TestStatesPagePublic(t *testing.T) {
	page := renderDesign(t, optInTheme(), loadStates(t), statesFixtureDesign(), false)
	if strings.Contains(page, "data-hm-force") || strings.Contains(page, " style=") {
		t.Error("public states page carries a forced selector or an inline style")
	}
	css := styleBody(t, page)
	styleGolden(t, "states.public.html", mainInner(t, page))
	styleGolden(t, "states.public.css", css+"\n")
	for _, want := range []string{
		".hm-n-c1:hover{box-shadow:", "transform:translateY(-4px);",
		".hm-n-c1:active{transform:translateY(0) scale(0.98);}",
		".hm-n-c1:hover .hm-n-c1t{color:",
		"@media (max-width:640px){.hm-n-c1:hover .hm-n-c1t{color:",
		".hm-n-b1:hover{background-color:var(--hm-primary);filter:brightness(110%);transform:rotate(-2deg);}",
		"@media (max-width:1024px){.hm-n-b1:hover{transform:rotate(-2deg) scale(1.05);}}",
		".hm-n-b1:focus-visible{",
		".hm-p-lift.hm-p-lift:hover{",
		".hm-n-c3.hm-n-c3:hover{",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("states css lacks %q", want)
		}
	}
	for _, rule := range strings.SplitAfter(css, "}}") {
		if i := strings.Index(rule, "transition-"); i >= 0 && !strings.Contains(rule[:i], "prefers-reduced-motion:no-preference") {
			t.Errorf("transition outside reduced-motion media: %s", rule)
		}
	}
}

func TestBudgetStatesPageCSS(t *testing.T) {
	css := styleBody(t, renderDesign(t, optInTheme(), loadStates(t), statesFixtureDesign(), false))
	gz := gzipLen(t, css)
	t.Logf("states page css: %d bytes, %d gzip (budget %d)", len(css), gz, pageCSSBudget)
	if gz > pageCSSBudget {
		t.Errorf("states css %d B gzip > %d", gz, pageCSSBudget)
	}
	budgetfile.Check(t, "render", "page_css_states_gzip", int64(gz), budgetfile.Metric{Unit: "bytes", Cap: pageCSSBudget, TolAbs: 1024})
}
