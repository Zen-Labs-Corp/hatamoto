//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestGroupStore(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	gs := NewGroupStore(db.Pool())
	mk := func(slug, locale, status string) uuid.UUID {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: slug, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s/%s: %v", slug, locale, err)
		}
		// The service decides the initial status; set it directly.
		if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = $2 WHERE id = $1`, e.ID, status); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}

	en, fr, es := mk("a", "en", "published"), mk("a", "fr", "published"), mk("a", "es", "draft")
	enTwo, shared := mk("b", "en", "published"), mk("c", "en", "published")
	// The service does not accept und yet; retag the row directly.
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET locale = $2 WHERE id = $1`, shared, LocaleShared); err != nil {
		t.Fatal(err)
	}

	if g, err := gs.GroupOf(ctx, en); err != nil || g != nil {
		t.Fatalf("fresh GroupOf = %v, %v; want nil", g, err)
	}
	group, err := gs.Link(ctx, en, fr)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if g, _ := gs.GroupOf(ctx, fr); g == nil || *g != group {
		t.Errorf("GroupOf(fr) = %v, want %v", g, group)
	}

	// Extending reuses the group; the draft sibling is linked but not live.
	if g, err := gs.Link(ctx, fr, es); err != nil || g != group {
		t.Fatalf("extend = %v, %v; want %v", g, err, group)
	}
	sibs, err := gs.LiveSiblings(ctx, en, []string{"en", "fr", "es"})
	if err != nil || len(sibs) != 2 || sibs[0].Locale != "en" || sibs[1].Locale != "fr" || sibs[1].Slug != "a" || sibs[1].SchemaSlug == "" {
		t.Fatalf("siblings = %+v, %v; want published en, fr only", sibs, err)
	}
	if sibs, _ := gs.LiveSiblings(ctx, en, []string{"fr"}); len(sibs) != 1 || sibs[0].ID != fr {
		t.Errorf("tag filter = %+v", sibs)
	}

	// One row per locale, and und never joins.
	if _, err := gs.Link(ctx, en, enTwo); !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("two en = %v, want ErrInvalidGroup", err)
	}
	if _, err := gs.Link(ctx, enTwo, fr); !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("clash with existing member = %v, want ErrInvalidGroup", err)
	}
	if _, err := gs.Link(ctx, en, shared); !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("und = %v, want ErrInvalidGroup", err)
	}

	m, err := gs.GroupsFor(ctx, []uuid.UUID{en, fr, shared})
	if err != nil || len(m) != 2 || m[fr].Group != group || m[fr].Locale != "fr" {
		t.Errorf("GroupsFor = %+v, %v", m, err)
	}

	if err := gs.Unlink(ctx, fr); err != nil {
		t.Fatal(err)
	}
	if g, _ := gs.GroupOf(ctx, fr); g != nil {
		t.Errorf("after unlink GroupOf = %v", g)
	}
	if sibs, _ := gs.LiveSiblings(ctx, fr, []string{"en", "fr"}); len(sibs) != 0 {
		t.Errorf("unlinked siblings = %+v", sibs)
	}
}

