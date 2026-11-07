package admin

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
)

// Building the rig mounts every admin route on one ServeMux, which panics on a
// pattern conflict: the designer routes must sit beside the legacy ones.
func TestFormDesignRoutesRegister(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	if rec := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("design: %d", rec.Code)
	}
}

func TestFormDesignPage(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("design: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`data-hm-fd-boot`, `"version":`, `"saveUrl":"/admin/forms/` + fm.ID.String() + `/definition"`, `class="hm-fd"`, `hm-fd__palette`, `hm-fd__canvas`, `hm-fd__inspector`, `needs JavaScript`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if n := strings.Count(body, `class="hm-tabs__tab"`); n != 4 {
		t.Errorf("tab strip has %d items, want 4", n)
	}
	if !strings.Contains(body, `href="/admin/forms/submissions?form=`+fm.ID.String()+`"`) {
		t.Error("tab strip lacks the submissions link")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("design a11y: %v", issues)
	}
}

func TestFormDesignEditRedirectAndUnknown(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("GET", "/admin/forms/edit/"+fm.ID.String(), "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/forms/design/"+fm.ID.String() {
		t.Errorf("edit redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, p := range []string{"/admin/forms/design/", "/admin/forms/edit/"} {
		if rec := g.do("GET", p+uuid.NewString(), "owner-token", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s unknown: %d", p, rec.Code)
		}
	}
}

// The designer's first paint carries its own modules and the shared ones, never the lazy chunks:
// the preview, the inspector sections, the shortcut sheet and the history panel load on demand.
// Only the modulepreload links count: the import map names every module.
func TestFormDesignModulepreloadStaysSmall(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("design: %d", rec.Code)
	}
	preloaded := strings.Join(regexp.MustCompile(`<link rel="modulepreload"[^>]*>`).FindAllString(rec.Body.String(), -1), "\n")
	for _, want := range []string{"/forms/screen.", "/forms/canvas.", "/forms/palette.", "/forms/inspector.", "/forms/save."} {
		if !strings.Contains(preloaded, want) {
			t.Errorf("first paint lacks %s", want)
		}
	}
	for _, lazy := range []string{"/forms/preview.", "/forms/inspsections.", "/builder/keys.", "/builder/history."} {
		if strings.Contains(preloaded, lazy) {
			t.Errorf("first paint preloads the lazy module %s", lazy)
		}
	}
	for _, banned := range []string{"/builder/screen.", "/builder/store.", "/builder/drag.", "/builder/sync.", "/builder/inspector."} {
		if strings.Contains(preloaded, banned) {
			t.Errorf("the designer pulls the page builder module %s", banned)
		}
	}
}
