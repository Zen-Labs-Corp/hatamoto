package admin

import (
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/media"
)

func init() {
	// A media edit lists the fields it changed.
	auditDetailKeys["media.updated"] = append(auditDetailKeys["media.updated"], "changed")
}

// contentWritesAuditCount fails unless exactly want rows were written, and checks them.
func contentWritesAuditCount(t *testing.T, read func() []AuditEvent, want int) []AuditEvent {
	t.Helper()
	evs := read()
	if len(evs) != want {
		t.Fatalf("events = %+v, want %d", evs, want)
	}
	assertAuditSafe(t, evs)
	for _, e := range evs {
		if e.Actor != "owner@example.com" {
			t.Fatalf("row %+v: actor is not the owner", e)
		}
	}
	return evs
}

func TestContentWritesAuditMenus(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	read := auditCapture(g.a)

	// A refused create (duplicate name) writes nothing.
	fm.add("Main", "header")
	if rec := g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {"main"}}, nil); rec.Code != 422 {
		t.Fatalf("duplicate create: %d", rec.Code)
	}
	contentWritesAuditCount(t, read, 0)

	if rec := g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {"Footer links"}, "location": {"footer"}}, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	var created uuid.UUID
	for id, m := range fm.menus {
		if m.Name == "Footer links" {
			created = id
		}
	}
	ev := contentWritesAuditCount(t, read, 1)[0]
	if ev.Action != AuditMenuCreated || ev.Target != created.String() || ev.Detail["name"] != "Footer links" || ev.Detail["location"] != "footer" {
		t.Fatalf("create row = %+v", ev)
	}

	f := itemForm("Footer links", [4]string{"Home", "custom", "0", "/"})
	f.Set("location", "footer")
	if rec := g.do("POST", "/admin/theme/menus/"+created.String(), "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	evs := contentWritesAuditCount(t, read, 2)
	if evs[1].Action != AuditMenuUpdated || evs[1].Target != created.String() || evs[1].Detail["items"] != 1 || evs[1].Detail["name"] != "Footer links" {
		t.Fatalf("save row = %+v", evs[1])
	}

	fm.replaceErr = errors.New("write failed")
	g.do("POST", "/admin/theme/menus/"+created.String(), "owner-token", f, nil)
	contentWritesAuditCount(t, read, 2)

	if rec := g.do("POST", "/admin/theme/menus/"+created.String()+"/delete", "owner-token", nil, nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	evs = contentWritesAuditCount(t, read, 3)
	if evs[2].Action != AuditMenuDeleted || evs[2].Target != created.String() {
		t.Fatalf("delete row = %+v", evs[2])
	}
	g.do("POST", "/admin/theme/menus/"+created.String()+"/delete", "owner-token", nil, nil)
	contentWritesAuditCount(t, read, 3)
}

func TestContentWritesAuditRedirectsAnd404Log(t *testing.T) {
	t.Parallel()

	g, fr := newRedirectRig(t)
	read := auditCapture(g.a)

	// An invalid status code is refused without a row.
	if rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/a"}, "target": {"/b"}, "code": {"299"}}, nil); rec.Code != 422 {
		t.Fatalf("invalid create: %d", rec.Code)
	}
	contentWritesAuditCount(t, read, 0)

	if rec := g.do("POST", "/admin/settings/redirects", "owner-token", url.Values{"source": {"/a"}, "target": {"/b?token=abc"}, "code": {"301"}}, nil); rec.Code != 303 {
		t.Fatalf("create: %d", rec.Code)
	}
	var id uuid.UUID
	for k := range fr.rules {
		id = k
	}
	ev := contentWritesAuditCount(t, read, 1)[0]
	if ev.Action != AuditRedirectCreated || ev.Target != id.String() || ev.Detail["source"] != "/a" || ev.Detail["target"] != "/b" ||
		ev.Detail["status_code"] != 301 || ev.Detail["enabled"] != true {
		t.Fatalf("create row = %+v", ev)
	}

	g.do("POST", "/admin/settings/redirects/"+id.String()+"/edit", "owner-token", url.Values{"source": {"/a"}, "target": {"/c"}, "code": {"302"}, "active": {"1"}}, nil)
	evs := contentWritesAuditCount(t, read, 2)
	if evs[1].Action != AuditRedirectUpdated || evs[1].Target != id.String() || evs[1].Detail["target"] != "/c" || evs[1].Detail["status_code"] != 302 {
		t.Fatalf("save row = %+v", evs[1])
	}

	g.do("POST", "/admin/settings/redirects/"+id.String()+"/toggle", "owner-token", nil, nil)
	evs = contentWritesAuditCount(t, read, 3)
	if evs[2].Action != AuditRedirectUpdated || evs[2].Detail["enabled"] != false || len(evs[2].Detail) != 1 {
		t.Fatalf("toggle row = %+v", evs[2])
	}

	// Bulk: one row per affected redirect.
	other := fr.add("/x", "/y", 301)
	g.do("POST", "/admin/settings/redirects/bulk", "owner-token", url.Values{"action": {"on"}, "id": {id.String(), other.ID.String(), uuid.NewString()}}, nil)
	evs = contentWritesAuditCount(t, read, 5)
	if evs[3].Action != AuditRedirectUpdated || evs[3].Detail["enabled"] != true || evs[4].Target != other.ID.String() && evs[3].Target != other.ID.String() {
		t.Fatalf("bulk rows = %+v", evs[3:])
	}
	g.do("POST", "/admin/settings/redirects/bulk", "owner-token", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {other.ID.String()}}, nil)
	evs = contentWritesAuditCount(t, read, 6)
	if evs[5].Action != AuditRedirectDeleted || evs[5].Target != other.ID.String() {
		t.Fatalf("bulk delete row = %+v", evs[5])
	}

	g.do("POST", "/admin/settings/redirects/"+id.String()+"/delete", "owner-token", nil, nil)
	evs = contentWritesAuditCount(t, read, 7)
	if evs[6].Action != AuditRedirectDeleted || evs[6].Target != id.String() {
		t.Fatalf("delete row = %+v", evs[6])
	}
	g.do("POST", "/admin/settings/redirects/"+id.String()+"/delete", "owner-token", nil, nil) // gone: 404
	contentWritesAuditCount(t, read, 7)

	// 404 log: ignore and delete, nothing for a missing entry.
	e1, e2 := fr.addLog("/missing-1", 1), fr.addLog("/missing-2", 1)
	g.do("POST", "/admin/settings/redirects/404/"+e1.ID.String()+"/ignore", "owner-token", nil, nil)
	g.do("POST", "/admin/settings/redirects/404/"+e2.ID.String()+"/delete", "owner-token", nil, nil)
	evs = contentWritesAuditCount(t, read, 9)
	if evs[7].Action != AuditNotFoundIgnored || evs[7].Target != e1.ID.String() || evs[8].Action != AuditNotFoundDeleted || evs[8].Target != e2.ID.String() {
		t.Fatalf("404 rows = %+v", evs[7:])
	}
	g.do("POST", "/admin/settings/redirects/404/"+uuid.NewString()+"/ignore", "owner-token", nil, nil)
	g.do("POST", "/admin/settings/redirects/404/"+uuid.NewString()+"/delete", "owner-token", nil, nil)
	contentWritesAuditCount(t, read, 9)
}

