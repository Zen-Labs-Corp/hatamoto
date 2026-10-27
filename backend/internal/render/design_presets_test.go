package render

import (
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

func cardPreset() builder.Preset {
	return builder.Preset{Label: "Card", S: map[string]map[string]string{
		"base": {"pt": "space-4", "bg": "surface"},
		"sm":   {"pt": "space-2"},
	}}
}

func presetDoc(n int) *builder.Doc {
	d := &builder.Doc{V: 2}
	for i := 0; i < n; i++ {
		d.Nodes = append(d.Nodes, &builder.Node{
			ID: fmt.Sprintf("n%d", i), T: "container", P: map[string]any{"preset": "card"},
			S: map[string]map[string]string{"md": {"pt": "space-6"}},
		})
	}
	return d
}

func designWith(name string, p builder.Preset) builder.Design {
	return builder.Design{Presets: map[string]builder.Preset{name: p}}
}

func TestPresetCompiledOnceWhateverNodeCount(t *testing.T) {
	rule := builder.PresetCSS("card", cardPreset(), builder.CSSOpts{})
	out := renderDesign(t, nil, presetDoc(5), designWith("card", cardPreset()), false)
	if got := strings.Count(out, rule); got != 1 {
		t.Fatalf("preset rules appear %d times", got)
	}
	if got := strings.Count(out, `class="hm-n hm-c hm-p-card hm-n-n`); got != 5 {
		t.Fatalf("%d nodes carry the preset class", got)
	}
	// preset rules come before node rules, and node rules double the selector
	pi, ni := strings.Index(out, ".hm-p-card{"), strings.Index(out, ".hm-n-n0.hm-n-n0{")
	if pi < 0 || ni < 0 || pi > ni {
		t.Fatalf("cascade order wrong: preset at %d, node at %d", pi, ni)
	}
	if !strings.Contains(out, "@media (max-width:1024px){.hm-n-n0.hm-n-n0{padding-top:var(--hm-space-6,1.5rem);}}") {
		t.Fatal("node md override missing the doubled selector")
	}
}

func TestPresetNeedsNoOptIn(t *testing.T) {
	out := renderDesign(t, nil, presetDoc(1), designWith("card", cardPreset()), false)
	if !strings.Contains(out, ".hm-p-card{") {
		t.Fatal("preset not emitted on a theme without the token opt-in")
	}
}

func TestUnusedPresetOnlyInCanvas(t *testing.T) {
	d := builder.Design{Presets: map[string]builder.Preset{"card": cardPreset(), "other": cardPreset()}}
	pub := renderDesign(t, nil, presetDoc(1), d, false)
	if strings.Contains(pub, ".hm-p-other{") || !strings.Contains(pub, ".hm-p-card{") {
		t.Fatal("public page must carry exactly the used presets")
	}
	canvas := renderDesign(t, nil, presetDoc(1), d, true)
	base := canvas[strings.Index(canvas, `id="hm-builder-base"`):]
	base = base[:strings.Index(base, "</style>")]
	if !strings.Contains(base, ".hm-p-other{") || !strings.Contains(base, ".hm-p-card{") {
		t.Fatal("canvas base must carry every preset")
	}
}

func TestPresetsAbsentWhenNoNativeNode(t *testing.T) {
	d := builder.FromSections([]builder.Section{{ID: "a", Type: "content", Content: map[string]any{"body": "<p>x</p>"}}})
	if out := renderDesign(t, nil, d, designWith("card", cardPreset()), false); strings.Contains(out, "hm-builder") {
		t.Fatal("v1-only page got builder css")
	}
}

func TestMissingPresetDeadClassNoRules(t *testing.T) {
	out := renderDesign(t, nil, presetDoc(1), builder.Design{}, false)
	if !strings.Contains(out, "hm-p-card") || strings.Contains(out, ".hm-p-card{") {
		t.Fatalf("missing preset: want dead class and no rules")
	}
	out = renderDesign(t, nil, presetDoc(1), designWith("other", cardPreset()), false)
	if strings.Contains(out, ".hm-p-other{") {
		t.Fatal("unreferenced preset emitted")
	}
}

func TestPresetChangeChangesOutputWithoutDocChange(t *testing.T) {
	doc := presetDoc(1)
	a := renderDesign(t, nil, doc, designWith("card", cardPreset()), false)
	p := cardPreset()
	p.S["base"]["pt"] = "space-8"
	b := renderDesign(t, nil, doc, designWith("card", p), false)
	if a == b || !strings.Contains(b, "padding-top:var(--hm-space-8,2.5rem)") {
		t.Fatal("preset edit did not reach the page")
	}
}

func TestHostilePresetDropped(t *testing.T) {
	p := builder.Preset{S: map[string]map[string]string{"base": {"bg": "red;}</style><script>alert(1)</script>"}}}
	out := renderDesign(t, nil, presetDoc(1), designWith("card", p), false)
	if strings.Contains(out, "<script>alert") || strings.Contains(out, ".hm-p-card{") {
		t.Fatalf("hostile preset emitted:\n%s", out)
	}
	// a half-valid preset is dropped whole, never partially emitted
	p = builder.Preset{S: map[string]map[string]string{"base": {"pt": "space-2", "bg": "red;}"}}}
	if out := renderDesign(t, nil, presetDoc(1), designWith("card", p), false); strings.Contains(out, ".hm-p-card{") {
		t.Fatal("invalid preset partially emitted")
	}
}

func TestComponentNodeIgnoresPreset(t *testing.T) {
	d := &builder.Doc{V: 2, Nodes: []*builder.Node{{ID: "c", T: "container", C: []*builder.Node{
		{ID: "k", T: "component", P: map[string]any{"type": "content", "preset": "card", "content": map[string]any{"body": "<p>b</p>"}}, S: map[string]map[string]string{"base": {"pt": "space-1"}}},
	}}}}
	out := renderDesign(t, nil, d, designWith("card", cardPreset()), false)
	if strings.Contains(out, "hm-p-card") || strings.Contains(out, ".hm-p-card{") || strings.Contains(out, ".hm-n-k.hm-n-k") {
		t.Fatal("component honoured a preset")
	}
}

func TestPresetClassOnWidgets(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	var tag func(ns []*builder.Node)
	tag = func(ns []*builder.Node) {
		for _, n := range ns {
			if n.T != "component" {
				if n.P == nil {
					n.P = map[string]any{}
				}
				n.P["preset"] = "card"
			}
			tag(n.C)
		}
	}
	tag(d.Nodes)
	out := renderDesign(t, nil, d, designWith("card", cardPreset()), false)
	for kind := range kindSet(d) {
		if kind == "component" {
			continue
		}
		if !strings.Contains(out, "hm-p-card") {
			t.Fatalf("no preset class for %s", kind)
		}
	}
	if strings.Count(out, builder.PresetCSS("card", cardPreset(), builder.CSSOpts{})) != 1 {
		t.Fatal("preset rules not emitted once")
	}
}

func kindSet(d *builder.Doc) map[string]bool {
	m := map[string]bool{}
	kindsIn(d.Nodes, m)
	return m
}

// ISC-20: all-widgets page, tokens on, eight presets: builder css stays within budget.
func TestBudgetAllWidgetsTokensPresetsCSS(t *testing.T) {
	d := loadWidgetDoc(t, "all")
	design := builder.Design{Tokens: builder.DefaultTokens(), Presets: map[string]builder.Preset{}}
	design.Tokens.Color["accent"] = "#112233"
	design.Tokens.Space["4"] = "1.25rem"
	design.Tokens.FS["lg"] = "1.4rem"
	design.Tokens.Shadow["md"] = "0 2px 6px rgba(0,0,0,.2)"
	names := make([]string, 8)
	for i := range names {
		names[i] = fmt.Sprintf("preset-%d", i)
		design.Presets[names[i]] = builder.Preset{Label: names[i], S: map[string]map[string]string{
			"base": {"pt": "space-4", "pb": "space-4", "bg": "surface", "radius": "md", "shadow": "sm", "fs": "md"},
			"md":   {"pt": "space-3", "pb": "space-3"},
			"sm":   {"pt": "space-2", "pb": "space-2", "shadow": "none"},
		}}
	}
	i := 0
	var tag func(ns []*builder.Node)
	tag = func(ns []*builder.Node) {
		for _, n := range ns {
			if n.T != "component" && i < 16 {
				if n.P == nil {
					n.P = map[string]any{}
				}
				n.P["preset"] = names[i%8]
				i++
			}
			tag(n.C)
		}
	}
	tag(d.Nodes)
	css := styleBody(t, renderDesign(t, optInTheme(), d, design, false))
	gz := gzipLen(t, css)
	t.Logf("all-widgets + tokens + 8 presets css: %d bytes, %d gzip (budget %d)", len(css), gz, pageCSSBudget)
	if gz > pageCSSBudget {
		t.Errorf("builder css %d B gzip > %d", gz, pageCSSBudget)
	}
	budgetfile.Check(t, "render", "page_css_tokens_presets_gzip", int64(gz), budgetfile.Metric{Unit: "bytes", Cap: pageCSSBudget, TolAbs: 1024})
	for _, n := range names {
		if got := strings.Count(css, builder.PresetCSS(n, design.Presets[n], builder.CSSOpts{Vars: true})); got != 1 {
			t.Errorf("preset %s emitted %d times", n, got)
		}
	}
}
