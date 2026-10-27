package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corePage mirrors the top level of testdata/homepage_core.json (the hatamoto.dev
// homepage built from core section types only).
type corePage struct {
	Title    string `json:"meta_title"`
	Sections []struct {
		ID        string         `json:"id"`
		Type      string         `json:"type"`
		Title     string         `json:"title"`
		Content   map[string]any `json:"content"`
		SortOrder int            `json:"sort_order"`
		IsVisible bool           `json:"isVisible"`
	} `json:"page_sections"`
}

func loadCorePage(t *testing.T) corePage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "homepage_core.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p corePage
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// declaredKeys is a schema's property names (and aliases) as a tree: name -> child
// keys for objects and arrays of objects.
type declaredKeys map[string]declaredKeys

func buildDeclaredKeys(n map[string]any) declaredKeys {
	out := declaredKeys{}
	props, _ := n["properties"].(map[string]any)
	for name, v := range props {
		pm, _ := v.(map[string]any)
		child := declaredKeys{}
		switch pm["type"] {
		case "object":
			child = buildDeclaredKeys(pm)
		case "array":
			if items, ok := pm["items"].(map[string]any); ok && items["type"] == "object" {
				child = buildDeclaredKeys(items)
			}
		}
		out[name] = child
		if aliases, ok := pm["x-hm-alias"].([]any); ok {
			for _, a := range aliases {
				out[fmt.Sprint(a)] = child
			}
		}
	}
	return out
}

func checkKeys(t *testing.T, where string, content any, node declaredKeys) {
	t.Helper()
	switch c := content.(type) {
	case map[string]any:
		for k, v := range c {
			child, ok := node[k]
			if !ok {
				t.Errorf("%s: key %q is not declared in the schema", where, k)
				continue
			}
			checkKeys(t, where+"."+k, v, child)
		}
	case []any:
		for i, v := range c {
			checkKeys(t, fmt.Sprintf("%s.%d", where, i), v, node)
		}
	}
}

// TestCoreHomepageRenders proves the homepage can be built from core section
// types alone: every type exists in the embedded theme, every content key is
// declared, and the page renders without a degraded section.
func TestCoreHomepageRenders(t *testing.T) {
	page := loadCorePage(t)
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	theme := e.current().theme

	v := minimalView()
	for _, s := range page.Sections {
		if s.Type == "html" {
			t.Errorf("section %s still uses type html", s.ID)
		}
		def, ok := theme.Sections[s.Type]
		if !ok {
			t.Errorf("section %s: type %q is not in the default theme", s.ID, s.Type)
			continue
		}
		var schema map[string]any
		if len(def.Schema) > 0 {
			if err := json.Unmarshal(def.Schema, &schema); err != nil {
				t.Fatalf("schema %s: %v", s.Type, err)
			}
		}
		checkKeys(t, s.ID, s.Content, buildDeclaredKeys(schema))
		v.Sections = append(v.Sections, Section{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, SortOrder: s.SortOrder})
	}
	if t.Failed() {
		return
	}

	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, bad := range []string{SectionFailedMarker, "unknown section type"} {
		if strings.Contains(out, bad) {
			t.Errorf("render contains %q", bad)
		}
	}
	if got := strings.Count(out, "data-hm-section="); got != 11 {
		t.Errorf("rendered %d sections, want 11", got)
	}
	// The prototype hero carries a code panel and a docker compose chip.
	for _, want := range []string{"hm-hero__chip", "docker compose up", "schemas / service.json", "hm-outs"} {
		if !strings.Contains(out, want) {
			t.Errorf("hero is missing %q", want)
		}
	}
}
