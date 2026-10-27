package render

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestPluginRegFuncValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fn      any
		wantErr bool
	}{
		{"string", func() string { return "x" }, false},
		{"string+error", func(s string) (string, error) { return s, errors.New("e") }, false},
		{"int", func() int { return 1 }, false},
		{"float", func() float64 { return 1 }, false},
		{"bool", func() bool { return true }, false},
		{"[]string", func() []string { return nil }, false},
		{"map", func() map[string]int { return nil }, false},
		{"template.HTML", func() template.HTML { return "<b>" }, true},
		{"template.JS", func() template.JS { return "x" }, true},
		{"template.URL", func() template.URL { return "x" }, true},
		{"template.CSS", func() template.CSS { return "x" }, true},
		{"template.HTMLAttr", func() template.HTMLAttr { return "x" }, true},
		{"[]template.HTML", func() []template.HTML { return nil }, true},
		{"map of HTML", func() map[string]template.HTML { return nil }, true},
		{"(HTML, error)", func() (template.HTML, error) { return "", nil }, true},
		{"any", func() any { return template.HTML("x") }, true},
		{"struct", func() struct{} { return struct{}{} }, true},
		{"no return", func() {}, true},
		{"bad second return", func() (string, string) { return "", "" }, true},
		{"map int key", func() map[int]string { return nil }, true},
		{"not a func", "x", true},
		{"nil", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newPluginRegistry()
			err := r.registerFunc("p", "f", tt.fn)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			funcs := r.snapshot().funcs
			if _, ok := funcs["plugin_p_f"]; ok == tt.wantErr {
				t.Fatalf("registered=%v with wantErr=%v", ok, tt.wantErr)
			}
		})
	}
}

func TestPluginRegNamesAndDuplicates(t *testing.T) {
	t.Parallel()
	r := newPluginRegistry()
	if err := r.registerFunc("acme", "hello", func() string { return "hi" }); err != nil {
		t.Fatal(err)
	}
	if err := r.registerFunc("acme", "hello", func() string { return "hi" }); err == nil {
		t.Fatal("duplicate func must fail")
	}
	for _, bad := range [][2]string{{"", "x"}, {"a b", "x"}, {"-a", "x"}, {"a", "1x"}, {"a", "x y"}} {
		if err := r.registerFunc(bad[0], bad[1], func() string { return "" }); err == nil {
			t.Errorf("name %v should be rejected", bad)
		}
		if err := r.registerPartial(bad[0], bad[1], "x"); err == nil {
			t.Errorf("partial name %v should be rejected", bad)
		}
	}
	if err := r.registerPartial("acme", "card", "<p>{{.}}</p>"); err != nil {
		t.Fatal(err)
	}
	if err := r.registerPartial("acme", "card", "x"); err == nil {
		t.Fatal("duplicate partial must fail")
	}
	if err := r.registerPartial("acme", "empty", ""); err == nil {
		t.Fatal("empty partial must fail")
	}
	snap := r.snapshot()
	funcs, partials := snap.funcs, snap.partials
	if _, ok := funcs["plugin_acme_hello"]; !ok {
		t.Fatalf("funcs: %v", funcs)
	}
	if _, ok := partials["plugin/acme/card"]; !ok {
		t.Fatalf("partials: %v", partials)
	}
	// Snapshot is a copy.
	delete(funcs, "plugin_acme_hello")
	if f2 := r.snapshot().funcs; len(f2) != 1 {
		t.Fatal("snapshot must be a copy")
	}
}

func TestPluginRegFuncOutputIsEscaped(t *testing.T) {
	t.Parallel()
	r := newPluginRegistry()
	if err := r.registerFunc("t", "x", func() string { return `<script>alert(1)</script>` }); err != nil {
		t.Fatal(err)
	}
	funcs := r.snapshot().funcs
	tpl := template.Must(template.New("t").Funcs(funcs).Parse(`<p>{{plugin_t_x}}</p>`))
	var b bytes.Buffer
	if err := tpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != `<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>` {
		t.Fatalf("got %s", b.String())
	}
}

