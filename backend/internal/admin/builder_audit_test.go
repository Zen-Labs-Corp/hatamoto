package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestEntityTxBuilderSaveWritesOneUpdateRow(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	rows := r.record()
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "save", "")); code != 200 || !out.OK {
		t.Fatalf("save: %d %+v", code, out)
	}
	wantAudit(t, rows(), "entity.update")
	if d := rows()[0].Detail; d["builder"] != true || d["schema"] == nil || d["name"] != "Builder only" {
		t.Errorf("detail = %v", d)
	}
}

func TestEntityTxBuilderAutosaveWritesNoRow(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	rows := r.record()
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "autosave", "")); code != 200 || !out.OK {
		t.Fatalf("autosave: %d %+v", code, out)
	}
	wantAudit(t, rows())
}

// A publish is two entity writes under one actor: an update, then the status
// change. Both rows are kept (one row per repository write).
func TestEntityTxBuilderPublishWritesUpdateAndPublish(t *testing.T) {
	t.Parallel()

	r := newSaveRig(t)
	rows := r.record()
	if code, out := r.save(t, r.page, saveBody(1, saveDoc, "publish", `,"name":"Our story"`)); code != 200 || !out.OK {
		t.Fatalf("publish: %d %+v", code, out)
	}
	wantAudit(t, rows(), "entity.update", "entity.publish")
}

func TestEntityTxBuilderMakeBlockWritesCreate(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	rows := r.record()
	doc := `{"v":2,"nodes":[{"id":"c1","t":"container","c":[{"id":"h1","t":"heading","p":{"text":"Hi","level":2}}]}]}`
	if rec := r.postJSON(r.url(r.page, "/builder/block"), "owner-token", `{"name":"Hero","nodeId":"c1","doc":`+doc+`}`, nil); rec.Code != http.StatusCreated {
		t.Fatalf("make block: %d %s", rec.Code, rec.Body)
	}
	wantAudit(t, rows(), "entity.create")
	if d := rows()[0].Detail; d["name"] != "Hero" || d["schema"] != "block" {
		t.Errorf("detail = %v", d)
	}
}

func TestEntityTxBuilderBlogLinkInsertWritesLinksInserted(t *testing.T) {
	t.Parallel()

	r := newBlogRig(t)
	rows := r.record()
	fl := newFakeLinking()
	r.a.deps.Growth.Linking = fl
	target := fid(11)
	fl.names[r.ent.ID] = "Go servers"
	fl.sugg = nil
	rec := r.post(r.path()+"/links/insert", url.Values{"content": {"<p>Go is live</p>"}, "target": {target.String()}, "anchor": {"Go"}})
	var ins struct {
		Inserted bool `json:"inserted"`
	}
	if rec.Code != 200 || jsonx.Unmarshal(rec.Body.Bytes(), &ins) != nil || !ins.Inserted || strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("insert: %d %s", rec.Code, rec.Body)
	}
	wantAudit(t, rows(), "entity.links_inserted")
	if rows()[0].Target != r.ent.ID.String() || rows()[0].Detail["count"] != 1 {
		t.Errorf("row = %+v", rows()[0])
	}
}
