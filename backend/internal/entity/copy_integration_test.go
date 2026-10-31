//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeCopyTerms maps every term to a fixed copy and records what it was asked.
type fakeCopyTerms struct {
	mapped   []uuid.UUID
	replaced map[uuid.UUID][]uuid.UUID
	fail     bool
}

func (f *fakeCopyTerms) MapTerms(context.Context, uuid.UUID, string) ([]uuid.UUID, error) {
	return f.mapped, nil
}

func (f *fakeCopyTerms) ReplaceEntityTerms(_ context.Context, id uuid.UUID, ids []uuid.UUID) error {
	if f.fail {
		return errors.New("terms down")
	}
	if f.replaced == nil {
		f.replaced = map[uuid.UUID][]uuid.UUID{}
	}
	f.replaced[id] = ids
	return nil
}

type copyFixture struct {
	copies   *CopyService
	svc      *Service
	groups   *GroupStore
	terms    *fakeCopyTerms
	schemaID uuid.UUID
	user     uuid.UUID // Nil: the system, as no user row exists
}

func newCopyFixture(t *testing.T, settings string) *copyFixture {
	t.Helper()
	svc, schemaID, _ := localeFixture(t, settings)
	db := newIntegrationDB(t)
	groups := NewGroupStore(db.Pool())
	terms := &fakeCopyTerms{}
	return &copyFixture{copies: NewCopyService(svc, groups, terms), svc: svc, groups: groups, terms: terms, schemaID: schemaID, user: uuid.Nil}
}

func (f *copyFixture) page(t *testing.T, slug, locale, attrs string, parent *uuid.UUID) *Entity {
	t.Helper()
	e, err := f.svc.CreateEntity(context.Background(), nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: slug, Slug: slug, Locale: locale, Attributes: json.RawMessage(attrs), ParentID: parent,
	})
	if err != nil {
		t.Fatalf("create %s/%s: %v", slug, locale, err)
	}
	return e
}

