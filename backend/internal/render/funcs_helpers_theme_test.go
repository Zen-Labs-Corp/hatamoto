package render

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// helpersLayout calls every generic template helper once.
const helpersLayout = `[ex:{{excerpt "<p>one two three</p><p>four</p>" 12}}]` +
	`[in:{{initial "  jane"}}]` +
	`[blank:{{isBlankHTML "<p>&nbsp;</p>"}}/{{isBlankHTML "<p>x</p>"}}]` +
	`[toc:{{$t := toc "<h2>Intro</h2><h2>Intro</h2>"}}{{$t.html}}{{range $t.items}}({{.ID}}={{.Text}}){{end}}]` +
	`[group:{{range groupBy .Items "Category"}}{{.Key}}:{{range .Items}}{{.Title}},{{end}};{{end}}]` +
	`[sort:{{range sortBy .Items "Title"}}{{.Title}},{{end}}]` +
	`[pager:{{range pager .Pagination}}{{.Kind}}{{if .Page}}{{.Page}}{{end}}{{if .Current}}*{{end}} {{end}}]` +
	`[vid:{{videoID "https://youtu.be/abc123XYZ_-"}}/{{videoID "https://example.test/x"}}]` +
	`[pct:{{percent 2 3}}/{{percent 1 0}}]`

func TestHelpersInFixtureTheme(t *testing.T) {
	theme := fstest.MapFS{
		"theme.json":        mapFile(`{"name":"helpers","version":"1.0.0"}`),
		"layouts/base.html": mapFile(helpersLayout),
	}
	e, err := New(Options{Layers: []fs.FS{theme}})
	if err != nil {
		t.Fatal(err)
	}
	v := minimalView()
	v.Items = []RelatedItem{
		{Title: "beta", Category: "x"},
		{Title: "alpha", Category: "y"},
		{Title: "gamma", Category: "x"},
	}
	v.Pagination = &Pagination{Page: 2, TotalPages: 4, Path: "/list", PrevURL: "/list", NextURL: "/list?page=3"}
	var buf bytes.Buffer
	if err := e.RenderPage(&buf, v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"[ex:one two...]",
		"[in:J]",
		"[blank:true/false]",
		`[toc:<h2 id="intro">Intro</h2><h2 id="intro-2">Intro</h2>(intro=Intro)(intro-2=Intro)]`,
		"[group:x:beta,gamma,;y:alpha,;]",
		"[sort:alpha,beta,gamma,]",
		"[pager:prev1 page1 page2* page3 page4 next3 ]",
		"[vid:abc123XYZ_-/]",
		"[pct:67/0]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
