package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// vendorManifest is the part of web/vendor/vendor.json the admin needs.
type vendorManifest struct {
	Assets []struct {
		Name      string            `json:"name"`
		Kind      string            `json:"kind"`
		File      string            `json:"file"`
		SHA256    string            `json:"sha256"`
		Importmap map[string]string `json:"importmap"`
	} `json:"assets"`
}

// ScriptTag is one classic vendored script with its subresource-integrity hash.
type ScriptTag struct {
	URL       string
	Integrity string
}

type asset struct {
	body []byte
	etag string
}

// Assets is the fingerprinted CSS/JS bundle plus the vendored scripts, built
// once at boot. All URLs live under <prefix>/_/ so the edge needs no rule.
type Assets struct {
	CSSURL    string
	Importmap template.HTML
	Tokens    []byte

	files   map[string]asset  // path below <prefix>/_/ -> content
	imports map[string]string // import-map specifier -> URL
	mods    map[string]module // module URL -> integrity and static imports
	jsURL   map[string]string
	scripts map[string]ScriptTag // vendor script name -> tag
}

// module is one ES module file: its subresource-integrity hash and the URLs of
// the modules it imports statically (dynamic import() is not part of the
// closure: a page that needs those modules declares them as entries).
type module struct {
	integrity string
	deps      []string
}

// Preload is one <link rel="modulepreload"> of a page.
type Preload struct {
	URL       string
	Integrity string
}

// defaultScripts are loaded on every page; others (sortablejs) are
// declared by the pages that use them.
var defaultScripts = []string{"htmx"}

func hashName(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:12]
}

func readGlob(fsys fs.FS, pattern string) ([][]byte, error) {
	names, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out [][]byte
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// buildAssets bundles css in a fixed order (tokens, base, partials/*, pages/*;
// the last two sorted by name), fingerprints it and the js modules, and reads
// the vendored files pinned by vendor.json, with every asset URL under
// prefix/_/. It is deterministic: equal inputs give equal URLs.
func buildAssets(adminFS, vendorFS fs.FS, prefix string) (*Assets, error) {
	assetsURL := prefix + "/_/"
	a := &Assets{files: map[string]asset{}, jsURL: map[string]string{}, scripts: map[string]ScriptTag{}, mods: map[string]module{}}
	sources := map[string][]byte{} // module URL -> source

	tokens, err := fs.ReadFile(adminFS, "css/tokens.css")
	if err != nil {
		return nil, err
	}
	base, err := fs.ReadFile(adminFS, "css/base.css")
	if err != nil {
		return nil, err
	}
	partials, err := readGlob(adminFS, "css/partials/*.css")
	if err != nil {
		return nil, err
	}
	pages, err := readGlob(adminFS, "css/pages/*.css")
	if err != nil {
		return nil, err
	}
	var css []byte
	for _, part := range append(append([][]byte{tokens, base}, partials...), pages...) {
		css = append(css, part...)
		css = append(css, '\n')
	}
	cssName := "admin." + hashName(css) + ".css"
	a.CSSURL = assetsURL + cssName
	a.files[cssName] = newAsset(css)
	a.Tokens = tokens

	imports := map[string]string{}
	// js/<name>.js is "@hm/<name>"; js/<dir>/<name>.js is "@hm/<dir>/<name>"
	// (the page editor's modules live in js/editor and js/canvas).
	jsNames, err := fs.Glob(adminFS, "js/*.js")
	if err != nil {
		return nil, err
	}
	nested, err := fs.Glob(adminFS, "js/*/*.js")
	if err != nil {
		return nil, err
	}
	jsNames = append(jsNames, nested...)
	for _, n := range jsNames {
		b, err := fs.ReadFile(adminFS, n)
		if err != nil {
			return nil, err
		}
		short := strings.TrimSuffix(strings.TrimPrefix(n, "js/"), ".js")
		hashed := "js/" + short + "." + hashName(b) + ".js"
		a.files[hashed] = newAsset(b)
		a.jsURL[short] = assetsURL + hashed
		sources[a.jsURL[short]] = b
		imports["@hm/"+short] = a.jsURL[short]
	}

	raw, err := fs.ReadFile(vendorFS, "vendor.json")
	if err != nil {
		return nil, fmt.Errorf("vendor manifest: %w", err)
	}
	var m vendorManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("vendor manifest: %w", err)
	}
	for _, v := range m.Assets {
		switch v.Kind {
		case "script":
			sum, err := hex.DecodeString(v.SHA256)
			if err != nil || len(sum) != sha256.Size {
				return nil, fmt.Errorf("vendor %s: bad sha256", v.Name)
			}
			b, err := fs.ReadFile(vendorFS, v.File)
			if err != nil {
				return nil, err
			}
			a.files["vendor/"+v.File] = newAsset(b)
			a.scripts[v.Name] = ScriptTag{
				URL:       assetsURL + "vendor/" + v.File,
				Integrity: "sha256-" + base64.StdEncoding.EncodeToString(sum),
			}
		case "module-graph":
			for spec, file := range v.Importmap {
				imports[spec] = assetsURL + "vendor/" + file
			}
			var files []string
			err := fs.WalkDir(vendorFS, path.Dir(firstDir(v.Importmap)), func(p string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					files = append(files, p)
				}
				return err
			})
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				b, err := fs.ReadFile(vendorFS, f)
				if err != nil {
					return nil, err
				}
				a.files["vendor/"+f] = newAsset(b)
				sources[assetsURL+"vendor/"+f] = b
			}
		}
	}
	a.imports = imports
	integrity := map[string]string{}
	for u, b := range sources {
		sri := sriOf(b)
		integrity[u] = sri
		a.mods[u] = module{integrity: sri, deps: staticImports(u, b, imports)}
	}
	im, err := json.Marshal(map[string]any{"imports": imports, "integrity": integrity})
	if err != nil {
		return nil, err
	}
	// An importmap script is not JS to html/template, so it is emitted as trusted
	// HTML: json.Marshal escapes <, > and & so the value cannot close the element.
	a.Importmap = template.HTML(im) //nolint:gosec
	return a, nil
}