func TestContentWritesAuditMedia(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	read := c.record()

	// A refused upload (not an allowed type) writes nothing.
	c.upload(t, "/admin/media/upload", map[string][]byte{"x.png": []byte("<html>")}, true)
	contentWritesAuditCount(t, read, 0)

	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"new.png": png1x1}, true)
	if rec.Code != 303 {
		t.Fatalf("upload: %d", rec.Code)
	}
	ev := contentWritesAuditCount(t, read, 1)[0]
	if ev.Action != AuditMediaUploaded || ev.Target == "" || ev.Detail["name"] != "new.png" || ev.Detail["mime"] != "image/png" ||
		ev.Detail["size"] != int64(len(png1x1)) || ev.Detail["folder"] != "/" {
		t.Fatalf("upload row = %+v", ev)
	}

	id := fid(51).String()
	if rec := c.post("/admin/media/"+id, url.Values{"alt_text": {"New alt"}, "caption": {""}, "credit": {""}}); rec.Code != 303 {
		t.Fatalf("update: %d", rec.Code)
	}
	evs := contentWritesAuditCount(t, read, 2)
	if got, _ := evs[1].Detail["changed"].([]string); evs[1].Action != AuditMediaUpdated || evs[1].Target != id || len(got) != 1 || got[0] != "alt_text" {
		t.Fatalf("update row = %+v", evs[1])
	}
	// Saving the same values again changes nothing: no row.
	c.post("/admin/media/"+id, url.Values{"alt_text": {"New alt"}, "caption": {""}, "credit": {""}})
	contentWritesAuditCount(t, read, 2)
	long := make([]byte, 501)
	for i := range long {
		long[i] = 'a'
	}
	if rec := c.post("/admin/media/"+id, url.Values{"alt_text": {string(long)}}); rec.Code != 422 {
		t.Fatalf("long alt: %d", rec.Code)
	}
	contentWritesAuditCount(t, read, 2)

	if rec := c.post("/admin/media/"+id+"/delete", nil); rec.Code != 303 {
		t.Fatalf("delete: %d", rec.Code)
	}
	evs = contentWritesAuditCount(t, read, 3)
	if evs[2].Action != AuditMediaDeleted || evs[2].Target != id || evs[2].Detail["name"] != "team.png" {
		t.Fatalf("delete row = %+v", evs[2])
	}

	// Bulk delete: one row per file.
	c.post("/admin/media/bulk", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {fid(52).String(), fid(53).String()}})
	evs = contentWritesAuditCount(t, read, 5)
	if evs[3].Action != AuditMediaDeleted || evs[3].Target != fid(52).String() || evs[3].Detail["name"] != "logo.png" || evs[4].Target != fid(53).String() {
		t.Fatalf("bulk rows = %+v", evs[3:])
	}
}

func TestContentWritesAuditMediaRebuild(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	fake := &fakeMediaFormats{state: media.State{Total: 40}}
	c.a.deps.MediaFormats = fake
	read := c.record()

	fake.err = media.ErrRebuildRunning
	c.post("/admin/media/rebuild", nil)
	contentWritesAuditCount(t, read, 0)

	fake.err = nil
	if rec := c.post("/admin/media/rebuild", nil); rec.Code != 303 {
		t.Fatalf("rebuild: %d", rec.Code)
	}
	ev := contentWritesAuditCount(t, read, 1)[0]
	if ev.Action != AuditMediaRebuilt || ev.Target != "media" || ev.Detail["count"] != 40 {
		t.Fatalf("rebuild row = %+v", ev)
	}
}
