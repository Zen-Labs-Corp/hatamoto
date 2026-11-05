package admin

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

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

func TestOrderPageGoldenFlat(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	c.ents.addEntity(60, c.page, "Alpha", "alpha", entity.StatusPublished, `{"body":"x"}`, t0)
	c.ents.addEntity(61, c.page, "Beta", "beta", entity.StatusDraft, `{"body":"x"}`, t0.Add(time.Hour))
	body := c.get(t, "owner-token", "/admin/entities/order?type=page", "entities-order")
	has(t, body, `aria-live="polite"`, `data-hm-orderlist`, `data-hm-drag`)
	// Every flat row has a visible drag handle and inline Up / Down buttons, no "…" menu.
	if n := strings.Count(body, `class="hm-table__handle" aria-hidden="true">::</span>`); n != 3 {
		t.Errorf("drag handles = %d, want 3", n)
	}
	lacks(t, body, `aria-haspopup="menu"`, `value="indent"`, `value="outdent"`)
	first := body[strings.Index(body, `id="row-`+fid(14).String()+`"`):] // the seeded page leads the order
	first = first[:strings.Index(first, "</tr>")]
	has(t, first, `aria-label="Move About us up"`, `title="Already first among its siblings."`, `aria-describedby="why-`+fid(14).String()+`-Up"`, `disabled="disabled"`)
	has(t, first, `<button type="submit" class="hm-btn hm-btn--secondary hm-btn--sm" aria-label="Move About us down"><span>Down</span>`)
	last := body[strings.Index(body, `id="row-`+fid(61).String()+`"`):]
	last = last[:strings.Index(last, "</tr>")]
	has(t, last, `title="Already last among its siblings."`)
}

func TestOrderPageGoldenTreeWithDepthLimit(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	// A chain of eight entries reaches the depth limit; a second child of the
	// seventh may not be indented under the eighth.
	var parent *entity.Entity
	for i := 0; i < 8; i++ {
		e := c.ents.addEntity(70+i, c.service, fmt.Sprintf("Level %d", i+1), fmt.Sprintf("level-%d", i+1), entity.StatusPublished, `{"summary":"x"}`, t0.Add(time.Duration(i)*time.Minute))
		if parent != nil {
			e.ParentID = &parent.ID
		}
		parent = e
	}
	zeta := c.ents.addEntity(80, c.service, "Zeta sibling", "zeta-sibling", entity.StatusPublished, `{"summary":"x"}`, t0.Add(time.Hour))
	zeta.ParentID = &c.ents.ents[fid(76)].ID
	body := c.get(t, "owner-token", "/admin/entities/order?type=service", "entities-order-tree")
	has(t, body, `Depth limit: 8 levels in the URL`)
	lacks(t, body, `data-hm-drag`)
	row := body[strings.Index(body, `id="row-`+fid(80).String()+`"`):]
	row = row[:strings.Index(row, "</tr>")]
	has(t, row, `value="up"`, `value="outdent"`, `<span>Down</span>`)
	lacks(t, row, `value="indent"`, `value="down"`) // disabled buttons carry no form fields
	// Indent is shown, disabled, with the reason, at the depth limit.
	ind := row[strings.Index(row, `<span>Indent</span>`)-200:]
	ind = ind[:strings.Index(ind, "</form>")]
	has(t, ind, ` disabled`, `title="Moving in would pass the depth limit of 8 levels in the URL."`, `aria-describedby="why-`+fid(80).String()+`-Indent"`)
	has(t, row, `id="why-`+fid(80).String()+`-Indent"`)
	lacks(t, body, `data-hm-drag`, `class="hm-table__handle"`, `aria-haspopup="menu"`)
}

