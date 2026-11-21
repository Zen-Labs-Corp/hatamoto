package render

import (
	"slices"
	"strings"
	"testing"
)

var allRefusalKinds = []RefusalKind{
	RefusalRegistration, RefusalVet, RefusalEntry, RefusalCallGraph, RefusalRangeDepth, RefusalScripts,
	RefusalMarkup, RefusalOutputCap, RefusalMissingTemplate, RefusalParse, RefusalOther,
}

func TestExplainRefusalEveryKind(t *testing.T) {
	t.Parallel()
	const (
		capSentence = "Its output went over the 256 KiB limit in the test render."
		regSentence = "The plugin registered it with a name, type or source that is not allowed, or with a name already taken."
		vetSentence = "It defines or calls a template that does not belong to this plugin."
	)
	rows := []struct {
		raw      string
		kind     RefusalKind
		template string
		reason   string
	}{
		{`invalid plugin/name "acme"/"9x" (want [A-Za-z][A-Za-z0-9_]*; - is allowed in the plugin name)`, RefusalRegistration, "", regSentence},
		{`plugin partial acme/card: empty source`, RefusalRegistration, "partial card", regSentence},
		{`plugin section acme/Bad Type: bad type (want ^[a-z][a-z0-9_]*$) or empty source`, RefusalRegistration, "", regSentence},
		{`plugin partial plugin/acme/card already registered`, RefusalRegistration, "partial card", regSentence},
		{`section type "promo" already registered by plugin acme`, RefusalRegistration, "section promo", regSentence},
		{`all templates: sections/thief.html: defines "head", which is not a name of its own`, RefusalVet, "all templates", vetSentence},
		{`all templates: plugin/acme/card: partial name outside plugin/acme/`, RefusalVet, "all templates", vetSentence},
		{`all templates: sections/x.html: must define "section-x"`, RefusalEntry, "all templates", "It does not define its own section template."},
		{`section x: does not define its section template`, RefusalEntry, "section x", "It does not define its own section template."},
		{`all templates: templates: template "plugin/acme/a" calls itself, directly or through other templates`, RefusalCallGraph, "all templates",
			"Its {{template}} calls loop back on themselves or add up to more than 1000 calls."},
		{`section x: template "section-x" expands to more than 1000 template calls`, RefusalCallGraph, "section x",
			"Its {{template}} calls loop back on themselves or add up to more than 1000 calls."},
		{`section x: template "section-x" nests ranges deeper than 2, counting the templates it calls`, RefusalRangeDepth, "section x",
			"It nests {{range}} loops over the content more than 2 deep, counting the templates it calls."},
		{`section promo: markup: banned-element: template section-promo: <script> is not allowed (not allowed without site.scripts)`, RefusalScripts, "section promo",
			"It contains a script, and the plugin does not hold site.scripts."},
		{`partial plugin/acme/card-loop: markup: action-context: an action inside a tag name, an attribute name or an unquoted value is not allowed without site.scripts: outside text it may only sit inside a quoted attribute value (template plugin/acme/card-loop) (not allowed without site.scripts)`,
			RefusalMarkup, "partial card-loop",
			"It puts a template action where the markup rules do not allow one, such as inside a tag, a comment or a URL scheme."},
		{`section gallery-grid: output limit: sample render of section-gallery-grid: plugin template output is over the 256 KiB output limit`, RefusalOutputCap, "section gallery-grid", capSentence},
		{`section esc: html/template:sections/esc.html: no such template "x"`, RefusalMissingTemplate, "section esc", `Uses {{template "x"}} which this plugin does not define.`},
		{`all templates: sections/broken.html: template: sections/broken.html:1: missing value for if`, RefusalParse, "all templates", "It has a template syntax error."},
		{`something new`, RefusalOther, "", "It was left out when the plugin's templates were compiled."},
	}
	seen := map[RefusalKind]bool{}
	for _, r := range rows {
		got := ExplainRefusal(r.raw)
		seen[got.Kind] = true
		if got.Kind != r.kind || got.Template != r.template || got.Reason != r.reason || got.Raw != r.raw {
			t.Errorf("%q:\n got  %+v\n want kind=%s template=%q reason=%q", r.raw, got, r.kind, r.template, r.reason)
		}
	}
	for _, k := range allRefusalKinds {
		if !seen[k] {
			t.Errorf("no row for kind %q", k)
		}
		if refusalSentences[k] == "" && k != RefusalMissingTemplate {
			t.Errorf("kind %q has no sentence", k)
		}
	}
}

