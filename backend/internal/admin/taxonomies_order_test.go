package admin

import (
	"net/url"
	"strings"
	"testing"
)

func TestTermOrderGoldenAndMove(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	rows := c.record()
	body := c.get(t, "owner-token", "/admin/settings/taxonomies?kind=category", "terms-order")
	has(t, body, `/admin/settings/taxonomies/terms/`+fid(32).String()+`/move`, `name="op" value="outdent"`)
	// The moves are visible buttons, disabled with a reason where they do nothing; terms have no drag handle.
	has(t, body, `<span>Up</span>`, `<span>Indent</span>`, `title="Already first among its siblings."`, `title="Already at the top level."`)
	lacks(t, body, `hm-table__handle`)
	rec := c.post("/admin/settings/taxonomies/terms/"+fid(32).String()+"/move", url.Values{"op": {"outdent"}})
	if rec.Code != 303 {
		t.Fatalf("outdent = %d", rec.Code)
	}
	if c.terms.terms[1].ParentID != nil {
		t.Error("outdent did not clear the parent")
	}
	if page := c.next(rec, rec.Header().Get("Location")); !strings.Contains(page, "Press moved to position") {
		t.Error("flash missing")
	}
	wantAudit(t, rows(), "term.reorder")
	// A move the tree does not allow is refused without an audit row.
	rec = c.post("/admin/settings/taxonomies/terms/"+fid(31).String()+"/move", url.Values{"op": {"up"}})
	if rec.Code != 303 {
		t.Fatalf("first row up = %d", rec.Code)
	}
	wantAudit(t, rows(), "term.reorder")
}

func TestTermMoveIsOwnerOnly(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	rec := c.do("POST", "/admin/settings/taxonomies/terms/"+fid(32).String()+"/move", "editor-token", url.Values{"op": {"up"}}, nil)
	if rec.Code != 403 {
		t.Errorf("editor term move = %d, want 403", rec.Code)
	}
}
