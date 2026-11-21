package designcheck

import (
	"strings"
	"testing"
	"testing/fstest"
)

func checkModule(t *testing.T, name, src string) []string {
	t.Helper()
	vs, err := CheckModule(fstest.MapFS{name: {Data: []byte(src)}})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vs {
		out = append(out, v.String())
	}
	return out
}

func wantRule(t *testing.T, got []string, rule, contains string) {
	t.Helper()
	for _, g := range got {
		if strings.Contains(g, " "+rule+" ") && strings.Contains(g, contains) {
			return
		}
	}
	t.Errorf("want a %s violation containing %q, got %q", rule, contains, got)
}

const modHead = `{{template "layouts/shell" .}}{{define "main"}}
`

func TestCheckModuleCleanPage(t *testing.T) {
	t.Parallel()
	src := modHead + `<form method="post" action="{{.Data.SaveURL}}">
{{template "hm/csrf" $}}
{{template "hm/card-start" (dict "ID" "example" "Title" "Example")}}
<p class="hm-muted">Some text</p>
{{template "hm/card-foot"}}
{{template "hm/button" (dict "Label" "Save" "Variant" "primary" "Type" "submit")}}
{{template "hm/card-foot-end"}}
</form>{{end}}
`
	if got := checkModule(t, "pages/example.html", src); len(got) != 0 {
		t.Errorf("clean page flagged: %q", got)
	}
}

func TestCheckModuleBespokeClass(t *testing.T) {
	t.Parallel()
	got := checkModule(t, "pages/x.html", modHead+"<p>ok</p>\n<div class=\"hm-muted example-fancy\">x</div>{{end}}\n")
	wantRule(t, got, "MOD-01", "pages/x.html:3 MOD-01")
	wantRule(t, got, "MOD-01", "example-fancy")
	for _, g := range got {
		if strings.Contains(g, `"hm-muted"`) {
			t.Errorf("core class flagged: %s", g)
		}
	}
}

func TestCheckModuleUndefinedCoreStyleClass(t *testing.T) {
	t.Parallel()
	got := checkModule(t, "pages/x.html", modHead+`<div class="hm-no-such-class">x</div>{{end}}`)
	wantRule(t, got, "MOD-01", "hm-no-such-class")
}

func TestCheckModuleInlineStyle(t *testing.T) {
	t.Parallel()
	wantRule(t, checkModule(t, "pages/x.html", modHead+`<p style="color:red">x</p>{{end}}`), "MOD-01", "style attribute")
	wantRule(t, checkModule(t, "pages/x.html", modHead+`<style>p{}</style>{{end}}`), "MOD-01", "<style>")
}

func TestCheckModuleHandRolledButton(t *testing.T) {
	t.Parallel()
	got := checkModule(t, "pages/x.html", modHead+`<button class="hm-btn hm-btn--primary" type="submit">Save</button>{{end}}`)
	wantRule(t, got, "UI-05", "")
}

func TestCheckModuleIgnoresActionsAndNonHTML(t *testing.T) {
	t.Parallel()
	src := modHead + `<div class="hm-muted {{.Data.Extra}} hm-btn--{{.Data.Kind}}">x</div>{{end}}`
	if got := checkModule(t, "pages/x.html", src); len(got) != 0 {
		t.Errorf("action-built class flagged: %q", got)
	}
	if got := checkModule(t, "pages/x.css", `.example-fancy { color: red }`); len(got) != 0 {
		t.Errorf("css file checked: %q", got)
	}
}