func TestCopyServiceCreateCopy(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	db := newIntegrationDB(t)
	var author uuid.UUID
	if err := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_authors (slug, name) VALUES ($1, 'Copy Author') RETURNING id`, "ca-"+uuid.NewString()[:8]).Scan(&author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_authors WHERE id = $1`, author)
	})

	src, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: "Pricing", Slug: "pricing", Locale: "en",
		Attributes: json.RawMessage(`{"featured_image":"media-1","canonical_url":"/pricing","meta_title":"T"}`),
		Metadata:   json.RawMessage(`{"k":"v"}`),
		AuthorID:   &author,
	})
	if err != nil {
		t.Fatal(err)
	}
	termID := uuid.New()
	f.terms.mapped = []uuid.UUID{termID}

	cp, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Locale != "fr" || cp.Status != StatusDraft || cp.Name != "Pricing" || cp.Slug != "pricing" {
		t.Fatalf("copy = %+v", cp)
	}
	got, err := f.svc.GetEntity(ctx, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	var attrs map[string]any
	if err := json.Unmarshal(got.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["featured_image"] != "media-1" || attrs["meta_title"] != "T" {
		t.Errorf("attributes = %v, want featured_image and meta_title kept", attrs)
	}
	if _, has := attrs["canonical_url"]; has {
		t.Errorf("canonical_url kept on the copy: %v", attrs)
	}
	if got.AuthorID == nil || *got.AuthorID != author {
		t.Errorf("author = %v, want %v", got.AuthorID, author)
	}
	var meta map[string]any
	_ = json.Unmarshal(got.Metadata, &meta)
	if meta["k"] != "v" {
		t.Errorf("metadata = %s", got.Metadata)
	}
	members, err := f.groups.Members(ctx, src.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	if ids := f.terms.replaced[cp.ID]; len(ids) != 1 || ids[0] != termID {
		t.Errorf("terms = %v, want [%v]", ids, termID)
	}

	// The locale is taken now, from either side.
	if _, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr"}); !errors.Is(err, ErrLocaleTaken) {
		t.Errorf("second fr copy: %v", err)
	}
	if _, err := f.copies.CreateCopy(ctx, f.user, cp.ID, CopyOptions{Locale: "en"}); !errors.Is(err, ErrLocaleTaken) {
		t.Errorf("en copy of the fr copy: %v", err)
	}
	for _, loc := range []string{"und", ""} {
		if _, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: loc}); !errors.Is(err, ErrValidationFailed) {
			t.Errorf("locale %q: %v, want a validation error", loc, err)
		}
	}
	var und int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND locale = 'und'`, f.schemaID).Scan(&und); err != nil || und != 0 {
		t.Errorf("und rows = %d (%v)", und, err)
	}
}

func TestCopyServiceFrom(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	en := f.page(t, "about", "en", `{"meta_title":"EN"}`, nil)
	de := f.page(t, "ueber", "pt", `{"meta_title":"DE"}`, nil)
	if _, err := f.groups.Link(ctx, en.ID, de.ID); err != nil {
		t.Fatal(err)
	}
	title := func(e *Entity) string {
		var a map[string]any
		_ = json.Unmarshal(e.Attributes, &a)
		s, _ := a["meta_title"].(string)
		return s
	}

	// "original" copies the group's original even when started from the German page.
	fromOrig, err := f.copies.CreateCopy(ctx, f.user, de.ID, CopyOptions{Locale: "fr", From: CopyFromOriginal})
	if err != nil {
		t.Fatal(err)
	}
	if fromOrig.Slug != "about" || title(fromOrig) != "EN" {
		t.Errorf("from original = %s/%s", fromOrig.Slug, title(fromOrig))
	}
	fromThis, err := f.copies.CreateCopy(ctx, f.user, de.ID, CopyOptions{Locale: "es", From: CopyFromThis})
	if err != nil {
		t.Fatal(err)
	}
	if fromThis.Slug != "ueber" || title(fromThis) != "DE" {
		t.Errorf("from this = %s/%s", fromThis.Slug, title(fromThis))
	}
	empty, err := f.copies.CreateCopy(ctx, f.user, de.ID, CopyOptions{Locale: "it", From: CopyFromEmpty, Name: "Chi siamo", Slug: "chi-siamo"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Slug != "chi-siamo" || title(empty) != "" {
		t.Errorf("empty = %s/%s", empty.Slug, title(empty))
	}
	if len(f.terms.replaced[empty.ID]) != 0 {
		t.Errorf("an empty page got terms: %v", f.terms.replaced[empty.ID])
	}
	if m, _ := f.groups.Members(ctx, en.ID); len(m) != 5 {
		t.Errorf("members = %d, want 5", len(m))
	}
}

func TestCopyServicePublish(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	src := f.page(t, "a", "en", `{}`, nil)
	draft, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr"})
	if err != nil || draft.Status != StatusDraft {
		t.Fatalf("draft = %+v, %v", draft, err)
	}
	pub, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "pt", Publish: true})
	if err != nil || pub.Status != StatusPublished {
		t.Fatalf("published = %+v, %v", pub, err)
	}
	got, _ := f.svc.GetEntity(ctx, pub.ID)
	if got.Status != StatusPublished {
		t.Errorf("stored status = %s", got.Status)
	}
}

func TestCopyServiceTermsFailureKeepsCopy(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	f.terms.mapped = []uuid.UUID{uuid.New()}
	f.terms.fail = true
	src := f.page(t, "a", "en", `{}`, nil)
	var warned string
	cp, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr", Warn: func(m string) { warned = m }})
	if err != nil || cp == nil {
		t.Fatalf("copy: %v", err)
	}
	if warned == "" {
		t.Error("no warning for the terms failure")
	}
	if m, _ := f.groups.Members(ctx, src.ID); len(m) != 2 {
		t.Errorf("members = %d, want the copy linked", len(m))
	}
}

// A link that fails leaves no row behind.
func TestCopyServiceLinkFailureLeavesNoOrphan(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	db := newIntegrationDB(t)
	src := f.page(t, "a", "en", `{}`, nil)
	if _, err := db.Pool().Exec(ctx, `
		CREATE OR REPLACE FUNCTION fail_entity_link() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'forced link failure'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_entity_link BEFORE UPDATE OF translation_group ON hatamoto_entities
		FOR EACH ROW EXECUTE FUNCTION fail_entity_link()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_entity_link ON hatamoto_entities; DROP FUNCTION IF EXISTS fail_entity_link()`)
	})
	if _, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr"}); err == nil {
		t.Fatal("expected the forced link failure")
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, f.schemaID).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d (%v), want only the source", n, err)
	}
}

func TestCopyServiceRefusesShared(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, `{}`)
	db := newIntegrationDB(t)
	src := f.page(t, "a", "en", `{}`, nil)
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET locale = $2 WHERE id = $1`, src.ID, LocaleShared); err != nil {
		t.Fatal(err)
	}
	if _, err := f.copies.CreateCopy(ctx, f.user, src.ID, CopyOptions{Locale: "fr"}); !errors.Is(err, ErrCopyUnsupported) {
		t.Errorf("shared source: %v", err)
	}
}