func TestPluginRegKebabNameNormalised(t *testing.T) {
	t.Parallel()
	r := newPluginRegistry()
	if err := r.registerFunc("acme-seo", "badge", func() string { return "x" }); err != nil {
		t.Fatal(err)
	}
	if err := r.registerPartial("acme-seo", "card", "<b>x</b>"); err != nil {
		t.Fatal(err)
	}
	snap := r.snapshot()
	funcs, partials := snap.funcs, snap.partials
	if _, ok := funcs["plugin_acme_seo_badge"]; !ok {
		t.Errorf("funcs = %v", funcs)
	}
	if _, ok := partials["plugin/acme_seo/card"]; !ok {
		t.Errorf("partials = %v", partials)
	}
	if err := r.registerFunc("acme-seo", "bad-name", func() string { return "" }); err == nil {
		t.Error("entry names stay strict")
	}
}

func TestPluginSectionPackAndRemove(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	if err := reg.registerFunc("acme", "badge", func() string { return "x" }); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerPartial("acme", "card", "<p>{{.}}</p>"); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerSection("acme", "promo", `{{define "section-promo"}}x{{end}}`); err != nil {
		t.Fatal(err)
	}
	gen := reg.size()
	if err := reg.registerSection("other", "promo", "y"); err == nil {
		t.Fatal("a section type already taken by a plugin must be refused")
	}
	for _, bad := range []string{"Promo", "1x", "a-b", ""} {
		if err := reg.registerSection("acme", bad, "x"); err == nil {
			t.Errorf("section type %q accepted", bad)
		}
	}
	if reg.size() != gen {
		t.Fatal("refused registrations must not change the registry")
	}
	if got := reg.snapshot().sections; len(got) != 1 || got["promo"] == "" {
		t.Fatalf("sections = %v", got)
	}

	// Removal is by owner: another plugin's entries stay, and the change is
	// visible to the engine's change detector.
	if err := reg.registerFunc("acme-two", "badge", func() string { return "y" }); err != nil {
		t.Fatal(err)
	}
	before := reg.size()
	reg.removePlugin("acme")
	if reg.size() == before {
		t.Fatal("removing a plugin must change the generation")
	}
	snap := reg.snapshot()
	funcs, partials := snap.funcs, snap.partials
	if _, ok := funcs["plugin_acme_badge"]; ok {
		t.Fatal("func survived removal")
	}
	if _, ok := funcs["plugin_acme_two_badge"]; !ok {
		t.Fatal("removal took another plugin's func")
	}
	if len(partials) != 0 || len(reg.snapshot().sections) != 0 {
		t.Fatalf("partials=%v sections=%v", partials, reg.snapshot().sections)
	}
	if err := reg.registerSection("other", "promo", "y"); err != nil {
		t.Fatalf("the type must be free again: %v", err)
	}
}