func TestExplainRefusalUnknownIsOther(t *testing.T) {
	t.Parallel()
	got := ExplainRefusal("something new")
	if got.Kind != RefusalOther || got.Template != "" || got.Raw != "something new" ||
		got.Reason != "It was left out when the plugin's templates were compiled." {
		t.Errorf("got %+v", got)
	}
	if ExplainRefusals(nil) != nil {
		t.Error("ExplainRefusals(nil) is not nil")
	}
	if got := ExplainRefusals([]string{"a", "b"}); len(got) != 2 || got[1].Raw != "b" {
		t.Errorf("ExplainRefusals = %+v", got)
	}
}

// The drift guard: raw strings come from the real producers, so a reworded
// producer fails here instead of degrading silently to "other".
func TestExplainRefusalMatchesProducers(t *testing.T) {
	t.Parallel()
	// gate runs withoutScripts and returns the one entry of the plugin.
	gate := func(t *testing.T, partials map[string]string, sections map[string]string) string {
		t.Helper()
		reg := newPluginRegistry()
		for n, src := range partials {
			if err := reg.registerPartial("acme", n, src); err != nil {
				t.Fatal(err)
			}
		}
		for typ, src := range sections {
			if err := reg.registerSection("acme", typ, src); err != nil {
				t.Fatal(err)
			}
		}
		_, why, graph := reg.snapshot().withoutScripts(nil)
		got := slices.Concat(why["acme"], graph["acme"])
		if len(got) == 0 {
			t.Fatal("the gate refused nothing")
		}
		return got[0]
	}
	// compiled builds an engine and returns the compile-time record.
	compiled := func(t *testing.T, partials map[string]string, sections map[string]string) string {
		t.Helper()
		reg := newPluginRegistry()
		for n, src := range partials {
			if err := reg.registerPartial("acme", n, src); err != nil {
				t.Fatal(err)
			}
		}
		for typ, src := range sections {
			if err := reg.registerSection("acme", typ, src); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := newEngine(Options{}, reg); err != nil {
			t.Fatal(err)
		}
		got := reg.templatesRefused("acme")
		if len(got) == 0 {
			t.Fatal("the compile refused nothing")
		}
		return got[0]
	}
	sec := func(typ, body string) map[string]string {
		return map[string]string{typ: `{{define "section-` + typ + `"}}` + body + `{{end}}`}
	}
	cases := []struct {
		name string
		raw  func(t *testing.T) string
		want RefusalKind
	}{
		{"registration", func(t *testing.T) string {
			err := newPluginRegistry().registerSection("acme", "Bad Type", "x")
			if err == nil {
				t.Fatal("accepted")
			}
			return strings.TrimPrefix(err.Error(), "render: ")
		}, RefusalRegistration},
		{"vet", func(t *testing.T) string {
			return compiled(t, nil, map[string]string{"thief": `{{define "section-thief"}}T{{end}}{{define "head"}}H{{end}}`})
		}, RefusalVet},
		{"entry", func(t *testing.T) string { return compiled(t, nil, map[string]string{"late": `<p>no entry</p>`}) }, RefusalEntry},
		{"call-graph", func(t *testing.T) string {
			return gate(t, map[string]string{"a": `{{template "plugin/acme/a"}}`}, sec("x", `{{template "plugin/acme/a"}}`))
		}, RefusalCallGraph},
		{"range-depth", func(t *testing.T) string {
			return gate(t, nil, sec("x", `{{range $.Content.items}}{{range $.Content.a}}{{range $.Content.b}}z{{end}}{{end}}{{end}}`))
		}, RefusalRangeDepth},
		{"scripts", func(t *testing.T) string { return gate(t, nil, sec("x", `<script>1</script>`)) }, RefusalScripts},
		{"markup", func(t *testing.T) string { return gate(t, nil, sec("x", `<p class={{.Settings.X}}>y</p>`)) }, RefusalMarkup},
		{"output-cap", func(t *testing.T) string {
			return gate(t, nil, sec("big", `{{range 30000}}<span class="cell">0123456789</span>{{end}}`))
		}, RefusalOutputCap},
		{"missing-template", func(t *testing.T) string {
			return compiled(t, nil, sec("esc", `{{template "plugin/acme/nope" .}}`))
		}, RefusalMissingTemplate},
		{"parse", func(t *testing.T) string { return compiled(t, nil, sec("broken", `{{if}}`)) }, RefusalParse},
	}
	seen := map[RefusalKind]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			raw := c.raw(t)
			if got := ExplainRefusal(raw); got.Kind != c.want {
				t.Errorf("%q classified %q, want %q", raw, got.Kind, c.want)
			}
		})
		seen[c.want] = true
	}
	for _, k := range allRefusalKinds {
		if k != RefusalOther && !seen[k] {
			t.Errorf("no producer case for kind %q", k)
		}
	}
}