func sriOf(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256-" + base64.StdEncoding.EncodeToString(h[:])
}

// staticRx finds `from "x"` and side-effect `import "x"` specifiers, in plain
// and minified sources. Dynamic import("x") has a parenthesis and is skipped.
var staticRx = regexp.MustCompile(`(?:\bfrom|\bimport)\s*["']([^"']+)["']`)

// staticImports resolves a module's static imports to URLs: bare specifiers
// through the import map, relative ones against the module's own URL. A
// specifier that resolves to nothing (text inside a comment) is ignored.
func staticImports(self string, src []byte, imports map[string]string) []string {
	var out []string
	for _, m := range staticRx.FindAllSubmatch(src, -1) {
		spec := string(m[1])
		var u string
		switch {
		case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
			u = path.Join(path.Dir(self), spec)
		default:
			u = imports[spec]
		}
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// Preloads returns the modulepreload closure of the entry modules (import-map
// specifiers such as "@hm/admin"), dependencies before dependents. An unknown
// entry is returned as an error so a typo in a template fails its test.
func (a *Assets) Preloads(entries []string) ([]Preload, error) {
	var out []Preload
	seen := map[string]bool{}
	var visit func(u string)
	visit = func(u string) {
		m, ok := a.mods[u]
		if !ok || seen[u] {
			return
		}
		seen[u] = true
		for _, d := range m.deps {
			visit(d)
		}
		out = append(out, Preload{URL: u, Integrity: m.integrity})
	}
	for _, e := range entries {
		u, ok := a.imports[e]
		if !ok {
			return nil, fmt.Errorf("unknown entry module %q", e)
		}
		visit(u)
	}
	return out, nil
}

// Module returns the URL and integrity of an import-map specifier.
func (a *Assets) Module(spec string) (Preload, bool) {
	u, ok := a.imports[spec]
	if !ok {
		return Preload{}, false
	}
	return Preload{URL: u, Integrity: a.mods[u].integrity}, true
}

// firstDir returns any importmap value; graphs share one top directory
// ("tiptap-3.31.3/...") and we walk that directory.
func firstDir(m map[string]string) string {
	for _, v := range m {
		top, _, _ := strings.Cut(v, "/")
		return top + "/x"
	}
	return "."
}

func newAsset(b []byte) asset {
	h := sha256.Sum256(b)
	return asset{body: b, etag: `"` + hex.EncodeToString(h[:8]) + `"`}
}

// JSURL is the fingerprinted URL of js/<name>.js ("" if unknown).
func (a *Assets) JSURL(name string) string { return a.jsURL[name] }

// Scripts returns the classic vendored scripts for a page: the defaults plus
// the extra names the page asked for (e.g. "sortablejs").
func (a *Assets) Scripts(extra []string) []ScriptTag {
	var out []ScriptTag
	seen := map[string]bool{}
	for _, n := range append(append([]string(nil), defaultScripts...), extra...) {
		if t, ok := a.scripts[n]; ok && !seen[n] {
			seen[n] = true
			out = append(out, t)
		}
	}
	return out
}

// Handler serves GET <prefix>/_/{path...}.
func (a *Assets) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("path")
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if name == "tokens.css" {
			// Stable URL for plugin UIs: short cache, cross-origin readable.
			h.Set("Content-Type", "text/css; charset=utf-8")
			h.Set("Cache-Control", "public, max-age=300")
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("ETag", newAsset(a.Tokens).etag)
			http.ServeContent(w, r, "tokens.css", time.Time{}, bytes.NewReader(a.Tokens))
			return
		}
		f, ok := a.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		ct := mime.TypeByExtension(path.Ext(name))
		switch path.Ext(name) {
		case ".mjs", ".js":
			ct = "text/javascript; charset=utf-8"
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.Set("Content-Type", ct)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("ETag", f.etag)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.body))
	})
}
