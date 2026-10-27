package render

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// inlineScriptIssues lists everything in page that would need script-src
// 'unsafe-inline': an inline executable <script>, an on*= attribute or a
// javascript: URL. Data blocks (JSON-LD, application/json) are not governed by
// script-src.
func inlineScriptIssues(page string) []string {
	var issues []string
	z := html.NewTokenizer(strings.NewReader(page))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return issues
		}
		if tt == html.EndTagToken {
			continue
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		attrs := map[string]string{}
		for _, a := range tok.Attr {
			attrs[a.Key] = a.Val
			if strings.HasPrefix(a.Key, "on") {
				issues = append(issues, "<"+tok.Data+"> has handler attribute "+a.Key)
			}
			switch a.Key {
			case "href", "src", "action", "formaction":
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
					issues = append(issues, "<"+tok.Data+" "+a.Key+"> is a javascript: URL")
				}
			}
		}
		if tok.Data != "script" {
			continue
		}
		if _, ok := attrs["src"]; ok {
			continue
		}
		switch attrs["type"] {
		case "application/ld+json", "application/json":
		default:
			issues = append(issues, "inline executable <script> (type="+attrs["type"]+")")
		}
	}
}

func TestInlineScriptIssuesDetects(t *testing.T) {
	bad := `<a href=" JavaScript:x()">a</a><p onclick="x()">b</p><script>x()</script>` +
		`<script type="application/ld+json">{}</script><script src="/a.js"></script>`
	if got := inlineScriptIssues(bad); len(got) != 3 {
		t.Errorf("issues = %v, want 3", got)
	}
}

// Strict-script CSP rests on this: the default theme (and core) emit no inline
// executable script, handler attribute or javascript: URL on public pages.
func TestDefaultThemePublicPagesHaveNoInlineScript(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !e.StrictScripts() {
		t.Fatal("default theme must opt in to strict scripts")
	}
	check := func(name, page string) {
		t.Helper()
		for _, is := range inlineScriptIssues(page) {
			t.Errorf("%s: %s", name, is)
		}
	}

	files, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures: %v (%d)", err, len(files))
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "head" {
			continue
		}
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
		} else {
			var sf sectionFixture
			if err := json.Unmarshal(raw, &sf); err != nil {
				t.Fatal(err)
			}
			v := minimalView()
			v.Forms, v.Submitted, v.FormError = sf.Forms, sf.Submitted, sf.FormError
			v.PluginDirectory, v.ThemeDirectory = sf.PluginDirectory, sf.ThemeDirectory
			v.Sections = []Section{{ID: sf.ID, Type: sf.Type, Title: sf.Title, Content: sf.Content, SortOrder: sf.SortOrder}}
			if err := e.RenderPage(&buf, v); err != nil {
				t.Fatal(err)
			}
		}
		check("fixture "+name, buf.String())
	}

	widgetDocs, _ := filepath.Glob(filepath.Join(widgetGoldenDir, "*.json"))
	for _, f := range widgetDocs {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		check("widget "+name, renderDocView(t, widgetFixtureView(name), loadWidgetDoc(t, name), false))
	}
	check("all widgets page", renderDoc(t, nil, loadWidgetDoc(t, "all"), false))
}
