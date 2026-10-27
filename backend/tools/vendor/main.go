// Command vendor fetches and verifies the third-party browser assets the Go
// admin ships (htmx, SortableJS, Tiptap). Stdlib only: no Node, no npm.
//
//	go run ./tools/vendor fetch [--update]   # network; pins new entries, refuses changed bytes
//	go run ./tools/vendor verify             # offline; committed bytes == vendor.json
//
// Run from backend/. The manifest is web/vendor/vendor.json; the committed
// bytes are the source of truth, the URLs are provenance.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxBytes   = 5 << 20
	maxModules = 400
)

// Manifest is web/vendor/vendor.json.
type Manifest struct {
	Schema      int     `json:"schema"`
	GeneratedBy string  `json:"generated_by"`
	Assets      []Asset `json:"assets"`
	// Licenses are the licence texts of everything above (esm.sh strips the
	// headers from the modules, so the texts ship beside them in licenses/).
	Licenses []License `json:"licenses,omitempty"`
}

// License is one pinned licence text. Covers names what it applies to
// ("@tiptap/*" for the Tiptap packages, which share one text).
type License struct {
	Package string `json:"package"`
	Version string `json:"version"`
	SPDX    string `json:"spdx"`
	Covers  string `json:"covers,omitempty"`
	URL     string `json:"url"`
	File    string `json:"file"` // under licenses/
	SHA256  string `json:"sha256,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
}

// Asset is one pinned dependency. Kind "script" is one classic script file;
// kind "module-graph" is a set of ES modules crawled from Roots and stored
// under Dir with relative imports (so the files are location independent).
type Asset struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	License string `json:"license"`
	Kind    string `json:"kind"`

	// script
	URL    string `json:"url,omitempty"`
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int    `json:"bytes,omitempty"`

	// module-graph
	Dir        string            `json:"dir,omitempty"`
	Roots      map[string]string `json:"roots,omitempty"`     // import-map specifier -> origin-relative URL
	Importmap  map[string]string `json:"importmap,omitempty"` // specifier -> file (relative to web/vendor)
	Files      []File            `json:"files,omitempty"`
	OriginHost string            `json:"origin,omitempty"`
}

// File is one module of a graph.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

var versioned = regexp.MustCompile(`@\d+\.\d+\.\d+([/?#-]|$)`)

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: vendor fetch [--update] | verify"))
	}
	dir := "web/vendor"
	var err error
	switch os.Args[1] {
	case "verify":
		err = Verify(dir)
	case "fetch":
		update := len(os.Args) > 2 && os.Args[2] == "--update"
		err = Fetch(dir, update, newClient(), "")
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "vendor:", err)
	os.Exit(1)
}

func readManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "vendor.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("vendor.json: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("vendor.json: unsupported schema %d", m.Schema)
	}
	return &m, nil
}

func writeManifest(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "vendor.json"), append(b, '\n'), 0o644)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// checkPinned rejects URLs that are not an exact version.
func checkPinned(a Asset) error {
	urls := []string{}
	if a.URL != "" {
		urls = append(urls, a.URL)
	}
	for _, r := range a.Roots {
		urls = append(urls, r)
	}
	if len(urls) == 0 {
		return fmt.Errorf("%s: no url", a.Name)
	}
	for _, u := range urls {
		if !versioned.MatchString(u) || strings.Contains(u, "@latest") {
			return fmt.Errorf("%s: %q is not pinned to an exact @x.y.z version", a.Name, u)
		}
	}
	return nil
}

