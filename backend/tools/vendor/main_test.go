package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestFile(t *testing.T, dir string, m Manifest) {
	t.Helper()
	if err := writeManifest(dir, &m); err != nil {
		t.Fatal(err)
	}
}

func scriptManifest(url string) Manifest {
	return Manifest{Schema: 1, Assets: []Asset{{
		Name: "lib", Version: "1.2.3", License: "MIT", Kind: "script",
		URL: url, File: "lib-1.2.3.min.js",
	}}}
}

func TestFetchPinsThenVerifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("alert(1)")) }))
	defer srv.Close()
	dir := t.TempDir()
	writeManifestFile(t, dir, scriptManifest(srv.URL+"/lib@1.2.3/lib.js"))
	if err := Fetch(dir, false, newClient(), ""); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err != nil {
		t.Fatalf("verify after fetch: %v", err)
	}
	// One flipped byte fails verify.
	p := filepath.Join(dir, "lib-1.2.3.min.js")
	if err := os.WriteFile(p, []byte("alert(2)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered file must fail verify, got %v", err)
	}
}

func TestFetchRefusesChangedUpstreamUnlessUpdate(t *testing.T) {
	body := "one"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	dir := t.TempDir()
	writeManifestFile(t, dir, scriptManifest(srv.URL+"/lib@1.2.3/lib.js"))
	if err := Fetch(dir, false, newClient(), ""); err != nil {
		t.Fatal(err)
	}
	body = "two"
	if err := Fetch(dir, false, newClient(), ""); err == nil || !strings.Contains(err.Error(), "upstream bytes changed") {
		t.Fatalf("changed bytes must be refused, got %v", err)
	}
	if err := Fetch(dir, true, newClient(), ""); err != nil {
		t.Fatalf("--update must accept: %v", err)
	}
}

func TestUnpinnedURLsRejected(t *testing.T) {
	for _, u := range []string{"https://x.test/lib@latest/a.js", "https://x.test/lib@^1.2.3/a.js", "https://x.test/lib/a.js", "https://x.test/lib@1/a.js"} {
		dir := t.TempDir()
		writeManifestFile(t, dir, scriptManifest(u))
		if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "not pinned") {
			t.Errorf("%s: want not-pinned error, got %v", u, err)
		}
	}
}

func TestRedirectOffHostRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeManifestFile(t, dir, scriptManifest(srv.URL+"/lib@1.2.3/lib.js"))
	if err := Fetch(dir, false, newClient(), ""); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("want redirect refusal, got %v", err)
	}
}

func TestVerifyFlagsExtraFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	dir := t.TempDir()
	writeManifestFile(t, dir, scriptManifest(srv.URL+"/lib@1.2.3/lib.js"))
	if err := Fetch(dir, false, newClient(), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err == nil || !strings.Contains(err.Error(), "stray.js") {
		t.Fatalf("want extra-file error, got %v", err)
	}
}

// The module graph keeps ONE copy of a shared module and rewrites
// origin-absolute imports to relative ones, following re-export shims.
func TestGraphDedupesAndRewrites(t *testing.T) {
	mods := map[string]string{
		"/a@1.0.0?target=es2022":       `export * from "/a@1.0.0/es2022/a.mjs";`,
		"/a@1.0.0/es2022/a.mjs":        `import {k} from"/shared@2.0.0/es2022/s.mjs";export*from"./dist/x.mjs";export const a=k;`,
		"/a@1.0.0/es2022/dist/x.mjs":   `export const x=1;`,
		"/b@1.0.0?target=es2022":       `export * from "/b@1.0.0/es2022/b.mjs";`,
		"/b@1.0.0/es2022/b.mjs":        `import {k} from"/shared@^2.0.0?target=es2022";export const b=k;`,
		"/shared@^2.0.0?target=es2022": `export * from "/shared@2.0.0/es2022/s.mjs";`,
		"/shared@2.0.0/es2022/s.mjs":   `export const k=1;`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		b, ok := mods[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeManifestFile(t, dir, Manifest{Schema: 1, Assets: []Asset{{
		Name: "g", Version: "1.0.0", License: "MIT", Kind: "module-graph", OriginHost: srv.URL, Dir: "g-1.0.0",
		Roots: map[string]string{"a": "/a@1.0.0?target=es2022", "b": "/b@1.0.0?target=es2022"},
	}}})
	if err := Fetch(dir, false, newClient(), ""); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir); err != nil {
		t.Fatal(err)
	}
	m, _ := readManifest(dir)
	a := m.Assets[0]
	if len(a.Files) != 4 {
		t.Fatalf("want 4 files (a, x, b, one shared), got %+v", a.Files)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "g-1.0.0/b@1.0.0/es2022/b.mjs"))
	if !strings.Contains(string(b), `"../../shared@2.0.0/es2022/s.mjs"`) {
		t.Fatalf("b.mjs not rewritten to the shared module: %s", b)
	}
	if got := a.Importmap["a"]; got != "g-1.0.0/a@1.0.0/es2022/a.mjs" {
		t.Fatalf("importmap a = %q", got)
	}
}

func TestNoticeIsGeneratedFromTheManifestAndVerifyCatchesDrift(t *testing.T) {
	m := &Manifest{Schema: 1,
		Assets:   []Asset{{Name: "x", Version: "1.0.0", License: "MIT"}},
		Licenses: []License{{Package: "x", Version: "1.0.0", SPDX: "MIT", File: "x-1.0.0.txt"}}}
	n := Notice(m)
	for _, want := range []string{"x 1.0.0  MIT", "licenses/x-1.0.0.txt"} {
		if !strings.Contains(n, want) {
			t.Errorf("NOTICE lacks %q:\n%s", want, n)
		}
	}
	// The committed NOTICE must be exactly what the committed manifest renders.
	if err := Verify("../../web/vendor"); err != nil {
		t.Fatal(err)
	}
}