// Two Links add the same locale to one group through different members; the
// group lock lets exactly one win. Repeated, each round released by a barrier.
func TestGroupStoreLinkRace(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	gs := NewGroupStore(db.Pool())
	mk := func(slug, locale string) uuid.UUID {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: slug, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s/%s: %v", slug, locale, err)
		}
		return e.ID
	}

	for round := 0; round < 15; round++ {
		m1, m2 := mk(fmt.Sprintf("m1-%d", round), "en"), mk(fmt.Sprintf("m2-%d", round), "fr")
		new1, new2 := mk(fmt.Sprintf("n1-%d", round), "es"), mk(fmt.Sprintf("n2-%d", round), "es")
		group, err := gs.Link(ctx, m1, m2)
		if err != nil {
			t.Fatalf("round %d seed link: %v", round, err)
		}

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, pair := range [][]uuid.UUID{{m1, new1}, {m2, new2}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = gs.Link(ctx, pair...)
			}()
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, ErrInvalidGroup):
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d links succeeded (%v), want exactly 1", round, ok, errs)
		}
		var n int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE translation_group = $1 AND locale = 'es'`, group).Scan(&n); err != nil || n != 1 {
			t.Fatalf("round %d: es rows in group = %d, %v; want 1", round, n, err)
		}
	}
}

// groupWriteFixture creates entities in the given locales, all published.
func groupWriteFixture(t *testing.T, locales ...string) (*GroupStore, func(id uuid.UUID) (group *uuid.UUID, original bool), []uuid.UUID, func(string, ...any)) {
	t.Helper()
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	ids := make([]uuid.UUID, len(locales))
	slug := "gw-" + uuid.NewString()[:8]
	for i, l := range locales {
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: slug + l, Slug: fmt.Sprintf("%s-%d", slug, i), Locale: l, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s: %v", l, err)
		}
		ids[i] = e.ID
	}
	read := func(id uuid.UUID) (*uuid.UUID, bool) {
		var g *uuid.UUID
		var o bool
		if err := db.Pool().QueryRow(ctx, `SELECT translation_group, translation_original FROM hatamoto_entities WHERE id = $1`, id).Scan(&g, &o); err != nil {
			t.Fatal(err)
		}
		return g, o
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	return NewGroupStore(db.Pool()), read, ids, exec
}

func TestLinkFlagsInitiatorAsOriginal(t *testing.T) {
	ctx := context.Background()
	gs, read, ids, _ := groupWriteFixture(t, "en", "fr")
	if _, err := gs.Link(ctx, ids[1], ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, o := read(ids[1]); !o {
		t.Error("first id not flagged")
	}
	if _, o := read(ids[0]); o {
		t.Error("second id flagged")
	}
}

func TestLinkJoinKeepsFlag(t *testing.T) {
	ctx := context.Background()
	gs, read, ids, _ := groupWriteFixture(t, "en", "fr", "es")
	if _, err := gs.Link(ctx, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	// The joiner comes first, but the group already exists: no flag moves.
	if _, err := gs.Link(ctx, ids[2], ids[1]); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false, false} {
		if _, o := read(ids[i]); o != want {
			t.Errorf("member %d flagged = %v, want %v", i, o, want)
		}
	}
	// A locale the group already has maps to ErrLocaleTaken, still an ErrInvalidGroup.
	gs2, _, ids2, _ := groupWriteFixture(t, "en", "fr", "fr")
	if _, err := gs2.Link(ctx, ids2[0], ids2[1]); err != nil {
		t.Fatal(err)
	}
	err := func() error { _, err := gs2.Link(ctx, ids2[0], ids2[2]); return err }()
	if !errors.Is(err, ErrLocaleTaken) || !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("clash with member = %v, want ErrLocaleTaken and ErrInvalidGroup", err)
	}
}

func TestUnlinkOriginalClearsFlag(t *testing.T) {
	ctx := context.Background()
	gs, read, ids, _ := groupWriteFixture(t, "en", "fr", "es")
	if _, err := gs.Link(ctx, ids[0], ids[1], ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := gs.Unlink(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if g, o := read(ids[0]); g != nil || o {
		t.Errorf("unlinked original: group %v flag %v", g, o)
	}
	for _, i := range []int{1, 2} {
		if g, o := read(ids[i]); g == nil || o {
			t.Errorf("remaining member %d: group %v flag %v; want grouped, unflagged", i, g, o)
		}
	}
}

func TestUnlinkDissolvesPair(t *testing.T) {
	ctx := context.Background()
	gs, read, ids, _ := groupWriteFixture(t, "en", "fr")
	if _, err := gs.Link(ctx, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := gs.Unlink(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	for i := range ids {
		if g, o := read(ids[i]); g != nil || o {
			t.Errorf("member %d: group %v flag %v; want dissolved", i, g, o)
		}
	}
	if err := gs.Unlink(ctx, ids[0]); err != nil {
		t.Errorf("unlink of an ungrouped entity = %v, want nil", err)
	}
}

func TestSetOriginalMovesFlag(t *testing.T) {
	ctx := context.Background()
	gs, read, ids, _ := groupWriteFixture(t, "en", "fr", "es")
	if _, err := gs.Link(ctx, ids[0], ids[1], ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := gs.SetOriginal(ctx, ids[2]); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, false, true} {
		if _, o := read(ids[i]); o != want {
			t.Errorf("member %d flagged = %v, want %v", i, o, want)
		}
	}
}

func TestSetOriginalUngrouped(t *testing.T) {
	gs, _, ids, _ := groupWriteFixture(t, "en")
	if err := gs.SetOriginal(context.Background(), ids[0]); !errors.Is(err, ErrNotGrouped) {
		t.Errorf("ungrouped = %v, want ErrNotGrouped", err)
	}
	if err := gs.SetOriginal(context.Background(), uuid.New()); !errors.Is(err, ErrNotGrouped) {
		t.Errorf("unknown id = %v, want ErrNotGrouped", err)
	}
}

func TestGroupWritesKeepUpdatedAt(t *testing.T) {
	ctx := context.Background()
	gs, _, ids, _ := groupWriteFixture(t, "en", "fr", "es")
	stamps := func() map[uuid.UUID]string {
		t.Helper()
		out := map[uuid.UUID]string{}
		for _, id := range ids {
			var s string
			if err := gs.db.QueryRow(ctx, `SELECT updated_at::text FROM hatamoto_entities WHERE id = $1`, id).Scan(&s); err != nil {
				t.Fatal(err)
			}
			out[id] = s
		}
		return out
	}
	// Pin a known updated_at inside one transaction with the trigger bypassed.
	tx, err := gs.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET updated_at = '2020-01-01T00:00:00Z' WHERE id = ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	before := stamps()
	if _, err := gs.Link(ctx, ids[0], ids[1], ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := gs.SetOriginal(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := gs.Unlink(ctx, ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := gs.Unlink(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	after := stamps()
	for _, id := range ids {
		if before[id] != after[id] {
			t.Errorf("updated_at of %s changed: %s -> %s", id, before[id], after[id])
		}
	}
}