// Verify checks, offline, that web/vendor holds exactly what vendor.json lists.
func Verify(dir string) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	want := map[string]string{"vendor.json": ""}
	for _, a := range m.Assets {
		if a.License == "" {
			return fmt.Errorf("%s: license missing", a.Name)
		}
		if err := checkPinned(a); err != nil {
			return err
		}
		switch a.Kind {
		case "script":
			if !strings.Contains(a.File, a.Version) {
				return fmt.Errorf("%s: file %q must carry the version %s", a.Name, a.File, a.Version)
			}
			want[a.File] = a.SHA256
		case "module-graph":
			for _, f := range a.Files {
				want[f.Path] = f.SHA256
			}
			for spec, p := range a.Importmap {
				if _, ok := want[p]; !ok {
					return fmt.Errorf("%s: importmap %q points at unlisted file %s", a.Name, spec, p)
				}
			}
		default:
			return fmt.Errorf("%s: unknown kind %q", a.Name, a.Kind)
		}
	}
	for _, l := range m.Licenses {
		if !versioned.MatchString(l.URL) || l.File == "" || l.SPDX == "" {
			return fmt.Errorf("license %s: needs a pinned url, file and spdx", l.Package)
		}
		want["licenses/"+l.File] = l.SHA256
	}
	if got, err := os.ReadFile(filepath.Join(dir, "NOTICE")); err != nil || string(got) != Notice(m) {
		return errors.New("NOTICE does not match vendor.json: run `make vendor` to regenerate it")
	}
	for p, h := range want {
		if p == "vendor.json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return fmt.Errorf("missing vendored file: %w", err)
		}
		if got := sum(b); got != h {
			return fmt.Errorf("%s: sha256 %s does not match vendor.json (%s)", p, got, h)
		}
	}
	var extra []string
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if _, ok := want[rel]; !ok && rel != "NOTICE" && !strings.HasPrefix(rel, "src/") {
			extra = append(extra, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("files not in vendor.json: %s", strings.Join(extra, ", "))
	}
	return nil
}

func newClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing redirect to another host: %s", req.URL.Host)
			}
			if req.URL.Scheme != "https" && !isLoopback(req.URL) {
				return errors.New("refusing a non-https redirect")
			}
			return nil
		},
	}
}

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost"
}

func get(c *http.Client, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !isLoopback(u) {
		return nil, fmt.Errorf("%s: only https is allowed", raw)
	}
	resp, err := c.Get(raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", raw, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", raw, maxBytes)
	}
	return b, nil
}

// Fetch downloads every asset. An already-pinned entry whose upstream bytes
// differ fails unless update is set. origin overrides the module-graph host
// (tests); empty means the asset's own "origin" field.
func Fetch(dir string, update bool, c *http.Client, origin string) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	for i := range m.Assets {
		a := &m.Assets[i]
		if err := checkPinned(*a); err != nil {
			return err
		}
		// The manifest is written after every asset, so a failure part-way
		// leaves files and vendor.json agreeing for everything fetched so far.
		switch a.Kind {
		case "script":
			err = fetchScript(dir, a, update, c)
		case "module-graph":
			o := origin
			if o == "" {
				o = a.OriginHost
			}
			err = fetchGraph(dir, a, update, c, o)
		default:
			err = fmt.Errorf("%s: unknown kind %q", a.Name, a.Kind)
		}
		if err != nil {
			return err
		}
		if err := writeManifest(dir, m); err != nil {
			return err
		}
	}
	for i := range m.Licenses {
		l := &m.Licenses[i]
		b, err := get(c, l.URL)
		if err != nil {
			return err
		}
		h := sum(b)
		if l.SHA256 != "" && l.SHA256 != h && !update {
			return fmt.Errorf("licence %s: upstream text changed; review and rerun with --update", l.Package)
		}
		l.SHA256, l.Bytes = h, len(b)
		if err := writeAtomic(filepath.Join(dir, "licenses", l.File), b); err != nil {
			return err
		}
		if err := writeManifest(dir, m); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "NOTICE"), []byte(Notice(m)), 0o644)
}

// Notice renders web/vendor/NOTICE from the manifest, so it cannot drift from
// what is vendored.
func Notice(m *Manifest) string {
	var b strings.Builder
	b.WriteString("Third-party assets vendored for the Go admin (pinned and hashed in vendor.json).\n")
	b.WriteString("Generated by `make vendor` from vendor.json; do not edit by hand.\n\n")
	for _, a := range m.Assets {
		fmt.Fprintf(&b, "%s %s  %s\n", a.Name, a.Version, a.License)
	}
	if len(m.Licenses) > 0 {
		b.WriteString("\nLicence texts (the Tiptap module graph carries no headers of its own):\n\n")
		for _, l := range m.Licenses {
			covers := l.Package
			if l.Covers != "" {
				covers = l.Covers
			}
			fmt.Fprintf(&b, "%s %s  %s  licenses/%s\n", covers, l.Version, l.SPDX, l.File)
		}
	}
	return b.String()
}

func writeAtomic(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".vendor-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func fetchScript(dir string, a *Asset, update bool, c *http.Client) error {
	b, err := get(c, a.URL)
	if err != nil {
		return err
	}
	h := sum(b)
	if a.SHA256 != "" && a.SHA256 != h && !update {
		return fmt.Errorf("%s: upstream bytes changed (%s != pinned %s); review and rerun with --update", a.Name, h, a.SHA256)
	}
	a.SHA256, a.Bytes = h, len(b)
	return writeAtomic(filepath.Join(dir, a.File), b)
}

