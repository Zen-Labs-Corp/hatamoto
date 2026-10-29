package admin

import (
	"encoding/json"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/web"
)

// vendoredGraph returns the import map and the URLs of the vendored module files.
func vendoredGraph(t *testing.T) (map[string]string, []string) {
	t.Helper()
	as, err := BuildAssets(fstest.MapFS{"css/tokens.css": {Data: []byte("a{}")}, "css/base.css": {Data: []byte("b{}")}}, web.Vendor())
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for u := range as.mods {
		if strings.Contains(u, "/vendor/") {
			urls = append(urls, u)
		}
	}
	sort.Strings(urls)
	return as.imports, urls
}

// Every vendored module and every first-party module carries an import-map
// integrity entry that matches its served bytes.
func TestImportmapIntegrityCoversEveryModule(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, _ := g.a.state()
	var im struct {
		Imports   map[string]string `json:"imports"`
		Integrity map[string]string `json:"integrity"`
	}
	if err := json.Unmarshal([]byte(as.Importmap), &im); err != nil {
		t.Fatal(err)
	}
	if len(as.mods) < 80 {
		t.Fatalf("only %d modules known", len(as.mods))
	}
	for u := range as.mods {
		sri, ok := im.Integrity[u]
		if !ok {
			t.Errorf("no integrity for %s", u)
			continue
		}
		body := as.files[strings.TrimPrefix(u, "/admin/_/")].body
		if sri != sriOf(body) {
			t.Errorf("integrity of %s does not match its bytes", u)
		}
	}
	for spec, u := range im.Imports {
		if _, ok := im.Integrity[u]; !ok {
			t.Errorf("import %s -> %s has no integrity", spec, u)
		}
	}
}

