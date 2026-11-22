//go:build integration

package changeset

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"testing"
	"time"
)

// editOf stages a no-stage edit of the entity id with the given status and
// optional publish time, based on the live row.
func (f *fixture) editOf(id, status string, at *time.Time, content string) int64 {
	f.t.Helper()
	p := map[string]any{"schema": f.schema.Slug, "name": "Edited " + f.tag, "slug": "imp-" + f.tag,
		"attributes": map[string]any{"content": content}, "status": status}
	if at != nil {
		p["published_at"] = at
	}
	it := Item{Kind: KindEntity, Op: OpUpsert, TargetID: id, BaseToken: f.entityToken(id), Payload: raw(p)}
	cs, err := f.eng.SubmitEdit(f.ctx, EditOptions{Requester: "user-1"}, it)
	if err != nil {
		f.t.Fatal(err)
	}
	return cs
}

func (f *fixture) auditCount(action, target string) int {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = $1 AND target = $2`, action, target)
}

func TestEditPublishTakesOverAndAudits(t *testing.T) {
	f := newFixture(t)
	f.eng.AuditTx = auditTxViaRecord("system")
	id, _ := f.importedByAlpha()
	var origin string
	f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, id).Scan(&origin)
	if origin == "owner" {
		t.Fatal("fixture entity is already the owner's")
	}

	cs := f.editOf(id.String(), "published", nil, "two")
	f.publish(cs)

	f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, id).Scan(&origin)
	if origin != "owner" {
		t.Errorf("origin after the edit = %q, want owner", origin)
	}
	target := fmt.Sprintf("changeset:%d", cs)
	if n := f.auditCount("changeset.edit_publish", target); n != 1 {
		t.Errorf("edit_publish rows = %d, want 1", n)
	}
	if n := f.auditCount("changeset.publish", target); n != 0 {
		t.Errorf("publish rows for an edit = %d, want 0", n)
	}
	if n := f.auditCount("entity.take_over", id.String()); n != 1 {
		t.Errorf("take_over rows = %d, want 1", n)
	}

	// A second edit finds the entity the owner's: no more take_over rows.
	cs2 := f.editOf(id.String(), "published", nil, "three")
	f.publish(cs2)
	if n := f.auditCount("entity.take_over", id.String()); n != 1 {
		t.Errorf("take_over rows after a second edit = %d, want 1", n)
	}

	// A run keeps its action name.
	run := f.begin("")
	f.stage(run, f.entityItem(f.tag+"-run", f.tag+"-run", "x"))
	f.publish(run)
	if n := f.auditCount("changeset.publish", fmt.Sprintf("changeset:%d", run)); n != 1 {
		t.Errorf("run publish rows = %d, want 1", n)
	}
}

func TestEditPublishScheduledKeepsTheTime(t *testing.T) {
	f := newFixture(t)
	id, _ := f.importedByAlpha()
	at := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	f.publish(f.editOf(id.String(), "scheduled", &at, "later"))

	var status string
	var got *time.Time
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT status, published_at FROM hatamoto_entities WHERE id = $1`, id).Scan(&status, &got); err != nil {
		t.Fatal(err)
	}
	if status != "scheduled" || got == nil || !got.Equal(at) {
		t.Errorf("row = %s at %v, want scheduled at %v", status, got, at)
	}
}

func TestEditPublishStaleBaseIsAConflict(t *testing.T) {
	f := newFixture(t)
	id, _ := f.importedByAlpha()
	cs := f.editOf(id.String(), "published", nil, "mine")
	f.takeOver(id, "someone else wrote this first")

	res, err := f.eng.Publish(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 || len(res.Conflicts) != 1 {
		t.Fatalf("result = %+v, want one conflict", res)
	}
	var attrs jsontext.Value
	f.db.Pool().QueryRow(f.ctx, `SELECT attributes FROM hatamoto_entities WHERE id = $1`, id).Scan(&attrs)
	if string(attrs) != `{"content": "someone else wrote this first"}` && string(attrs) != `{"content":"someone else wrote this first"}` {
		t.Errorf("the live row changed: %s", attrs)
	}
}

func TestEditDiscardActionName(t *testing.T) {
	f := newFixture(t)
	id, _ := f.importedByAlpha()
	cs := f.editOf(id.String(), "published", nil, "x")
	var got []string
	f.eng.Audit = func(_ context.Context, action, _ string, _ map[string]any) { got = append(got, action) }
	if err := f.eng.Discard(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	run := f.begin("")
	if err := f.eng.Discard(f.ctx, run); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "changeset.edit_discard" || got[1] != "changeset.discard" {
		t.Errorf("discard actions = %v", got)
	}
}
