//go:build integration

package changeset

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// ids of one object of each kind, all created by plugin "alpha".
type ownedSet map[Kind]string

func (f *fixture) alphaObjects() ownedSet {
	f.t.Helper()
	run := uuid.New()
	cs, err := f.eng.Begin(f.ctx, run, "alpha")
	if err != nil {
		f.t.Fatal(err)
	}
	f.stage(cs, f.entityItem("", "own-"+f.tag, "x"))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-own"})})
	f.stage(cs, Item{Kind: KindMenu, Op: OpUpsert, Payload: raw(map[string]any{"name": "M", "slug": f.tag + "-own"})})
	f.stage(cs, Item{Kind: KindRedirect, Op: OpUpsert, Payload: raw(map[string]any{"source_path": "/" + f.tag + "-own", "target": "/x"})})
	f.stage(cs, Item{Kind: KindMedia, Op: OpUpsert, BlobKeys: []string{f.stageFile(run, "o.png")}, Payload: raw(map[string]any{"filename": "o.png"})})
	f.publish(cs)

	set := ownedSet{}
	for kind, q := range map[Kind]string{
		KindEntity:   `SELECT id FROM hatamoto_entities WHERE schema_id = $1`,
		KindTerm:     `SELECT id FROM hatamoto_terms WHERE slug = $1`,
		KindMenu:     `SELECT id FROM hatamoto_menus WHERE slug = $1`,
		KindRedirect: `SELECT id FROM hatamoto_redirects WHERE source_path = $1`,
		KindMedia:    `SELECT id FROM hatamoto_media WHERE origin = 'plugin:alpha' AND original_filename = 'o.png' ORDER BY created_at DESC LIMIT 1`,
	} {
		var args []any
		switch kind {
		case KindEntity:
			args = []any{f.schema.ID}
		case KindTerm, KindMenu:
			args = []any{f.tag + "-own"}
		case KindRedirect:
			args = []any{"/" + f.tag + "-own"}
		}
		var id string
		if err := f.db.Pool().QueryRow(f.ctx, q, args...).Scan(&id); err != nil {
			f.t.Fatalf("%s: %v", kind, err)
		}
		set[kind] = id
	}
	return set
}

func TestChangesetOwnership(t *testing.T) {
	f := newFixture(t)
	objs := f.alphaObjects()
	for kind, tbl := range originTables {
		var origin string
		if err := f.db.Pool().QueryRow(f.ctx, `SELECT origin FROM `+tbl+` WHERE id = $1`, objs[kind]).Scan(&origin); err != nil || origin != "plugin:alpha" {
			t.Fatalf("%s origin = %q, %v", kind, origin, err)
		}
	}

	// Another plugin and the owner may neither update nor delete alpha's objects.
	for _, plugin := range []string{"beta", ""} {
		cs := f.begin(plugin)
		for kind, id := range objs {
			for _, op := range []string{OpUpsert, OpDelete} {
				err := f.eng.Stage(f.ctx, cs, Item{Kind: kind, Op: op, TargetID: id, Payload: raw(map[string]any{"name": "x"}), BlobKeys: []string{"x"}})
				if !errors.Is(err, ErrNotOwned) {
					t.Errorf("plugin %q %s %s: err = %v, want not_owned", plugin, op, kind, err)
				}
			}
		}
	}

	// Alpha's own objects are fine (media is create-only, so not media).
	cs := f.begin("alpha")
	for _, kind := range []Kind{KindEntity, KindTerm, KindMenu, KindRedirect} {
		if err := f.eng.Stage(f.ctx, cs, Item{Kind: kind, Op: OpDelete, TargetID: objs[kind]}); err != nil {
			t.Errorf("alpha delete own %s: %v", kind, err)
		}
	}

	// A plugin changeset may not touch an owner object.
	var ownerEntity string
	owner := f.begin("")
	f.stage(owner, f.entityItem("", "mine-"+f.tag, "x"))
	f.publish(owner)
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT id FROM hatamoto_entities WHERE slug = $1`, "mine-"+f.tag).Scan(&ownerEntity); err != nil {
		t.Fatal(err)
	}
	err := f.eng.Stage(f.ctx, f.begin("alpha"), Item{Kind: KindEntity, Op: OpUpsert, TargetID: ownerEntity, Payload: raw(map[string]any{"name": "x"})})
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("plugin update of an owner entity: %v, want not_owned", err)
	}

	// A singleton kind is refused at stage.
	if err := f.eng.Stage(f.ctx, f.begin("alpha"), Item{Kind: "robots", Op: OpUpsert}); !errors.Is(err, ErrSingleton) {
		t.Errorf("robots: %v, want ErrSingleton", err)
	}
}

// An object handed to the owner between stage and publish is skipped at publish.
func TestChangesetOwnershipHandOverCaughtAtPublish(t *testing.T) {
	f := newFixture(t)
	objs := f.alphaObjects()

	cs := f.begin("alpha")
	upd := f.entityItem("", "", "changed")
	upd.TargetID = objs[KindEntity]
	upd.Payload = raw(map[string]any{"attributes": map[string]any{"content": "changed"}})
	f.stage(cs, upd)
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, TargetID: objs[KindTerm], Payload: raw(map[string]any{"name": "Renamed"})})

	// The owner edits the entity: it is theirs now.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET origin = 'owner' WHERE id = $1`, objs[KindEntity]); err != nil {
		t.Fatal(err)
	}
	res := f.publish(cs)
	if res.Applied != 1 || res.Skipped != 1 || len(res.Conflicts) != 1 || res.Conflicts[0].Reason != ReasonNotOwned || res.Conflicts[0].Kind != KindEntity {
		t.Fatalf("result = %+v", res)
	}
	var content, result string
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, objs[KindEntity]).Scan(&content)
	_ = f.db.Pool().QueryRow(f.ctx, `SELECT result FROM hatamoto_changeset_items WHERE changeset_id = $1 AND kind = 'entity'`, cs).Scan(&result)
	if content != "x" {
		t.Errorf("owner's entity was overwritten: %q", content)
	}
	if result != ResultSkippedOther {
		t.Errorf("item result = %q, want %s", result, ResultSkippedOther)
	}
	if n := f.count(`SELECT count(*) FROM hatamoto_terms WHERE name = 'Renamed' AND origin = 'plugin:alpha'`); n != 1 {
		t.Errorf("term update not applied (%d)", n)
	}
}