// Plugin templates may only define names of their own. A partial that defines
// "head" (or any theme template) is rejected, and the theme renders as before.
func TestPluginPartialCannotRedefineThemeTemplates(t *testing.T) {
	t.Parallel()
	plain := renderToString(t, newTestEngine(t, Options{}), minimalView())

	var reg *pluginRegistry
	for _, src := range []string{
		`{{define "head"}}<script>alert(document.cookie)</script>{{end}}`,
		`<b>ok</b>{{define "base"}}X{{end}}`,
		`{{block "footer" .}}<script>x</script>{{end}}`,
		`{{define "plugin/other/card"}}impersonating another plugin{{end}}`,
	} {
		reg = newPluginRegistry()
		if err := reg.registerPartial("evil", "card", src); err != nil {
			t.Fatal(err)
		}
		// A second, honest partial of the same plugin goes too: all or nothing.
		if err := reg.registerPartial("evil", "honest", `<i>fine</i>`); err != nil {
			t.Fatal(err)
		}
		e, err := newEngine(Options{}, reg)
		if err != nil {
			t.Fatalf("%q: the theme must compile: %v", src, err)
		}
		if out := renderToString(t, e, minimalView()); out != plain || strings.Contains(out, "alert(") {
			t.Fatalf("%q changed the page:\n%s", src, out)
		}
		if e.cur.layouts == nil || e.cur.theme == nil {
			t.Fatal("compiled set missing")
		}
	}

	// A well-behaved partial still works, and may define helpers under its prefix.
	reg = newPluginRegistry()
	if err := reg.registerPartial("good-one", "card", `{{define "plugin/good_one/helper"}}H{{end}}<i>{{template "plugin/good_one/helper"}}</i>`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if e.cur == nil {
		t.Fatal("no compiled set")
	}
	if _, ok := reg.snapshot().partials["plugin/good_one/card"]; !ok {
		t.Fatal("partial not registered")
	}
	if e.cur.pluginSize != reg.size() {
		t.Fatal("not compiled against the registry")
	}
}

// A plugin section may only define section-<its type> (a type the theme does
// not have) and names of its own; a broken one never stops the theme or other
// plugins' sections, and is not recompiled on every request.
func TestPluginSectionIsolation(t *testing.T) {
	t.Parallel()
	view := func() *PageView {
		v := minimalView()
		v.Sections = []Section{{ID: "a", Type: "promo", Content: map[string]any{}}, {ID: "b", Type: "hero", Content: map[string]any{"headline": "THEME-HERO"}}}
		return v
	}
	reg := newPluginRegistry()
	for typ, src := range map[string]string{
		"promo":  `{{define "section-promo"}}<p>PROMO</p>{{end}}`,
		"broken": `{{define "section-broken"}}{{if}}{{end}}`,                       // syntax error
		"nodef":  `<p>no define</p>`,                                               // does not define its entry
		"thief":  `{{define "section-thief"}}T{{end}}{{define "head"}}HEAD{{end}}`, // redefines a theme template
	} {
		owner := "good"
		if typ != "promo" {
			owner = "bad-" + typ
		}
		if err := reg.registerSection(owner, typ, src); err != nil {
			t.Fatal(err)
		}
	}
	// A plugin section cannot replace a theme section of the same type.
	if err := reg.registerSection("squatter", "hero", `{{define "section-hero"}}PLUGIN-HERO{{end}}`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatalf("a bad plugin section must not stop the theme compiling: %v", err)
	}
	out := renderToString(t, e, view())
	if !strings.Contains(out, "PROMO") || !strings.Contains(out, "THEME-HERO") || strings.Contains(out, "PLUGIN-HERO") || strings.Contains(out, "HEAD<") {
		t.Fatalf("sections:\n%s", mainInner(t, out))
	}
	// The rebuild is cached: nothing changed, the same compiled set serves.
	c1 := e.current()
	if e.current() != c1 {
		t.Fatal("the compiled set was rebuilt without a change")
	}
}

type countingHandler struct {
	slog.Handler
	errors *int
}

func (h countingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		*h.errors++
	}
	return nil
}

// When a rebuild fails (here: the theme went bad while a plugin changed) the
// previous version keeps serving and nothing recompiles per request.
func TestFailedRebuildIsNotRetriedPerRequest(t *testing.T) {
	t.Parallel()
	reg := newPluginRegistry()
	dir := t.TempDir()
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}ok{{end}}`)
	var errs int
	h := countingHandler{Handler: slog.NewTextHandler(io.Discard, nil), errors: &errs}
	e, err := newEngine(Options{Layers: []fs.FS{os.DirFS(dir)}, Logger: slog.New(h)}, reg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "partials/scripts.html", `{{define "scripts"}}{{if}}{{end}}`) // broken theme
	if err := reg.registerPartial("p", "x", "x"); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		e.current()
	}
	if errs != 1 {
		t.Fatalf("the failed rebuild was attempted %d times for one change", errs)
	}
	if err := reg.registerPartial("p", "y", "y"); err != nil { // a new change retries once
		t.Fatal(err)
	}
	for range 5 {
		e.current()
	}
	if errs != 2 {
		t.Fatalf("attempts = %d, want one more after a new change", errs)
	}
}

// A plugin's sections and partials with script constructs are not registered
// while the plugin lacks site.scripts, and the engine follows the grant live.
func TestPluginMarkupScriptsFollowTheGrant(t *testing.T) { //nolint:paralleltest // sets the global script gate
	hostile := map[string]string{
		"inline":  `<script>alert(1)</script>`,
		"extern":  `<script src="/p/evil/assets/x.js"></script>`,
		"onerror": `<img src=x onerror="alert(1)">`,
		"jsurl":   `<a href="javascript:alert(1)">l</a>`,
		"split":   `<scr{{if .Content}}{{end}}ipt>alert(1)</script>`,
		"srcdoc":  `<p srcdoc="x">`,
	}
	granted := false
	SetPluginScriptGate(func(string) bool { return granted })
	t.Cleanup(func() { SetPluginScriptGate(nil) })

	reg := newPluginRegistry()
	for name, markup := range hostile {
		if err := reg.registerSection("evil", "s"+name, `{{define "section-s`+name+`"}}<b>`+markup+`</b>{{end}}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.registerSection("evil", "plain", `{{define "section-plain"}}<p style="color:red">ok</p>{{end}}`); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerPartial("evil", "card", `<script>partialEvil()</script>`); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(Options{}, reg)
	if err != nil {
		t.Fatal(err)
	}
	render := func() string {
		v := minimalView()
		for name := range hostile {
			v.Sections = append(v.Sections, Section{ID: "i" + name, Type: "s" + name, Content: map[string]any{}})
		}
		v.Sections = append(v.Sections, Section{ID: "p", Type: "plain", Content: map[string]any{}})
		var sb strings.Builder
		if err := e.RenderPage(&sb, v); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}
	out := render()
	for _, bad := range []string{"alert(1)", "onerror=", "javascript:", "srcdoc="} {
		if strings.Contains(out, bad) {
			t.Errorf("without the grant the page has %q", bad)
		}
	}
	if !strings.Contains(out, "color:red") {
		t.Error("a section without script constructs must still render")
	}
	if got := len(reg.gated().sections); got != 1 {
		t.Errorf("sections registered without the grant = %d, want 1", got)
	}
	if got := reg.gated().partials["plugin/evil/card"]; strings.Contains(got, "script") {
		t.Errorf("the script partial is registered without the grant: %q", got)
	}

	granted = true
	if out := render(); !strings.Contains(out, "alert(1)") {
		t.Error("with the grant the section must render as today")
	}
	granted = false
	if out := render(); strings.Contains(out, "alert(1)") {
		t.Error("after the revoke the script is still on the page")
	}
}

