//go:build integration

package changeset

import (
	"encoding/json/jsontext"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// importedByAlpha publishes one entity as plugin alpha under a source key.
func (f *fixture) importedByAlpha() (id uuid.UUID, changeset int64) {
	f.t.Helper()
	cs := f.begin("alpha")
	f.stage(cs, f.entityItem(f.tag+"-key", "imp-"+f.tag, "one"))
	f.publish(cs)
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE slug = $1`, "imp-"+f.tag).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id, cs
}

func (f *fixture) ownership(svc *OwnershipService, id uuid.UUID) *Ownership {
	f.t.Helper()
	o, err := svc.Get(f.ctx, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func (f *fixture) takeOver(id uuid.UUID, content string) {
	f.t.Helper()
	attrs := jsontext.Value(`{"content":"` + content + `"}`)
	if _, err := f.ent.UpdateEntity(f.ctx, id, nil, &entity.UpdateEntityInput{Attributes: &attrs}); err != nil {
		f.t.Fatal(err)
	}
}

func TestOwnershipPanel(t *testing.T) {
	f := newFixture(t)
	svc := &OwnershipService{DB: f.db}
	id, cs := f.importedByAlpha()

	o := f.ownership(svc, id)
	if o.State != OwnershipTracked || o.Plugin != "alpha" || o.SourceKey != f.tag+"-key" || o.ChangesetID != cs || o.PublishedAt == nil {
		t.Fatalf("tracked: %+v", o)
	}

	f.takeOver(id, "two")
	if o := f.ownership(svc, id); o.State != OwnershipYours || o.Plugin != "alpha" || o.ChangesetID != cs {
		t.Fatalf("yours: %+v", o)
	}

	var version int
	var updated time.Time
	read := func() {
		if err := f.db.Pool().QueryRow(f.ctx, `SELECT version, updated_at FROM hatamoto_entities WHERE id = $1`, id).Scan(&version, &updated); err != nil {
			t.Fatal(err)
		}
	}
	read()
	v0, u0 := version, updated

	plugin, err := svc.HandBack(f.ctx, id)
	if err != nil || plugin != "alpha" {
		t.Fatalf("HandBack = %q, %v", plugin, err)
	}
	read()
	if version != v0 || !updated.Equal(u0) {
		t.Errorf("hand-back moved version/updated_at: %d/%s -> %d/%s", v0, u0, version, updated)
	}
	if o := f.ownership(svc, id); o.State != OwnershipTracked {
		t.Errorf("after hand-back: %+v", o)
	}

	if _, err := svc.HandBack(f.ctx, id); !errors.Is(err, ErrNotHandedOver) {
		t.Errorf("second HandBack: %v, want ErrNotHandedOver", err)
	}
}

func TestOwnershipPanelNoMapping(t *testing.T) {
	f := newFixture(t)
	svc := &OwnershipService{DB: f.db}
	owner := f.begin("")
	f.stage(owner, f.entityItem("", "mine-"+f.tag, "x"))
	f.publish(owner)
	var id uuid.UUID
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE slug = $1`, "mine-"+f.tag).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if o := f.ownership(svc, id); o.State != OwnershipNone {
		t.Errorf("state = %q, want none", o.State)
	}
	if _, err := svc.HandBack(f.ctx, id); !errors.Is(err, ErrNoMapping) {
		t.Errorf("HandBack: %v, want ErrNoMapping", err)
	}
	if _, err := svc.HandBack(f.ctx, uuid.New()); !errors.Is(err, ErrNoMapping) {
		t.Errorf("HandBack of an unknown entity: %v, want ErrNoMapping", err)
	}
	if o := f.ownership(svc, uuid.New()); o.State != OwnershipNone {
		t.Errorf("unknown entity state = %q", o.State)
	}
}

// A mapping that points at a deleted entity is harmless.
func TestOwnershipPanelDeletedTarget(t *testing.T) {
	f := newFixture(t)
	svc := &OwnershipService{DB: f.db}
	id, _ := f.importedByAlpha()
	if _, err := f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if o := f.ownership(svc, id); o.State != OwnershipNone {
		t.Errorf("state = %q", o.State)
	}
	if _, err := svc.HandBack(f.ctx, id); !errors.Is(err, ErrNoMapping) {
		t.Errorf("HandBack: %v", err)
	}
}

// After a person edits a plugin's entity the plugin cannot stage a change to
// it; after the hand-back it can again.
func TestChangesetNotOwned(t *testing.T) {
	f := newFixture(t)
	svc := &OwnershipService{DB: f.db}
	id, _ := f.importedByAlpha()
	f.takeOver(id, "two")

	upd := Item{Kind: KindEntity, Op: OpUpsert, TargetID: id.String(), Payload: raw(map[string]any{"attributes": map[string]any{"content": "from plugin"}})}
	cs := f.begin("alpha")
	if err := f.eng.Stage(f.ctx, cs, upd); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("stage after hand-over: %v, want not_owned", err)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, cs); n != 0 {
		t.Errorf("%d items staged, want 0", n)
	}

	if _, err := svc.HandBack(f.ctx, id); err != nil {
		t.Fatal(err)
	}
	f.stage(cs, upd)
	if n := f.count(`SELECT count(*) FROM hatamoto_changeset_items WHERE changeset_id = $1`, cs); n != 1 {
		t.Errorf("%d items staged after hand-back, want 1", n)
	}
}
