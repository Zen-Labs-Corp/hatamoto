package admin

import (
	"context"
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// record wires a recording audit sink into the rig and returns the rows so far.
func (c *contentRig) record() func() []AuditEvent {
	var got []AuditEvent
	c.a.deps.Audit = func(_ context.Context, e AuditEvent) { got = append(got, e) }
	return func() []AuditEvent { return got }
}

func wantAudit(t *testing.T, got []AuditEvent, actions ...string) {
	t.Helper()
	if len(got) != len(actions) {
		t.Fatalf("audit rows = %d %v, want %v", len(got), got, actions)
	}
	for i, a := range actions {
		if got[i].Action != a {
			t.Errorf("row %d action = %q, want %q", i, got[i].Action, a)
		}
		if got[i].Actor != "owner@example.com" {
			t.Errorf("row %d actor = %q", i, got[i].Actor)
		}
	}
}

func TestEntityStatusChangeIsAudited(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	for _, tc := range []struct {
		n              int
		status, action string
	}{
		{12, "published", "entity.publish"},
		{11, "draft", "entity.unpublish"},
		{14, "archived", "entity.archive"},
	} {
		before := len(rows())
		if rec := c.post("/admin/entities/"+fid(tc.n).String()+"/status", url.Values{"status": {tc.status}}); rec.Code != 303 {
			t.Fatalf("status %s: %d", tc.status, rec.Code)
		}
		got := rows()
		if len(got) != before+1 || got[before].Action != tc.action || got[before].Target != fid(tc.n).String() {
			t.Fatalf("status %s: %+v", tc.status, got)
		}
	}
	last := rows()[2]
	if last.Detail["name"] != "About us" || last.Detail["slug"] != "about" {
		t.Errorf("detail = %v", last.Detail)
	}
	// A failing or refused change writes nothing.
	n := len(rows())
	c.post("/admin/entities/"+fid(99).String()+"/status", url.Values{"status": {"published"}})
	c.post("/admin/entities/"+fid(12).String()+"/status", url.Values{"status": {"bogus"}})
	if len(rows()) != n {
		t.Errorf("failed status change audited: %v", rows()[n:])
	}
}

func TestEntityBulkIsAuditedPerEntity(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	ids := []string{fid(11).String(), fid(12).String(), fid(13).String()}
	if rec := c.post("/admin/entities/bulk", url.Values{"action": {"archive"}, "id": ids}); rec.Code != 303 {
		t.Fatalf("bulk archive: %d", rec.Code)
	}
	wantAudit(t, rows(), "entity.archive", "entity.archive", "entity.archive")
	if rows()[0].Detail["bulk"] != true {
		t.Errorf("bulk flag missing: %v", rows()[0].Detail)
	}
	if rec := c.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": ids}); rec.Code != 303 {
		t.Fatalf("bulk delete: %d", rec.Code)
	}
	got := rows()
	wantAudit(t, got, "entity.archive", "entity.archive", "entity.archive", "entity.delete", "entity.delete", "entity.delete")
	if got[3].Detail["name"] != "Alpha service" {
		t.Errorf("delete lost the name (read before delete): %v", got[3].Detail)
	}
	// Already gone: every store call fails, nothing is recorded.
	c.post("/admin/entities/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": ids})
	if len(rows()) != 6 {
		t.Errorf("failed bulk delete audited: %d rows", len(rows()))
	}
}

func TestEntityDeleteIsAudited(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	if rec := c.post("/admin/entities/"+fid(12).String()+"/delete", url.Values{}); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	wantAudit(t, rows(), "entity.delete")
	if d := rows()[0]; d.Target != fid(12).String() || d.Detail["name"] != "Beta service" || d.Detail["schema"] != "service" {
		t.Errorf("row = %+v", d)
	}
	c.post("/admin/entities/"+fid(12).String()+"/delete", url.Values{})
	if len(rows()) != 1 {
		t.Error("deleting a missing entity was audited")
	}
}

func TestEntityEditFormIsAudited(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	form := url.Values{"name": {"Beta renamed"}, "slug": {"beta"}, "status": {"published"}, "attr.summary": {"New"}}
	if rec := c.post("/admin/entities/"+fid(12).String(), form); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	got := rows()
	wantAudit(t, got, "entity.update", "entity.publish")
	if got[0].Detail["name"] != "Beta renamed" || got[1].Detail["from"] != "draft" || got[1].Detail["to"] != "published" {
		t.Errorf("details = %v / %v", got[0].Detail, got[1].Detail)
	}
	// A save that changes no status is one edit row.
	form.Set("name", "Beta again")
	c.post("/admin/entities/"+fid(12).String(), form)
	wantAudit(t, rows(), "entity.update", "entity.publish", "entity.update")
	// A refused save writes nothing.
	form.Set("name", "")
	if rec := c.post("/admin/entities/"+fid(12).String(), form); rec.Code == 303 {
		t.Fatalf("empty name saved: %d", rec.Code)
	}
	if len(rows()) != 3 {
		t.Errorf("refused save audited: %d rows", len(rows()))
	}
}

func TestVersionRestoreIsAudited(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(11).String()
	c.post("/admin/entities/"+id, url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"Changed"}})
	rows := c.record()
	if rec := c.post("/admin/entities/"+id+"/versions/1/restore", nil); rec.Code != 303 {
		t.Fatalf("restore: %d", rec.Code)
	}
	wantAudit(t, rows(), "entity.restore_version")
	if rows()[0].Detail["version"] != 1 {
		t.Errorf("detail = %v", rows()[0].Detail)
	}
	if rec := c.post("/admin/entities/"+id+"/versions/99/restore", nil); rec.Code != 404 || len(rows()) != 1 {
		t.Errorf("missing version: %d, %d rows", rec.Code, len(rows()))
	}
}

func TestBlogSaveIsAuditedButAutosaveIsNot(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	rows := r.record()
	auto := url.Values{"version": {"1"}, "name": {"Autosaved title"}, "content": {"<p>Typing…</p>"}}
	if rec := r.post(r.path()+"/autosave", auto); rec.Code != 200 {
		t.Fatalf("autosave: %d", rec.Code)
	}
	if len(rows()) != 0 {
		t.Fatalf("autosave audited: %v", rows())
	}
	f := r.form(t)
	f.Set("content", "<p>Saved</p>")
	f.Set("_op", "save")
	if rec := r.post(r.path(), f); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	wantAudit(t, rows(), "entity.update")
	if rows()[0].Detail["schema"] != "post" {
		t.Errorf("detail = %v", rows()[0].Detail)
	}
	f = r.form(t)
	f.Set("_op", "publish")
	f.Set("status", string(entity.StatusPublished))
	r.post(r.path(), f)
	wantAudit(t, rows(), "entity.update", "entity.update", "entity.publish")
}

// A save that also schedules is an edit row and a distinct schedule row, not
// two identical "Edited" rows.
func TestScheduledSaveWritesADistinctScheduleRow(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	form := url.Values{"name": {"Beta"}, "slug": {"beta"}, "status": {"scheduled"}, "schedule": {"2099-01-02T10:00"}, "attr.summary": {"New"}}
	if rec := c.post("/admin/entities/"+fid(12).String(), form); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	wantAudit(t, rows(), "entity.update", "entity.schedule")
	if auditLabel("entity.schedule") == auditLabel("entity.update") || auditLabel("entity.schedule") == "entity.schedule" {
		t.Errorf("schedule label = %q", auditLabel("entity.schedule"))
	}
}