func gatedSections(t *testing.T, markup map[string]string, granted bool) map[string]bool {
	t.Helper()
	SetPluginScriptGate(func(string) bool { return granted })
	t.Cleanup(func() { SetPluginScriptGate(nil) })
	reg := newPluginRegistry()
	for name, m := range markup {
		if err := reg.registerSection("p", name, `{{define "section-`+name+`"}}`+m+`{{end}}`); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]bool{}
	for typ := range reg.gated().sections {
		got[typ] = true
	}
	return got
}

// The exceptions a plugin section keeps without site.scripts.
func TestPluginMarkupExceptionsStayAllowed(t *testing.T) { //nolint:paralleltest // sets the global script gate
	allowed := map[string]string{
		"formhttp":  `<form action="http://example.com/x" method="post"><input name="a"></form>`,
		"formhttps": `<form action="https://example.com/x" method="get"></form>`,
		"formrel":   `<form action="/subscribe" method="post"></form>`,
		"formdyn":   `<form action="{{.Content.u}}" method="post"></form>`,
		"styleel":   `<style>.a{color:red}</style><p class="a">x</p>`,
		"styleattr": `<p style="color:red">x</p>`,
	}
	got := gatedSections(t, allowed, false)
	for name := range allowed {
		if !got[name] {
			t.Errorf("%s: refused without site.scripts, but it is on the exception list", name)
		}
	}
}

// Every element and attribute a widget pack refuses, other than the exceptions,
// is refused for a plugin section without the grant and accepted with it.
func TestPluginMarkupRefusedWithoutTheGrant(t *testing.T) { //nolint:paralleltest // sets the global script gate
	refused := map[string]string{
		"meta":       `<meta http-equiv="refresh" content="0;url=/x">`,
		"metaplain":  `<meta name="x" content="y">`,
		"svg":        `<svg><circle r="1"></circle></svg>`,
		"math":       `<math><mi>x</mi></math>`,
		"link":       `<link rel="stylesheet" href="/x.css">`,
		"template":   `<template><p>x</p></template>`,
		"noscript":   `<noscript><p>x</p></noscript>`,
		"dialog":     `<dialog open><p>x</p></dialog>`,
		"textarea":   `<textarea>x</textarea>`,
		"title":      `<title>x</title>`,
		"xlinkhref":  `<a xlink:href="/x">x</a>`,
		"formaction": `<form action="/x"><button formaction="/y">x</button></form>`,
		"formjs":     `<form action="javascript:x()"></form>`,
		"formmail":   `<form action="mailto:a@b.c"></form>`,
		"actionp":    `<p action="/x">x</p>`,
		"script":     `<script>x()</script>`,
		"onclick":    `<p onclick="x()">x</p>`,
	}
	for name, m := range refused {
		if got := gatedSections(t, map[string]string{name: m}, false); got[name] {
			t.Errorf("%s: registered without site.scripts", name)
		}
		if got := gatedSections(t, map[string]string{name: m}, true); !got[name] {
			t.Errorf("%s: not registered with site.scripts", name)
		}
	}
}