func TestOrderMoveAuditsOnceAndExplainsRefusals(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	rows := c.record()
	c.ents.addEntity(60, c.service, "Alpha", "alpha", entity.StatusPublished, `{"summary":"x"}`, t0)
	b := c.ents.addEntity(61, c.service, "Beta", "beta", entity.StatusPublished, `{"summary":"x"}`, t0.Add(time.Hour))
	rec := c.post("/admin/entities/"+b.ID.String()+"/move", url.Values{"op": {"up"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "#row-"+b.ID.String()) {
		t.Fatalf("up = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	wantAudit(t, rows(), "entity.reorder")
	// A refused move writes no audit row and says why.
	c.ents.moveErr = entity.ErrParentTooDeep
	rec = c.post("/admin/entities/"+b.ID.String()+"/move", url.Values{"op": {"indent"}})
	if rec.Code != 303 {
		t.Fatalf("refused = %d", rec.Code)
	}
	if page := c.next(rec, rec.Header().Get("Location")); !strings.Contains(page, "nesting would be too deep") {
		t.Errorf("refusal flash missing")
	}
	wantAudit(t, rows(), "entity.reorder")
}

func TestOrderRoutesNeedAnEditorOrOwner(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	if rec := c.do("GET", "/admin/entities/order?type=service", "editor-token", nil, nil); rec.Code != 200 {
		t.Errorf("editor order page = %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/entities/order?type=service", "", nil, nil); rec.Code == 200 {
		t.Error("signed-out visitor saw the order page")
	}
}

// An Indent or Outdent that takes a plugin's page over is audited and says so.
func TestOrderMoveSaysWhenItTookAPageOver(t *testing.T) {
	t.Parallel()
	for _, took := range []bool{true, false} {
		c, _ := ownershipRig(t)
		c.ents.tookOwnership = took
		rows := c.record()
		c.ents.addEntity(60, c.service, "Alpha", "alpha", entity.StatusPublished, `{"summary":"x"}`, t0)
		b := c.ents.addEntity(61, c.service, "Beta", "beta", entity.StatusPublished, `{"summary":"x"}`, t0.Add(time.Hour))
		rec := c.post("/admin/entities/"+b.ID.String()+"/move", url.Values{"op": {"indent"}})
		if rec.Code != 303 {
			t.Fatalf("indent = %d", rec.Code)
		}
		wantAudit(t, rows(), "entity.reorder")
		if got := rows()[0].Detail["took_ownership"] == true; got != took {
			t.Errorf("took_ownership = %v, want %v", got, took)
		}
		page := c.next(rec, rec.Header().Get("Location"))
		if got := strings.Contains(page, "This page is yours now"); got != took {
			t.Errorf("took = %v, flash note shown = %v", took, got)
		}
	}
}

// The drag enhancement asks for JSON and must get a plain 204 or an error
// status, never a redirect that hides the outcome.
func TestOrderMoveAnswersScriptedRequestsWithoutRedirect(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	c.ents.addEntity(60, c.service, "Alpha", "alpha", entity.StatusPublished, `{"summary":"x"}`, t0)
	b := c.ents.addEntity(61, c.service, "Beta", "beta", entity.StatusPublished, `{"summary":"x"}`, t0)
	path := "/admin/entities/" + b.ID.String() + "/move"
	hdr := map[string]string{"Accept": "application/json"}
	if rec := c.do("POST", path, "owner-token", url.Values{"position": {"1"}}, hdr); rec.Code != 204 || rec.Header().Get("Location") != "" {
		t.Errorf("success = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	c.ents.moveErr = entity.ErrParentTooDeep
	rec := c.do("POST", path, "owner-token", url.Values{"position": {"1"}}, hdr)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "nesting would be too deep") {
		t.Errorf("failure = %d %q", rec.Code, rec.Body)
	}
}

// Focus after a button move goes to the row's first enabled button, and a drag
// starts only from the "::" handle.
func TestOrderingScriptFocusesTheRowButton(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../web/admin/js/ordering.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	if !strings.Contains(js, `querySelector("button:not(:disabled)")`) || strings.Contains(js, "aria-haspopup") {
		t.Error("restoreFocus must focus the row's first enabled button")
	}
	if !strings.Contains(js, `handle: ".hm-table__handle"`) {
		t.Error("drag must start from the handle")
	}
	if !strings.Contains(js, `redirect: "manual"`) || strings.Contains(js, "location.reload") {
		t.Error("drag requests must not follow the redirect nor reload over the live region")
	}
}
