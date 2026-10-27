package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/internal/builder"
)

// The StyleSpec v2 page: every new key at base, md and sm, plus a sticky
// header, a phone-only and a desktop-only element and a hero with image and
// gradient. It lives apart from testdata/widgets/all.json so the existing
// page_css_* metrics keep measuring old kinds only.
const styleGoldenDir = "testdata/styles"

func loadAllKeys(t *testing.T) *builder.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(styleGoldenDir, "allkeys.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := builder.Parse(raw)
	if err != nil {
		t.Fatalf("allkeys.json: %v", err)
	}
	return d
}

func styleGolden(t *testing.T, file, got string) {
	t.Helper()
	path := filepath.Join(styleGoldenDir, file)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if got != string(want) {
		t.Fatalf("golden %s mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", file, got, want)
	}
}

// The fixture uses every StyleSpec key that v2 added, so a new key cannot ship
// without appearing in the page and its CSS budget.
func TestAllKeysFixtureCoversEveryNewKey(t *testing.T) {
	have := map[string]bool{}
	var walk func([]*builder.Node)
	walk = func(ns []*builder.Node) {
		for _, n := range ns {
			for _, m := range n.S {
				for k := range m {
					have[k] = true
				}
			}
			walk(n.C)
		}
	}
	walk(loadAllKeys(t).Nodes)
	for _, e := range builder.StyleSpecTable() {
		if !have[e.Key] && !oldStyleKey[e.Key] {
			t.Errorf("allkeys.json lacks style key %s", e.Key)
		}
	}
}

// oldStyleKey are the keys StyleSpec had before v2 (all.json covers them).
var oldStyleKey = map[string]bool{
	"pt": true, "pb": true, "pl": true, "pr": true, "gap": true, "bg": true, "color": true, "ai": true,
	"jc": true, "wrap": true, "as": true, "hide": true, "cols": true, "rows": true, "span": true, "maxw": true,
	"w": true, "minh": true, "radius": true, "fs": true, "shadow": true,
}

func TestAllKeysPagePublic(t *testing.T) {
	d := loadAllKeys(t)
	page := renderDoc(t, nil, d, false)
	pub := mainInner(t, page)
	if strings.Contains(pub, " style=") || strings.Contains(pub, "data-hm-") {
		t.Error("public all-keys page carries an inline style or builder marker")
	}
	css := styleBody(t, page)
	styleGolden(t, "allkeys.public.html", pub)
	styleGolden(t, "allkeys.public.css", css+"\n")
	for _, want := range []string{
		"border-bottom-width:1px;display:flex;", // sticky header
		"position:sticky;", "top:0;z-index:10;",
		".hm-n-k3{display:none;text-align:center;}@media (max-width:640px){.hm-n-k3{background-color:", // phone-only banner
		"@media (max-width:1024px){.hm-n-k4{display:none;}}",                                           // desktop-only note
		`background-image:linear-gradient(180deg,#000,var(--hm-accent,#c8102e)),url("/media/a.jpg");`,  // hero
		`background-image:radial-gradient(#000,var(--hm-primary)),url("/media/a.jpg");`,                // sm grad keeps the image
	} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q", want)
		}
	}
}

func TestBudgetAllKeysPageCSS(t *testing.T) {
	css := styleBody(t, renderDoc(t, nil, loadAllKeys(t), false))
	gz := gzipLen(t, css)
	t.Logf("all-keys page css: %d bytes, %d gzip (budget %d)", len(css), gz, pageCSSBudget)
	if gz > pageCSSBudget {
		t.Errorf("all-keys css %d B gzip > %d", gz, pageCSSBudget)
	}
	budgetfile.Check(t, "render", "page_css_allstylekeys_gzip", int64(gz), budgetfile.Metric{Unit: "bytes", Cap: pageCSSBudget, TolAbs: 1024})
}