// The sample render is scanned too: markup a template only prints is refused.
func TestPluginMarkupSampleRenderIsScanned(t *testing.T) { //nolint:paralleltest // sets the global script gate
	funcs := template.FuncMap{"plugin_x": func() string { panic("must not be called") }}
	if err := scanSampleRender(`{{define "a"}}<p>{{plugin_x}}</p>{{end}}{{define "b"}}<p>ok</p>{{end}}`, funcs); err != nil {
		t.Fatalf("a harmless template was refused: %v", err)
	}
	// An action that prints a fixed constant is the one thing a sample sees that
	// the source scan could miss: the escaper keeps it as text, so it stays quiet.
	if err := scanSampleRender(`{{define "a"}}<p>{{"<svg>"}}</p>{{end}}`, funcs); err != nil {
		t.Fatalf("escaped text was refused: %v", err)
	}
}

// html/template joins `<scr` and an action that prints "ipt" into <script>, and
// puts "o" and "nclick" side by side as one attribute name. The sample render
// sees that; since the structural action rule, the source scan refuses these
// sources first, so the gate's call to scanSampleRender has no case of its own
// that is reachable: the gate's call is not pinned by any test (the
// five printing-action sources of TestPluginSampleRenderCanary pass both
// layers, which is a canary, not a proof). The call stays as defence in depth.
// This test pins the layer itself and that the gate refuses what it would
// print, not the wiring.
func TestPluginSampleRenderStopsWhatTheSourceScanCannotSee(t *testing.T) { //nolint:paralleltest // sets the global script gate
	sneaky := map[string]string{
		"joinedtag":  `<p><scr{{"ipt"}}>x()</scr{{"ipt"}}></p>`,
		"joinedattr": `<p {{"o"}}{{"nclick"}}>x</p>`,
	}
	for name, m := range sneaky {
		src := `{{define "section-` + name + `"}}` + m + `{{end}}`
		// The sample render is a layer of its own: it must see this even though
		// the structural action rule now refuses these sources too.
		if err := scanSampleRender(src, nil); err == nil {
			t.Fatalf("%s: the sample render of the section does not see what it prints", name)
		}
		// What the page would print without the gate.
		tpl := template.Must(template.New("t").Parse(m))
		var out strings.Builder
		if err := tpl.Execute(&out, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "<script") && !strings.Contains(out.String(), "onclick") {
			t.Fatalf("%s: prints %q, which holds nothing refused", name, out.String())
		}
		if got := gatedSections(t, map[string]string{name: m}, false); got[name] {
			t.Errorf("%s: registered without site.scripts although it prints %q", name, out.String())
		}
		if got := gatedSections(t, map[string]string{name: m}, true); !got[name] {
			t.Errorf("%s: not registered with site.scripts", name)
		}
	}
}

// A canary, not a proof. Five representative printing-action sources must pass
// both the source rule and the sample render: html/template escapes what a
// printing action prints by context, so the sample render has no case of its
// own on sources the rule accepts. The test does not search; it fails only if
// one of these five changes outcome.
func TestPluginSampleRenderCanary(t *testing.T) {
	t.Parallel()
	funcs := template.FuncMap{"plugin_x": func() string { return "" }}
	for name, src := range map[string]string{
		"script in text":   `{{define "a"}}<p>{{"<script>x()</script>"}}</p>{{end}}`,
		"tag in value":     `{{define "a"}}<p title="{{"\"><script>x()</script>"}}">x</p>{{end}}`,
		"onclick in value": `{{define "a"}}<p class="{{"a\" onclick=\"x()"}}">x</p>{{end}}`,
		"text after style": `{{define "a"}}<style>.a{b:c}</style><p>{{"</style><script>x()</script>"}}</p>{{end}}`,
		"func in text":     `{{define "a"}}<p>{{plugin_x}}</p>{{end}}`,
	} {
		if err := checkPluginScripts(src, funcs); err != nil {
			t.Errorf("%s: the source rule refused it: %v", name, err)
			continue
		}
		if err := scanSampleRender(src, funcs); err != nil {
			t.Errorf("%s: passes the source rule but the sample render refuses it: %v", name, err)
		}
	}
}