// Import specifiers that esm.sh emits are origin-absolute ("/pkg@1.2.3/es2022/x.mjs")
// or relative ("./dist/x.mjs"). Absolute ones are rewritten to relative.
var importRx = regexp.MustCompile(`(\bfrom\s*|\bimport\s*\(?\s*)(["'])((?:/|\./|\.\./)[^"'?]*)(\?[^"']*)?(["'])`)

var exportStarRx = regexp.MustCompile(`export\s*\*\s*from\s*["'](/[^"']+)["']`)

func fetchGraph(dir string, a *Asset, update bool, c *http.Client, origin string) error {
	origin = strings.TrimRight(origin, "/")
	if origin == "" {
		return fmt.Errorf("%s: origin missing", a.Name)
	}
	srcs := map[string]string{}  // resolved url (path, no query) -> body
	alias := map[string]string{} // url with query -> real url path
	var queue []string
	for _, r := range a.Roots {
		queue = append(queue, r)
	}
	sort.Strings(queue)

	var resolve func(u string) (string, error)
	resolve = func(u string) (string, error) {
		if real, ok := alias[u]; ok {
			return real, nil
		}
		if !strings.Contains(u, "?") {
			return u, nil
		}
		b, err := get(c, origin+u)
		if err != nil {
			return "", err
		}
		mm := exportStarRx.FindSubmatch(b)
		if mm == nil {
			return "", fmt.Errorf("%s: %s is not a re-export shim", a.Name, u)
		}
		real, err := resolve(string(mm[1]))
		if err != nil {
			return "", err
		}
		alias[u] = real
		return real, nil
	}

	for len(queue) > 0 {
		u := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		real, err := resolve(u)
		if err != nil {
			return err
		}
		if _, ok := srcs[real]; ok {
			continue
		}
		if len(srcs) >= maxModules {
			return fmt.Errorf("%s: more than %d modules", a.Name, maxModules)
		}
		b, err := get(c, origin+real)
		if err != nil {
			return err
		}
		srcs[real] = string(b)
		for _, mm := range importRx.FindAllStringSubmatch(string(b), -1) {
			t := mm[3] + mm[4]
			if !strings.HasPrefix(t, "/") {
				t = path.Join(path.Dir(real), t)
			}
			queue = append(queue, t)
		}
	}

	files := map[string][]byte{}
	for real, body := range srcs {
		self := path.Dir(real)
		var rerr error
		out := importRx.ReplaceAllStringFunc(body, func(s string) string {
			mm := importRx.FindStringSubmatch(s)
			if !strings.HasPrefix(mm[3], "/") {
				return s // already relative
			}
			target, err := resolve(mm[3] + mm[4])
			if err != nil {
				rerr = err
				return s
			}
			rel, err := filepath.Rel(filepath.FromSlash(self), filepath.FromSlash(target))
			if err != nil {
				rerr = err
				return s
			}
			rel = filepath.ToSlash(rel)
			if !strings.HasPrefix(rel, ".") {
				rel = "./" + rel
			}
			return mm[1] + mm[2] + rel + mm[5]
		})
		if rerr != nil {
			return rerr
		}
		files[path.Join(a.Dir, real)] = []byte(out)
	}

	old := map[string]string{}
	for _, f := range a.Files {
		old[f.Path] = f.SHA256
	}
	var list []File
	for p, b := range files {
		h := sum(b)
		if prev, ok := old[p]; ok && prev != h && !update {
			return fmt.Errorf("%s: upstream bytes changed for %s; review and rerun with --update", a.Name, p)
		}
		list = append(list, File{Path: p, SHA256: h, Bytes: len(b)})
	}
	if len(old) > 0 && !update {
		for p := range old {
			if _, ok := files[p]; !ok {
				return fmt.Errorf("%s: upstream graph no longer contains %s; rerun with --update", a.Name, p)
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })

	// Replace the stored tree.
	if err := os.RemoveAll(filepath.Join(dir, a.Dir)); err != nil {
		return err
	}
	for p, b := range files {
		if err := writeAtomic(filepath.Join(dir, filepath.FromSlash(p)), b); err != nil {
			return err
		}
	}
	a.Files = list
	a.Importmap = map[string]string{}
	for spec, r := range a.Roots {
		real, err := resolve(r)
		if err != nil {
			return err
		}
		a.Importmap[spec] = path.Join(a.Dir, real)
	}
	return nil
}