// The preload closure of the blog editor covers every module the editor
// imports at run time, each with integrity, and nothing is listed twice.
func TestPreloadClosure(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, _ := g.a.state()

	base, err := as.Preloads([]string{"@hm/admin"})
	if err != nil {
		t.Fatal(err)
	}
	has := func(ps []Preload, sub string) bool {
		for _, p := range ps {
			if strings.Contains(p.URL, sub) {
				return true
			}
		}
		return false
	}
	if !has(base, "/js/admin.") || !has(base, "/js/components.") || !has(base, "/js/recovery.") {
		t.Errorf("admin closure incomplete: %+v", base)
	}
	if has(base, "@tiptap/core") || has(base, "/js/editor/") || has(base, "/js/editors.") || has(base, "/js/blogeditor.") {
		t.Error("the editor stack must not be preloaded on every page")
	}

	blog, err := as.Preloads([]string{"@hm/admin", "@hm/editor/kit", "@hm/editor/slash", "@hm/editor/bubble"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range blog {
		if seen[p.URL] {
			t.Errorf("%s listed twice", p.URL)
		}
		seen[p.URL] = true
		if !strings.HasPrefix(p.Integrity, "sha256-") {
			t.Errorf("%s has no integrity", p.URL)
		}
	}
	for _, want := range []string{"/js/editor/slash.", "/js/editor/bubble.", "@tiptap/suggestion@", "@tiptap/extension-bubble-menu@", "@floating-ui/dom@", "@tiptap/core@", "prosemirror-state@", "@tiptap/starter-kit@", "/js/editor/entitylink."} {
		if !has(blog, want) {
			t.Errorf("closure is missing %s", want)
		}
	}
	// Dependencies come before dependents.
	idx := func(sub string) int {
		for i, p := range blog {
			if strings.Contains(p.URL, sub) {
				return i
			}
		}
		return -1
	}
	if idx("/core@") > idx("/starter-kit@") || idx("prosemirror-state@") > idx("@tiptap/suggestion@") {
		t.Error("closure is not dependency-ordered")
	}
	if _, err := as.Preloads([]string{"@hm/nope"}); err == nil {
		t.Error("an unknown entry module must be an error")
	}
}

// Page templates declare entry modules; pages that declare none preload only
// the admin entry, editor pages the editor stack. Every declared module exists.
func TestPagesDeclareEntryModules(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	as, pages := g.a.state()
	declared := map[string][]string{}
	for name, ps := range pages {
		mods, err := pageModules(ps.tmpl(), as)
		if err != nil {
			t.Errorf("page %s: %v", name, err)
		}
		if len(mods) > 0 {
			declared[name] = mods
		}
	}
	// The module map: which pages declare which modules.
	want := map[string][]string{
		"analytics/overview":  {"@hm/aia"},
		"analytics/crawlers":  {"@hm/aia"},
		"analytics/content":   {"@hm/aia"},
		"analytics/referrals": {"@hm/aia"},
		"analytics/health":    {"@hm/aia"},
		"analytics/agent":     {"@hm/aia"},
		"entities/blogedit":   {"@hm/blogeditor", "@hm/seopanel", "@hm/editor/slash"},
		"entities/builder":    {"@hm/builder", "@hm/builder/screen", "@hm/canvas/host"},
		"entities/edit":       {"@hm/repeater", "@hm/content", "@hm/entityform", "@hm/seopanel"},
		"entities/list":       {"@hm/content"},
		"forms/edit":          {"@hm/content"},
		"forms/submissions":   {"@hm/content"},
		"linking/rule":        {"@hm/searchselect"},
		"linking/rules":       {"@hm/content"},
		"linking/suggestions": {"@hm/searchselect"},
		"media/detail":        {"@hm/content"},
		"media/list":          {"@hm/content"},
		"media/new":           {"@hm/content"},
		"menus/edit":          {"@hm/menutree", "@hm/searchselect"},
		"plugins/browse":      {"@hm/pluginpanel"},
		"redirects/list":      {"@hm/content"},
		"schemas/edit":        {"@hm/content"},
		"settings/api":        {"@hm/apikeys"},
		"settings/apikeynew":  {"@hm/apikeys"},
		"settings/general":    {"@hm/searchselect"},
		"setup/account":       {"@hm/setupcode"},
		"taxonomies/list":     {"@hm/content"},
		"taxonomies/new":      {"@hm/content"},
		"taxonomies/term":     {"@hm/seopanel"},
	}
	for page, mods := range want {
		for _, m := range mods {
			if !slices.Contains(declared[page], m) {
				t.Errorf("%s declares %v, want %s", page, declared[page], m)
			}
		}
	}
	for page := range declared {
		if _, ok := want[page]; !ok {
			t.Errorf("%s declares modules %v that the map does not list", page, declared[page])
		}
	}
	// SortableJS is a head script only for the pages whose modules drive it.
	for name, ps := range pages {
		scripts, err := pageScripts(ps.tmpl())
		if err != nil {
			t.Errorf("page %s: %v", name, err)
		}
		needs := slices.ContainsFunc(declared[name], func(m string) bool {
			return m == "@hm/repeater"
		}) || name == "schemas/edit" || name == "forms/edit" || name == "entities/builder" // the builder's component forms load repeater lazily
		if needs != slices.Contains(scripts, "sortablejs") {
			t.Errorf("%s: sortablejs declared=%v, want %v", name, slices.Contains(scripts, "sortablejs"), needs)
		}
	}
	for _, page := range []string{"entities/list", "menus/list", "users/list", "media/list"} {
		for _, m := range declared[page] {
			if strings.HasPrefix(m, "@hm/editor/") || m == "@hm/editors" || m == "@hm/blogeditor" {
				t.Errorf("%s must not load %s", page, m)
			}
		}
	}
}

// One copy of @tiptap/core and of each ProseMirror package: a second copy
// breaks plugin keys and instanceof checks ("Adding different instances of a
// keyed plugin"). Also checks every relative import in the graph lands on a
// listed file, and that nothing in it reaches a second state module.
func TestVendoredTiptapHasOneCoreAndOneProseMirror(t *testing.T) {
	t.Parallel()

	_, urls := vendoredGraph(t)
	count := map[string]int{}
	for _, u := range urls {
		for _, pkg := range []string{"@tiptap/core@", "prosemirror-state@", "prosemirror-model@", "prosemirror-view@", "prosemirror-transform@"} {
			if strings.Contains(u, "/"+pkg) && strings.HasSuffix(u, "/"+strings.TrimSuffix(path.Base(pkg), "@")+".mjs") {
				count[pkg]++
			}
		}
	}
	for _, pkg := range []string{"@tiptap/core@", "prosemirror-state@", "prosemirror-model@", "prosemirror-view@", "prosemirror-transform@"} {
		if count[pkg] != 1 {
			t.Errorf("%s main module appears %d times in the vendored tree, want 1", pkg, count[pkg])
		}
	}
	linkCopies := 0
	for _, u := range urls {
		if strings.HasSuffix(u, "/extension-link.mjs") {
			linkCopies++
		}
	}
	if linkCopies != 1 {
		t.Errorf("extension-link appears %d times (StarterKit's copy only), want 1", linkCopies)
	}
}
