//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
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
