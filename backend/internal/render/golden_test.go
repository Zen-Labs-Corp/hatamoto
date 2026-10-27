package render

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sectionFixture is a testdata/fixtures/<type>.json file: one section rendered
// inside a minimal page, then compared on the <main> contents only so section
// goldens do not churn when the header/footer change.
type sectionFixture struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Content   map[string]any `json:"content"`
	SortOrder int            `json:"sort_order"`
	// Forms and Submitted populate the page-level data contact_form reads.
	Forms     map[string]FormView `json:"forms"`
	Submitted bool                `json:"submitted"`
	FormError bool                `json:"form_error"`
	// PluginDirectory is the registry data plugin_directory reads.
	PluginDirectory *PluginDirectoryView `json:"plugin_directory"`
	// ThemeDirectory is the registry data theme_directory reads.
	ThemeDirectory *ThemeDirectoryView `json:"theme_directory"`
}

func minimalView() *PageView {
	return &PageView{
		Site:    SiteView{Name: "Test Site"},
		Page:    PageMeta{Title: "Fixture"},
		BaseURL: "https://example.test",
		Path:    "/fixture",
	}
}

// mainInner returns the markup between <main ...> and </main>.
func mainInner(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, "<main ")
	j := strings.Index(html, "</main>")
	if i < 0 || j < i {
		t.Fatalf("no <main> in output:\n%s", html)
	}
	start := strings.Index(html[i:], ">") + i + 1
	return strings.TrimSpace(html[start:j]) + "\n"
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".html")
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
		t.Fatalf("golden %s mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestGolden renders every testdata/fixtures/*.json with the embedded theme.
// page-shell is a full PageView (whole document compared); every other
// fixture is a single section (only <main> compared). head.json belongs to
// TestGoldenHead.
func TestGolden(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()

	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "head" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if name == "page-shell" {
				var v PageView
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Fatal(err)
				}
				if err := e.RenderPage(&buf, &v); err != nil {
					t.Fatal(err)
				}
				// The fingerprinted bundle name changes with every CSS edit; keep the golden stable.
				out := strings.Replace(buf.String(), AssetPrefix+e.current().cssName, AssetPrefix+"theme.HASH.css", 1)
				compareGolden(t, name, out)
				return
			}
			var sf sectionFixture
			if err := json.Unmarshal(raw, &sf); err != nil {
				t.Fatal(err)
			}
			v := minimalView()
			v.Forms, v.Submitted, v.FormError = sf.Forms, sf.Submitted, sf.FormError
			v.PluginDirectory = sf.PluginDirectory
			v.ThemeDirectory = sf.ThemeDirectory
			v.Sections = []Section{{ID: sf.ID, Type: sf.Type, Title: sf.Title, Content: sf.Content, SortOrder: sf.SortOrder}}
			if err := e.RenderPage(&buf, v); err != nil {
				t.Fatal(err)
			}
			compareGolden(t, name, mainInner(t, buf.String()))
		})
	}
}

// Every richtext schema field must be marked data-hm-kind="richtext" in its
// section template (the documented marker contract).
func TestRichtextFieldsCarryKindMarker(t *testing.T) {
	th, err := LoadTheme(Chain{DefaultFS()})
	if err != nil {
		t.Fatal(err)
	}
	for typ, def := range th.Sections {
		if !strings.Contains(string(def.Schema), `"x-hm-field": "richtext"`) && !strings.Contains(string(def.Schema), `"x-hm-field":"richtext"`) {
			continue
		}
		if !strings.Contains(def.Template, `data-hm-kind="richtext"`) {
			t.Errorf("section %s has richtext fields but no data-hm-kind=\"richtext\" wrapper", typ)
		}
	}
}

// TestGoldenSchemaLayout renders a post-type entity (no sections) through a
// fixture theme's layouts/single-blog.html: Attrs, sanitisedHTML, formatDate and
// readingTime. The default theme is untouched.
func TestGoldenSchemaLayout(t *testing.T) {
	e, err := New(Options{Layers: []fs.FS{os.DirFS(filepath.Join("testdata", "themes", "blog"))}})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.EntityID = "e1"
	v.SchemaSlug = "blog"
	v.Attrs = map[string]any{
		"title":   "Hello <World>",
		"content": `<p data-hm-text="x">First paragraph.</p><script>alert(1)</script><p>Second one.</p>`,
	}
	v.PublishedAt = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(buf.String(), AssetPrefix+e.current().cssName, AssetPrefix+"theme.HASH.css", 1)
	compareGolden(t, "schema-layout-blog", mainInner(t, out))
}
