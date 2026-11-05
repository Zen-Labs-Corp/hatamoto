package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

func TestOrderPageIsPlainPostForms(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	a := c.ents.addEntity(60, c.service, "Alpha", "alpha", entity.StatusPublished, `{"summary":"x"}`, t0)
	c.ents.addEntity(61, c.service, "Beta", "beta", entity.StatusDraft, `{"summary":"x"}`, t0)
	body := c.get(t, "owner-token", "/admin/entities/order?type=service", "")
	has(t, body, `action="/admin/entities/`+a.ID.String()+`/move"`, `name="op" value="up"`, `name="op" value="indent"`,
		`Depth limit: 8 levels in the URL`)
	if strings.Contains(body, "<script") && strings.Contains(body, "onclick") {
		t.Error("inline script on the order page")
	}
	if rec := c.do("GET", "/admin/entities/order?type=nope", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown type = %d", rec.Code)
	}
}

func TestOrderMovePostRedirectsWithFlash(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	c.ents.addEntity(60, c.service, "Alpha", "alpha", entity.StatusPublished, `{"summary":"x"}`, t0)
	b := c.ents.addEntity(61, c.service, "Beta", "beta", entity.StatusPublished, `{"summary":"x"}`, t0)
	rec := c.post("/admin/entities/"+b.ID.String()+"/move", url.Values{"op": {"indent"}})
	if rec.Code != 303 {
		t.Fatalf("move = %d", rec.Code)
	}
	if b.ParentID == nil {
		t.Error("indent did not re-parent")
	}
	page := c.next(rec, rec.Header().Get("Location"))
	if !strings.Contains(page, "Beta moved to position") {
		t.Errorf("flash missing")
	}
	rec = c.post("/admin/entities/"+b.ID.String()+"/move", url.Values{"op": {"up"}})
	if rec.Code != 303 {
		t.Fatalf("up = %d", rec.Code)
	}
}

func TestPlanMove(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	_ = c
	ids := []orderNode{{ID: fid(1)}, {ID: fid(2)}, {ID: fid(3)}}
	if p, ok := planMove("up", ids, fid(1)); ok {
		t.Errorf("first row up = %+v", p)
	}
	if p, ok := planMove("down", ids, fid(1)); !ok || p.Position != 2 {
		t.Errorf("down = %+v %v", p, ok)
	}
	if p, ok := planMove("indent", ids, fid(3)); !ok || p.Parent == nil || *p.Parent != fid(2) {
		t.Errorf("indent = %+v", p)
	}
	p1 := fid(1)
	nested := []orderNode{{ID: fid(1)}, {ID: fid(2), Parent: &p1}, {ID: fid(3)}}
	if p, ok := planMove("outdent", nested, fid(2)); !ok || !p.Clear || p.Position != 2 {
		t.Errorf("outdent = %+v", p)
	}
	if _, ok := planMove("outdent", nested, fid(1)); ok {
		t.Error("top-level outdent allowed")
	}
}
