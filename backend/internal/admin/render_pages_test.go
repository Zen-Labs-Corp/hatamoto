package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/web"
)

// Every embedded page parses: lazy parsing moves a template error from boot to
// first use, so this keeps CI catching it.
func TestAllAdminPagesParse(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	if len(pages) == 0 {
		t.Fatal("no admin pages")
	}
	for key, ps := range pages {
		if _, err := ps.tmpl(); err != nil {
			t.Errorf("page %s: %v", key, err)
		}
	}
}

// Booting parses the shared layouts only: no page is parsed until it is used.
func TestAdminPagesParseLazily(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	for key, ps := range pages {
		if ps.t != nil {
			t.Fatalf("page %s was parsed at build", key)
		}
	}
	ps := pages["setup/admin-address-strength"]
	first, err := ps.tmpl()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := ps.tmpl(); again != first {
		t.Error("a page must be built once per Admin")
	}
}

// With a template dir every request sees the edited files.
func TestAdminTemplateDirEdits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.CopyFS(dir, web.Admin()); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, func(d *Deps) { d.Dir = dir })
	const key = "setup/admin-address-strength"
	file := filepath.Join(dir, "pages", key+".html")
	const marker = "EDITED-MARKER-7f3a"
	if err := os.WriteFile(file, []byte(`{{define "main"}}`+marker+`{{end}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, pages := g.a.state()
	tmpl, err := pages[key].tmpl()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, "main", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), marker) {
		t.Errorf("edited template not served: %q", sb.String())
	}
}

// RenderPartial runs against the layouts and partials alone, so using it any
// number of times parses no page.
func TestRenderPartialParsesNoPage(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for range 300 {
		out, err := g.a.RenderPartial("hm/aia-policy-start", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = out
	}
	_, pages := g.a.state()
	for key, ps := range pages {
		if ps.t != nil {
			t.Fatalf("RenderPartial parsed page %s", key)
		}
	}
}
