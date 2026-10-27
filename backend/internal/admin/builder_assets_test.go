package admin

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var bootRx = regexp.MustCompile(`(?s)<script type="application/json" id="hm-builder-boot">(.*?)</script>`)

// builderPage is the builder shell of the rig's page with doc as its layout.
func builderPage(t *testing.T, r *editorRig, attrs string) string {
	t.Helper()
	r.ent.Attributes = json.RawMessage(attrs)
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("builder page: %d", rec.Code)
	}
	return rec.Body.String()
}

// The shell starts from one JSON data block: the stored doc, the version and the
// vocabularies the palette and inspector read.
func TestBuilderBootJSON(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	body := builderPage(t, r, builderOnlyDoc)
	m := bootRx.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no boot block")
	}
	var boot struct {
		Doc     json.RawMessage `json:"doc"`
		Version int             `json:"version"`
		Status  string          `json:"status"`
		Name    string          `json:"name"`
		Kinds   []struct {
			Kind  string
			Props []struct{ Key, Bind string }
		}
		BindSources []struct{ Source, Class string } `json:"bindSources"`
		Sections    []struct{ Type, Title string }
		URLs        map[string]string `json:"urls"`
	}
	if err := json.Unmarshal([]byte(m[1]), &boot); err != nil {
		t.Fatalf("boot is not JSON: %v", err)
	}
	var want map[string]json.RawMessage
	_ = json.Unmarshal([]byte(builderOnlyDoc), &want)
	var a, b any
	_ = json.Unmarshal(boot.Doc, &a)
	_ = json.Unmarshal(want["page_doc"], &b)
	if !jsonEqual(a, b) {
		t.Errorf("boot doc = %s, want the stored page_doc", boot.Doc)
	}
	if boot.Version != r.ent.Version || boot.Status != "draft" || boot.Name != "Landing" {
		t.Errorf("boot meta = %d %q %q", boot.Version, boot.Status, boot.Name)
	}
	if len(boot.BindSources) < 20 {
		t.Errorf("bindSources: %d rows", len(boot.BindSources))
	}
	for _, k := range boot.Kinds {
		for _, p := range k.Props {
			if k.Kind == "heading" && p.Key == "text" && p.Bind != "text" {
				t.Errorf("heading.text bind = %q", p.Bind)
			}
		}
	}
	if len(boot.Kinds) < 8 || len(boot.Sections) != 2 {
		t.Errorf("kinds %d, sections %d", len(boot.Kinds), len(boot.Sections))
	}
	for _, k := range []string{"stage", "render", "component", "save", "stylespec"} {
		if !strings.HasPrefix(boot.URLs[k], "/admin/") {
			t.Errorf("url %s = %q", k, boot.URLs[k])
		}
	}
	// the data block carries no inline style and no script besides itself
	if strings.Contains(body, ` style="`) {
		t.Error("inline style attribute in the builder page")
	}

	// a text with markup characters cannot close the block
	r.ent.Name = `</script><b>x`
	if body := builderPage(t, r, builderOnlyDoc); strings.Count(body, `id="hm-builder-boot"`) != 1 || strings.Contains(bootRx.FindStringSubmatch(body)[1], "</script") {
		t.Error("the boot block can be closed from its data")
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// The builder owns its own editors: its no-JS outline (in <noscript>) carries
// schemaform hooks, which must not pull the page editor in. The classic editor
// still gets it.
func TestBuilderPageSkipsMarkupHooks(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	body := builderPage(t, r, validDocSection)
	if !strings.Contains(body, `data-hm-editor="html"`) {
		t.Fatal("the outline should carry an html field for this to prove anything")
	}
	scripts, preloads := moduleTags(body)
	all := append(slices.Clone(scripts), preloads...)
	for _, n := range all {
		if n == "editors" {
			t.Fatalf("builder page loads @hm/editors: %v", all)
		}
	}
	if !slices.Contains(scripts, "builder") {
		t.Errorf("module script builder missing: %v", scripts)
	}
	// Lazy modules are reached by import(): never a module script, never a modulepreload.
	for _, lazy := range []string{"repeater", "content", "editor/mount", "editor/kit", "editor/fieldsview", "editor/slash", "editor/bubble", "editor/htmltext", "builder/style", "builder/reuse", "builder/addtab", "builder/savedialog", "builder/commandk", "builder/recent", "builder/patterns", "builder/startpanel", "builder/coachmarks", "builder/sitestyles", "builder/colournames", "builder/keys", "builder/fields", "builder/presetpanel", "builder/designops", "builder/controls", "builder/controlsui", "builder/boxmodel", "builder/presetsrc", "builder/elementsettings"} {
		if slices.Contains(all, lazy) {
			t.Errorf("lazy module %s is a module tag or modulepreload on the builder page: %v", lazy, all)
		}
	}
	if !strings.Contains(body, "sortable-") {
		t.Error("sortablejs is not loaded for the component forms")
	}
}

// The media picker's document click handler lives in the lazy content chunk; native Image fields and the
// rich-text toolbar need it on a fresh load, so the screen must fetch it on the first opener click.
func TestBuilderScreenLoadsPickerLazily(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	var src string
	for path, f := range as.files {
		if strings.HasPrefix(path, "js/builder/screen.") {
			src = string(f.body)
		}
	}
	for _, want := range []string{`import("@hm/content")`, `[data-hm-media-picker]`, "stopImmediatePropagation"} {
		if !strings.Contains(src, want) {
			t.Errorf("builder/screen.js lacks %s", want)
		}
	}
}

func TestBuilderModulesAreFingerprintedAndMapped(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	for _, name := range []string{"builder", "builder/screen", "builder/store", "builder/bridge", "builder/bp", "builder/lazy", "builder/style", "builder/addtab", "builder/savedialog", "builder/commandk", "builder/recent", "builder/patterns", "builder/startpanel", "builder/coachmarks", "builder/sitestyles", "builder/colournames", "editor/mount", "repeater", "content", "searchselect"} {
		u := as.JSURL(name)
		if u == "" || !strings.Contains(u, "/js/"+name+".") {
			t.Fatalf("%s: %q", name, u)
		}
		if !strings.Contains(string(as.Importmap), `"@hm/`+name+`":"`+u+`"`) {
			t.Errorf("%s missing from the import map", name)
		}
	}
	// no eval and no inline styles in the builder sources
	for path, f := range as.files {
		if !strings.HasPrefix(path, "js/builder/") && !strings.HasPrefix(path, "js/editor/mount.") {
			continue
		}
		src := string(f.body)
		for _, bad := range []string{"eval(", "new Function", `setAttribute("style"`, "setAttribute('style'"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains %q", path, bad)
			}
		}
	}
}

// UX-2 builds on three screen.js exports and its own stylesheet; this pins the contract.
func TestBuilderScreenExportsContract(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	var src string
	for path, f := range as.files {
		if strings.HasPrefix(path, "js/builder/screen.") {
			src = string(f.body)
		}
	}
	for _, name := range []string{"mountInspector", "onSelect", "insertAt"} {
		if !regexp.MustCompile(`(?m)^export function ` + name + `\b`).MatchString(src) {
			t.Errorf("builder/screen.js does not export function %s", name)
		}
	}
	if !strings.Contains(src, "mountInspector as drawInspector") {
		t.Error("the inspector import must stay aliased so the export can take the name")
	}
}

// css/pages/*.css is concatenated into the one admin stylesheet, so an (almost) empty file is how UX-2 gets its own rules.
func TestBuilderInspectorCSSBundled(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	as, _ := r.a.state()
	var css string
	for path, f := range as.files {
		if strings.HasPrefix(path, "admin.") && strings.HasSuffix(path, ".css") {
			css = string(f.body)
		}
	}
	if !strings.Contains(css, "builderinspector.css:") {
		t.Error("the admin stylesheet does not contain css/pages/builderinspector.css")
	}
}
