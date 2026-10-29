package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/menu"
)

type previewCall struct {
	location string
	items    []*menu.MenuItem
}

func menuPreviewRig(t *testing.T, fn func(*http.Request, string, *menu.Menu) ([]byte, string, error)) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) {
		d.Growth.Menus = newFakeMenus()
		d.Content.MenuPreview = fn
	})
}

func TestMenuPreviewBuildsTheTreeFromTheForm(t *testing.T) {
	t.Parallel()

	var got previewCall
	g := menuPreviewRig(t, func(_ *http.Request, loc string, m *menu.Menu) ([]byte, string, error) {
		got = previewCall{loc, m.Items}
		return []byte("<!doctype html><p>preview</p>"), "default-src 'self'", nil
	})
	id := uuid.New()
	f := itemForm("Main", [4]string{"Home", "custom", "0", "/"}, [4]string{"Docs", "none", "0", ""},
		[4]string{"Guide", "custom", "1", "/guide"}, [4]string{"Page", "entity", "1", id.String()},
		[4]string{"Bad", "custom", "0", "javascript:alert(1)"}, [4]string{"", "custom", "0", "/x"})
	f.Set("location", "footer")
	rec := g.do("POST", "/admin/theme/menus/preview", "owner-token", f, nil)
	if rec.Code != 200 || rec.Body.String() != "<!doctype html><p>preview</p>" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("X-Robots-Tag") != "noindex" ||
		h.Get("Content-Security-Policy") != "default-src 'self'" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("headers: %v", h)
	}
	if got.location != "footer" || len(got.items) != 4 {
		t.Fatalf("call: %q, %d top items", got.location, len(got.items))
	}
	docs := got.items[1]
	if docs.LinkType != menu.LinkTypeNone || len(docs.Children) != 2 || docs.Position != 1 {
		t.Errorf("docs: %+v", docs)
	}
	if c := docs.Children[0]; c.Label != "Guide" || c.Position != 2 || c.URL == nil || *c.URL != "/guide" {
		t.Errorf("guide: %+v", c)
	}
	if c := docs.Children[1]; c.Position != 3 || c.EntityID == nil || *c.EntityID != id {
		t.Errorf("unsaved page keeps its entity id: %+v", c)
	}
	// A row with an error still renders, as text; an empty label gets a name.
	if b := got.items[2]; b.LinkType != menu.LinkTypeNone || b.URL != nil || b.Position != 4 {
		t.Errorf("bad link not demoted: %+v", b)
	}
	if e := got.items[3]; e.Label != "Untitled item" || e.Position != 5 {
		t.Errorf("empty label: %+v", e)
	}
}

func TestMenuPreviewRefusals(t *testing.T) {
	t.Parallel()

	ok := func(*http.Request, string, *menu.Menu) ([]byte, string, error) { return []byte("x"), "", nil }
	g := menuPreviewRig(t, ok)
	f := itemForm("Main", [4]string{"A", "custom", "0", "/a"})
	if rec := g.do("POST", "/admin/theme/menus/preview", "", f, nil); rec.Code == 200 {
		t.Errorf("anonymous preview served: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/menus/preview", "owner-token", f, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("missing csrf token: %d", rec.Code)
	}
	big := url.Values{}
	for i := 0; i <= maxMenuItems; i++ {
		p := "i." + strconv.Itoa(i) + "."
		big.Set(p+"label", "x")
		big.Set(p+"type", "custom")
		big.Set(p+"url", "/x")
	}
	if rec := g.do("POST", "/admin/theme/menus/preview", "owner-token", big, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("201 items: %d", rec.Code)
	}
	off := menuPreviewRig(t, func(*http.Request, string, *menu.Menu) ([]byte, string, error) { return nil, "", ErrMenuPreviewOff })
	if rec := off.do("POST", "/admin/theme/menus/preview", "owner-token", f, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("rendering off: %d", rec.Code)
	}
	broken := menuPreviewRig(t, func(*http.Request, string, *menu.Menu) ([]byte, string, error) { return nil, "", errors.New("boom") })
	if rec := broken.do("POST", "/admin/theme/menus/preview", "owner-token", f, nil); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("render error: %d %s", rec.Code, rec.Body.String())
	}
	none := menuPreviewRig(t, nil)
	if rec := none.do("POST", "/admin/theme/menus/preview", "owner-token", f, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no dependency: %d", rec.Code)
	}
}
