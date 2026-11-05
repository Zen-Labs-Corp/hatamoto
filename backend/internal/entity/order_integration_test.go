//go:build integration

package entity

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (f *csFixture) orderEntity(name string, parent *uuid.UUID) *Entity {
	f.t.Helper()
	e, err := f.svc.CreateEntity(f.ctx, nil, &CreateEntityInput{
		SchemaID: f.schema.ID, Name: name, Slug: name + "-" + uuid.NewString()[:6], ParentID: parent,
		Attributes: json.RawMessage(`{"content":"x"}`),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

// order returns the names of the siblings under parent in manual order.
func (f *csFixture) order(parent *uuid.UUID) []string {
	f.t.Helper()
	rows, err := f.svc.OrderRows(f.ctx, f.schema.ID, f.rowLocale())
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if (r.ParentID == nil) == (parent == nil) && (parent == nil || *r.ParentID == *parent) {
			out = append(out, r.Name)
		}
	}
	return out
}

func (f *csFixture) rowLocale() string {
	var l string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT public.hatamoto_site_language()`).Scan(&l); err != nil {
		f.t.Fatal(err)
	}
	return l
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMoveEntityPersistsAndTiesAreStable(t *testing.T) {
	f := newCSFixture(t)
	a, _, c := f.orderEntity("A", nil), f.orderEntity("B", nil), f.orderEntity("C", nil)
	// All 0: ties break by created_at, id.
	if got := f.order(nil); !eq(got, []string{"A", "B", "C"}) {
		t.Fatalf("untouched order = %v", got)
	}
	res, err := f.svc.MoveEntity(f.ctx, c.ID, nil, MoveInput{Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Position != 1 || res.Total != 3 || res.Entity.SortOrder != 1 {
		t.Errorf("result = %d of %d, sort_order %d", res.Position, res.Total, res.Entity.SortOrder)
	}
	if got := f.order(nil); !eq(got, []string{"C", "A", "B"}) {
		t.Errorf("after move = %v", got)
	}
	// Position clamps; below 1 is last.
	if _, err := f.svc.MoveEntity(f.ctx, c.ID, nil, MoveInput{Position: 99}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.MoveEntity(f.ctx, a.ID, nil, MoveInput{Position: 0}); err != nil {
		t.Fatal(err)
	}
	if got := f.order(nil); !eq(got, []string{"B", "C", "A"}) {
		t.Errorf("after clamp = %v", got)
	}
	// A new row joins an ordered set last; the set stays untouched otherwise.
	d := f.orderEntity("D", nil)
	if d.SortOrder != 4 {
		t.Errorf("new row sort_order = %d, want 4", d.SortOrder)
	}
}

func TestNewRowsStayUnorderedWhileNothingIsOrdered(t *testing.T) {
	f := newCSFixture(t)
	if e := f.orderEntity("A", nil); e.SortOrder != 0 {
		t.Errorf("sort_order = %d, want 0", e.SortOrder)
	}
}

func TestMoveEntityLeavesPluginOwnedEntityAlone(t *testing.T) {
	f := newCSFixture(t)
	e := f.demoEntity("plug-" + uuid.NewString()[:6])
	f.orderEntity("Other", nil)
	// Age the row so a stray updated_at stamp shows.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET updated_at = now() - interval '48 hours', updated_by = NULL WHERE id = $1`, e.ID); err != nil {
		t.Fatal(err)
	}
	type state struct {
		version  int
		updated  time.Time
		by       *uuid.UUID
		versions int
	}
	read := func() (s state) {
		if err := f.db.Pool().QueryRow(f.ctx, `SELECT version, updated_at, updated_by,
			(SELECT count(*) FROM hatamoto_entity_versions WHERE entity_id = $1) FROM hatamoto_entities WHERE id = $1`, e.ID).
			Scan(&s.version, &s.updated, &s.by, &s.versions); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := read()

	res, err := f.svc.MoveEntity(f.ctx, e.ID, ptr(uuid.New()), MoveInput{Position: 99})
	if err != nil {
		t.Fatal(err)
	}
	if res.Entity.TookOwnership {
		t.Error("TookOwnership set by a reorder")
	}
	if got := f.origin(e.ID); got != demoOrigin {
		t.Errorf("origin = %q, want %q", got, demoOrigin)
	}
	after := read()
	if after.version != before.version || !after.updated.Equal(before.updated) || after.by != nil || after.versions != before.versions {
		t.Errorf("row changed: before %+v after %+v", before, after)
	}
}

func TestMoveEntityReparent(t *testing.T) {
	f := newCSFixtureSettings(t, json.RawMessage(`{"url_pattern":"/{path}"}`))
	root := f.orderEntity("Root", nil)
	k1 := f.orderEntity("K1", &root.ID)
	f.orderEntity("K2", &root.ID)
	loose := f.orderEntity("Loose", nil)

	// Re-parent lands last among the new siblings.
	if _, err := f.svc.MoveEntity(f.ctx, loose.ID, nil, MoveInput{ParentID: &root.ID, Position: 0}); err != nil {
		t.Fatal(err)
	}
	if got := f.order(&root.ID); !eq(got, []string{"K1", "K2", "Loose"}) {
		t.Errorf("children = %v", got)
	}
	// And into a given slot.
	if _, err := f.svc.MoveEntity(f.ctx, loose.ID, nil, MoveInput{ParentID: &root.ID, Position: 1}); err != nil {
		t.Fatal(err)
	}
	if got := f.order(&root.ID); !eq(got, []string{"Loose", "K1", "K2"}) {
		t.Errorf("children = %v", got)
	}
	if got := f.order(nil); !eq(got, []string{"Root"}) {
		t.Errorf("roots = %v", got)
	}

	// A cycle is refused and writes nothing.
	if _, err := f.svc.MoveEntity(f.ctx, root.ID, nil, MoveInput{ParentID: &k1.ID, Position: 1}); !errors.Is(err, ErrParentCycle) {
		t.Errorf("cycle err = %v", err)
	}
	got, _ := f.repo.GetByID(f.ctx, root.ID)
	if got.ParentID != nil {
		t.Error("cycle moved the row")
	}

	// Too deep is refused and writes nothing.
	prev := &root.ID
	for i := 0; i < 7; i++ {
		prev = &f.orderEntity("L", prev).ID
	}
	lone := f.orderEntity("Lone", nil)
	if _, err := f.svc.MoveEntity(f.ctx, lone.ID, nil, MoveInput{ParentID: prev, Position: 1}); !errors.Is(err, ErrParentTooDeep) {
		t.Errorf("depth err = %v", err)
	}
	got, _ = f.repo.GetByID(f.ctx, lone.ID)
	if got.ParentID != nil {
		t.Error("too deep moved the row")
	}
}

func TestMoveEntitySiblingsAreIsolated(t *testing.T) {
	f := newCSFixture(t)
	other := newCSFixture(t) // another schema
	_, b := f.orderEntity("A", nil), f.orderEntity("B", nil)
	x, y := other.orderEntity("X", nil), other.orderEntity("Y", nil)
	if _, err := f.svc.MoveEntity(f.ctx, b.ID, nil, MoveInput{Position: 1}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{x.ID, y.ID} {
		e, _ := other.repo.GetByID(other.ctx, id)
		if e.SortOrder != 0 {
			t.Errorf("another type's row changed: %d", e.SortOrder)
		}
	}
}

func TestMoveEntityConcurrentMoves(t *testing.T) {
	f := newCSFixture(t)
	var ids []uuid.UUID
	for _, n := range []string{"A", "B", "C", "D", "E", "F"} {
		ids = append(ids, f.orderEntity(n, nil).ID)
	}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.MoveEntity(f.ctx, id, nil, MoveInput{Position: len(ids) - i}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// Whatever the interleaving, the set is a clean 1..n numbering.
	rows, err := f.svc.OrderRows(f.ctx, f.schema.ID, f.rowLocale())
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if r.SortOrder != i+1 {
			t.Fatalf("row %d (%s) sort_order = %d, want %d", i, r.Name, r.SortOrder, i+1)
		}
	}
}

func TestDuplicateLandsAfterItsSourceInAnOrderedSet(t *testing.T) {
	f := newCSFixture(t)
	_, b := f.orderEntity("A", nil), f.orderEntity("B", nil)
	if _, err := f.svc.MoveEntity(f.ctx, b.ID, nil, MoveInput{Position: 1}); err != nil {
		t.Fatal(err)
	}
	cp, err := NewDuplicator(f.svc, nil).Duplicate(f.ctx, uuid.Nil, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.order(nil); !eq(got, []string{"B", cp.Name, "A"}) {
		t.Errorf("order = %v", got)
	}
}

// The public listing sorts by the stored order only when asked: the default
// order is the newest first, as before.
func TestPublicListingManualOrder(t *testing.T) {
	f := newCSFixture(t)
	var ids []uuid.UUID
	for _, n := range []string{"A", "B", "C"} {
		e := f.orderEntity(n, nil)
		if _, err := f.svc.UpdateEntityStatus(f.ctx, e.ID, nil, StatusPublished); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	// Spread the publish dates so "newest" is C, B, A.
	for i, id := range ids {
		if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET published_at = now() - make_interval(hours => $2) WHERE id = $1`, id, 10-i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.MoveEntity(f.ctx, ids[2], nil, MoveInput{Position: 1}); err != nil { // C A B
		t.Fatal(err)
	}
	repo := NewPostgresPublicRepository(f.db)
	names := func(order string) []string {
		items, _, err := repo.ListPublishedFiltered(f.ctx, ListFilter{SchemaSlugs: []string{f.schema.Slug}, Order: order}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range items {
			out = append(out, e.Name)
		}
		return out
	}
	for order, want := range map[string][]string{
		"manual":  {"C", "A", "B"},
		"-manual": {"B", "A", "C"},
		"":        {"C", "B", "A"},
	} {
		if got := names(order); !eq(got, want) {
			t.Errorf("order %q = %v, want %v", order, got, want)
		}
	}
}
