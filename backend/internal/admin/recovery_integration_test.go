//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// recoveryRows counts the copies of an entity per user.
func (p *pgRig) recoveryRows(id uuid.UUID) map[uuid.UUID]bool {
	p.t.Helper()
	rows, err := p.db.Pool().Query(context.Background(), `SELECT user_id FROM hatamoto_admin_recovery_copies WHERE entity_id = $1`, id)
	if err != nil {
		p.t.Fatal(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			p.t.Fatal(err)
		}
		out[u] = true
	}
	return out
}

func (p *pgRig) putCopy(path, token, body string) {
	p.t.Helper()
	if rec := p.postJSON(path, token, body, nil); rec.Code != http.StatusNoContent {
		p.t.Fatalf("PUT %s = %d\n%s", path, rec.Code, rec.Body.String())
	}
}

// DATABASE_URL=... go test -tags=integration ./internal/admin -run Recovery
func TestBlogRecoveryAgainstPostgres(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	rec := p.post("/admin/entities", url.Values{"type": {"post"}, "name": {"Recovery post " + p.suffix}})
	if rec.Code != 303 {
		t.Skipf("no post type in this database (%d)", rec.Code)
	}
	id := p.locationID(rec)
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, id) })
	edit := "/admin/entities/" + id.String() + "/edit"
	owner, editor := p.owner.ID, p.editor.ID

	// Publish first: recovery copies are for published posts.
	f := parseForm(t, p.get(edit))
	f.Set("content", "<p>Live text</p>")
	f.Set("_op", "publish")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("publish: %d\n%s", rec.Code, rec.Body.String())
	}
	e, err := p.ents.GetEntity(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	// Both users keep a copy; the editor sees only its own in the document.
	p.putCopy(edit+"/recovery", "owner-token", `{"content":"<p>OWNER draft</p>","version":`+strconv.Itoa(e.Version)+`,"name":"Owner title","excerpt":"E","featured_image":"/m/a.jpg"}`)
	p.putCopy(edit+"/recovery", "editor-token", `{"content":"<p>EDITOR draft</p>","version":`+strconv.Itoa(e.Version)+`,"name":"Editor title"}`)
	if rows := p.recoveryRows(id); len(rows) != 2 || !rows[owner] || !rows[editor] {
		t.Fatalf("rows = %v", rows)
	}
	page := func(token string) string {
		rec := p.do("GET", edit, token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("GET editor as %s: %d", token, rec.Code)
		}
		return rec.Body.String()
	}
	ob, eb := page("owner-token"), page("editor-token")
	if !strings.Contains(ob, "OWNER draft") || strings.Contains(ob, "EDITOR draft") {
		t.Errorf("owner document carries the wrong copy")
	}
	if !strings.Contains(eb, "EDITOR draft") || strings.Contains(eb, "OWNER draft") {
		t.Errorf("editor document carries the wrong copy")
	}
	m := regexp.MustCompile(`(?s)id="hm-be-recovery">(.*?)</script>`).FindStringSubmatch(ob)
	var got map[string]any
	if m == nil || json.Unmarshal([]byte(m[1]), &got) != nil || got["name"] != "Owner title" || got["version"] != float64(e.Version) || got["featured_image"] != "/m/a.jpg" {
		t.Errorf("owner copy in the document = %v (%v)", got, m != nil)
	}

	// Discard removes the caller's row only.
	if rec := p.postJSON(edit+"/recovery/discard", "editor-token", ``, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("discard: %d", rec.Code)
	}
	if rows := p.recoveryRows(id); len(rows) != 1 || !rows[owner] {
		t.Fatalf("rows after the editor's discard = %v", rows)
	}
	p.putCopy(edit+"/recovery", "editor-token", `{"content":"<p>EDITOR again</p>","version":`+strconv.Itoa(e.Version)+`}`)

	// The owner's Update deletes the owner's row; the editor's copy stays.
	f = parseForm(t, p.get(edit))
	f.Set("content", "<p>Live text, updated</p>")
	f.Set("_op", "save")
	if rec := p.post(edit, f); rec.Code != 303 {
		t.Fatalf("update: %d\n%s", rec.Code, rec.Body.String())
	}
	if rows := p.recoveryRows(id); len(rows) != 1 || !rows[editor] {
		t.Fatalf("rows after the owner's update = %v, want only the editor's", rows)
	}

	// A refused save (stale version) keeps the copy.
	p.putCopy(edit+"/recovery", "owner-token", `{"content":"<p>again</p>","version":1}`)
	stale := parseForm(t, p.get(edit))
	stale.Set("version", "1")
	stale.Set("_op", "save")
	if rec := p.post(edit, stale); rec.Code != http.StatusConflict {
		t.Fatalf("stale update: %d", rec.Code)
	}
	if rows := p.recoveryRows(id); !rows[owner] {
		t.Error("a refused update deleted the copy")
	}

	// Deleting the entity cascades.
	if err := p.ents.DeleteEntity(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rows := p.recoveryRows(id); len(rows) != 0 {
		t.Errorf("rows after entity delete = %v", rows)
	}
}

func TestBuilderRecoveryAgainstPostgres(t *testing.T) {
	p := newPGRig(t)
	ctx := context.Background()
	rec := p.post("/admin/entities", url.Values{"type": {"page"}, "name": {"Recovery page " + p.suffix}})
	if rec.Code != 303 {
		t.Skipf("no page type in this database (%d)", rec.Code)
	}
	id := p.locationID(rec)
	p.cleanups = append(p.cleanups, func() { _ = p.ents.DeleteEntity(ctx, id) })
	base := "/admin/entities/" + id.String()
	owner, editor := p.owner.ID, p.editor.ID
	version := func() int {
		e, err := p.ents.GetEntity(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e.Version
	}
	copyBody := func(text string) string {
		doc, _ := json.Marshal(map[string]any{"v": 2, "nodes": []any{}})
		b, _ := json.Marshal(map[string]any{"content": string(doc), "version": version(), "name": text})
		return string(b)
	}
	save := func(op string) int {
		rec := p.postJSON(base+"/builder/save", "owner-token", saveBody(version(), saveDoc, op, ""), nil)
		return rec.Code
	}

	p.putCopy(base+"/builder/recovery", "owner-token", copyBody("OWNER copy"))
	p.putCopy(base+"/builder/recovery", "editor-token", copyBody("EDITOR copy"))
	if rows := p.recoveryRows(id); len(rows) != 2 || !rows[owner] || !rows[editor] {
		t.Fatalf("rows = %v", rows)
	}

	// The boot data of each user carries only that user's copy.
	boot := func(token string) (m struct {
		Recovery map[string]any `json:"recovery"`
	}) {
		rec := p.do("GET", base+"/edit", token, nil, nil)
		sub := bootRx.FindStringSubmatch(rec.Body.String())
		if rec.Code != 200 || sub == nil {
			t.Fatalf("builder as %s: %d", token, rec.Code)
		}
		if err := json.Unmarshal([]byte(sub[1]), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := boot("owner-token"); m.Recovery["name"] != "OWNER copy" {
		t.Errorf("owner boot = %v", m.Recovery)
	}
	if m := boot("editor-token"); m.Recovery["name"] != "EDITOR copy" {
		t.Errorf("editor boot = %v", m.Recovery)
	}

	// A draft autosave keeps the copies.
	if code := save("autosave"); code != http.StatusOK {
		t.Fatalf("autosave: %d", code)
	}
	if rows := p.recoveryRows(id); len(rows) != 2 {
		t.Fatalf("autosave changed the rows: %v", rows)
	}
	// A stale save is refused and keeps them.
	if rec := p.postJSON(base+"/builder/save", "owner-token", saveBody(version()-1, saveDoc, "save", ""), nil); rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d", rec.Code)
	}
	if rows := p.recoveryRows(id); len(rows) != 2 {
		t.Fatalf("a refused save changed the rows: %v", rows)
	}
	// A save deletes the saver's row only.
	if code := save("save"); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	if rows := p.recoveryRows(id); len(rows) != 1 || !rows[editor] {
		t.Fatalf("rows after the owner's save = %v, want only the editor's", rows)
	}

	// Discard removes the caller's row only.
	p.putCopy(base+"/builder/recovery", "owner-token", copyBody("again"))
	if rec := p.postJSON(base+"/builder/recovery/discard", "owner-token", ``, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("discard: %d", rec.Code)
	}
	if rows := p.recoveryRows(id); len(rows) != 1 || !rows[editor] {
		t.Fatalf("rows after the owner's discard = %v", rows)
	}
	// Builder content that is not a document object is refused.
	if rec := p.postJSON(base+"/builder/recovery", "owner-token", `{"content":"[1]","version":1}`, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-object content: %d", rec.Code)
	}
}
