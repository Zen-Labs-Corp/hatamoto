//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

// membersFixture returns a service, a group store and a creator of base
// entities (status as given, set directly).
func membersFixture(t *testing.T) (*Service, *GroupStore, func(slug, name, locale, status string) uuid.UUID, *database.DB) {
	t.Helper()
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	mk := func(slug, name, locale, status string) uuid.UUID {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: name, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s/%s: %v", slug, locale, err)
		}
		if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = $2 WHERE id = $1`, e.ID, status); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	return svc, NewGroupStore(db.Pool()), mk, db
}

func TestGroupMembers(t *testing.T) {
	ctx := context.Background()
	_, gs, mk, _ := membersFixture(t)
	en, fr, es := mk("m", "Hello", "en", "published"), mk("m", "Bonjour", "fr", "draft"), mk("m", "Hola", "es", "published")
	if ms, err := gs.Members(ctx, en); err != nil || ms != nil {
		t.Fatalf("ungrouped Members = %v, %v; want nil", ms, err)
	}
	group, err := gs.Link(ctx, en, fr, es)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := gs.Members(ctx, fr)
	if err != nil || len(ms) != 3 {
		t.Fatalf("Members = %+v, %v; want 3", ms, err)
	}
	if ms[0].Locale != "en" || ms[1].Locale != "es" || ms[2].Locale != "fr" {
		t.Errorf("order = %s %s %s, want en es fr", ms[0].Locale, ms[1].Locale, ms[2].Locale)
	}
	for _, m := range ms {
		if m.Group != group || m.Slug != "m" || m.UpdatedAt.IsZero() || m.ContentChangedAt.Before(m.CreatedAt) {
			t.Errorf("member %+v", m)
		}
	}
	if ms[2].Status != "draft" || ms[2].Name != "Bonjour" {
		t.Errorf("draft member = %+v: want all statuses listed", ms[2])
	}
	by, err := gs.MembersFor(ctx, []uuid.UUID{group})
	if err != nil || len(by[group]) != 3 {
		t.Errorf("MembersFor = %v, %v", by, err)
	}
	if by, err := gs.MembersFor(ctx, nil); err != nil || len(by) != 0 {
		t.Errorf("MembersFor(nil) = %v, %v", by, err)
	}
}

func TestLinkCandidates(t *testing.T) {
	ctx := context.Background()
	_, gs, mk, db := membersFixture(t)
	en, fr := mk("c1", "Home page", "en", "published"), mk("c1", "Accueil", "fr", "published")
	it := mk("c1", "Startseite", "it", "published")
	enOther := mk("c2", "Home two", "en", "published")
	es := mk("c3", "Inicio", "es", "draft")
	shared := mk("c4", "Shared", "en", "published")
	// shared is untouched by Link; retag it to the shared locale below.
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET locale = $2 WHERE id = $1`, shared, LocaleShared); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Link(ctx, en, fr); err != nil {
		t.Fatal(err)
	}
	got, err := gs.LinkCandidates(ctx, en, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[uuid.UUID]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	// en and fr are taken by the group: only the ungrouped it and es remain.
	if !ids[it] || !ids[es] || ids[enOther] || ids[fr] || ids[en] {
		t.Errorf("candidates = %+v, want it and es only", got)
	}
	if got, _ := gs.LinkCandidates(ctx, en, "inicio", 50); len(got) != 1 || got[0].ID != es {
		t.Errorf("filtered = %+v, want es", got)
	}
	if got, _ := gs.LinkCandidates(ctx, en, "100%", 50); len(got) != 0 {
		t.Errorf("percent is literal: got %+v", got)
	}
	if got, _ := gs.LinkCandidates(ctx, en, "", 1); len(got) != 1 {
		t.Errorf("limit 1: got %d", len(got))
	}
	// An ungrouped entity excludes its own locale.
	if got, _ := gs.LinkCandidates(ctx, enOther, "", 50); func() bool {
		for _, c := range got {
			if c.Locale == "en" {
				return true
			}
		}
		return false
	}() {
		t.Errorf("ungrouped en entity offered an en candidate: %+v", got)
	}
}

func TestContentChangedAtFollowsAttributeEdits(t *testing.T) {
	ctx := context.Background()
	svc, gs, mk, _ := membersFixture(t)
	en, fr := mk("cc", "A", "en", "draft"), mk("cc", "B", "fr", "draft")
	if _, err := gs.Link(ctx, en, fr); err != nil {
		t.Fatal(err)
	}
	changed := func() time.Time {
		t.Helper()
		ms, err := gs.Members(ctx, en)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			if m.ID == en {
				return m.ContentChangedAt
			}
		}
		t.Fatal("en missing")
		return time.Time{}
	}
	before := changed()
	time.Sleep(20 * time.Millisecond)
	attrs := json.RawMessage(`{"title":"changed"}`)
	if _, err := svc.UpdateEntity(ctx, en, nil, &UpdateEntityInput{Attributes: &attrs}); err != nil {
		t.Fatal(err)
	}
	afterEdit := changed()
	if !afterEdit.After(before) {
		t.Errorf("attribute edit: %v not after %v", afterEdit, before)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := svc.UpdateEntityStatus(ctx, en, nil, StatusPublished); err != nil {
		t.Fatal(err)
	}
	if got := changed(); !got.Equal(afterEdit) {
		t.Errorf("publish moved content time: %v -> %v", afterEdit, got)
	}
}

// A group left with one member (entity delete, older Unlink) counts as no group.
func TestOneMemberGroupIsUngrouped(t *testing.T) {
	ctx := context.Background()
	_, gs, mk, db := membersFixture(t)
	en, fr, it, es := mk("s1", "Home", "en", "published"), mk("s1", "Accueil", "fr", "published"), mk("s1", "Casa", "it", "published"), mk("s1", "Inicio", "es", "published")
	if _, err := gs.Link(ctx, es, it); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, it); err != nil {
		t.Fatal(err)
	}
	// es is alone in its group; fr is free.
	got, err := gs.LinkCandidates(ctx, fr, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range got {
		found = found || c.ID == es
	}
	if !found {
		t.Errorf("candidates = %+v, want the stranded es page", got)
	}
	// Linking it into a real group must not trip over its old group.
	if _, err := gs.Link(ctx, en, fr); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Link(ctx, en, es); err != nil {
		t.Fatalf("link a one-member-group page into a group: %v", err)
	}
	if ms, _ := gs.Members(ctx, en); len(ms) != 3 {
		t.Errorf("members = %d, want 3", len(ms))
	}
}
