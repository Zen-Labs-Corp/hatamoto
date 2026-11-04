//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newDuplicateFixture(t *testing.T, settings string) (*Duplicator, *copyFixture) {
	t.Helper()
	f := newCopyFixture(t, settings)
	return NewDuplicator(f.svc, f.terms), f
}

func TestDuplicateCopiesContentIntoADraft(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	db := newIntegrationDB(t)
	var author uuid.UUID
	if err := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_authors (slug, name) VALUES ($1, 'Dup Author') RETURNING id`, "da-"+uuid.NewString()[:8]).Scan(&author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_authors WHERE id = $1`, author)
	})
	src, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: "Pricing", Slug: "pricing", Locale: "en",
		Attributes: json.RawMessage(`{"featured_image":"media-1","canonical_url":"/pricing","meta_title":"T","meta_description":"D"}`),
		Metadata:   json.RawMessage(`{"k":"v"}`),
		AuthorID:   &author,
	})
	if err != nil {
		t.Fatal(err)
	}
	termID := uuid.New()
	f.terms.mapped = []uuid.UUID{termID}

	cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID == src.ID || cp.Name != "Copy of Pricing" || cp.Slug != "pricing-copy" || cp.Status != StatusDraft || cp.PublishedAt != nil || cp.Locale != "en" {
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
	if attrs["featured_image"] != "media-1" || attrs["meta_title"] != "T" || attrs["meta_description"] != "D" {
		t.Errorf("attributes = %v", attrs)
	}
	if _, has := attrs["canonical_url"]; has {
		t.Errorf("canonical_url kept: %v", attrs)
	}
	if got.AuthorID == nil || *got.AuthorID != author {
		t.Errorf("author = %v", got.AuthorID)
	}
	var meta map[string]any
	_ = json.Unmarshal(got.Metadata, &meta)
	if meta["k"] != "v" {
		t.Errorf("metadata = %s", got.Metadata)
	}
	if ids := f.terms.replaced[cp.ID]; len(ids) != 1 || ids[0] != termID {
		t.Errorf("terms = %v", ids)
	}
}

func TestDuplicateSlugCollisions(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	src := f.page(t, "about", "en", `{}`, nil)
	for _, want := range []string{"about-copy", "about-copy-2", "about-copy-3"} {
		cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
		if err != nil || cp.Slug != want {
			t.Fatalf("slug = %v, %v; want %s", cp, err, want)
		}
	}
}

func TestDuplicateLeavesTranslationGroupAndKeepsParent(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, nestedSettings)
	parent := f.page(t, "company", "en", `{}`, nil)
	en := f.page(t, "pricing", "en", `{}`, &parent.ID)
	fr := f.page(t, "tarifs", "fr", `{}`, nil)
	if _, err := f.groups.Link(ctx, en.ID, fr.ID); err != nil {
		t.Fatal(err)
	}
	cp, err := d.Duplicate(ctx, uuid.Nil, en.ID)
	if err != nil {
		t.Fatal(err)
	}
	members, err := f.groups.Members(ctx, cp.ID)
	if err != nil || len(members) > 1 {
		t.Fatalf("members of the copy = %+v, %v; want only itself", members, err)
	}
	for _, m := range members {
		if m.ID != cp.ID {
			t.Errorf("copy grouped with %v", m.ID)
		}
	}
	if p := f.parentOf(t, cp.ID); p == nil || *p != parent.ID || cp.Locale != "en" {
		t.Errorf("parent = %v locale = %s", p, cp.Locale)
	}
	if again, err := f.groups.Members(ctx, en.ID); err != nil || len(again) != 2 {
		t.Errorf("source group = %+v, %v", again, err)
	}
}

func TestDuplicateIgnoresStatusAndDates(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	db := newIntegrationDB(t)
	for _, st := range []EntityStatus{StatusPublished, StatusScheduled} {
		src := f.page(t, "s-"+string(st), "en", `{}`, nil)
		if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = $2, published_at = now() + interval '1 day' WHERE id = $1`, src.ID, string(st)); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
		if err != nil || cp.Status != StatusDraft || cp.PublishedAt != nil {
			t.Errorf("%s source: copy = %+v, %v", st, cp, err)
		}
	}
}

func TestDuplicateRefusesPeopleAndReuseBlocks(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	db := newIntegrationDB(t)
	for _, slug := range []string{personSchemaSlug, BlockSchemaSlug} {
		var schemaID uuid.UUID
		err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_entity_schemas WHERE slug = $1 AND is_builtin`, slug).Scan(&schemaID)
		if errors.Is(err, pgx.ErrNoRows) {
			if err = db.Pool().QueryRow(ctx,
				`INSERT INTO hatamoto_entity_schemas (name, slug, schema, is_builtin) VALUES ($1,$1,'{"type":"object"}',true) RETURNING id`, slug).Scan(&schemaID); err == nil {
				t.Cleanup(func() {
					c := context.Background()
					_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
					_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
				})
			}
		}
		if err != nil {
			t.Fatalf("%s schema: %v", slug, err)
		}
		src, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "x " + slug, Slug: "dup-" + uuid.NewString()[:8], Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, src.ID)
		})
		if _, err := d.Duplicate(ctx, uuid.Nil, src.ID); !errors.Is(err, ErrDuplicateUnsupported) || !errors.Is(err, ErrValidationFailed) {
			t.Errorf("%s: err = %v", slug, err)
		}
	}
}

func TestDuplicateOfPluginOriginIsOwned(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	db := newIntegrationDB(t)
	src := f.page(t, "plug", "en", `{}`, nil)
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET origin = 'plugin:demo' WHERE id = $1`, src.ID); err != nil {
		t.Fatal(err)
	}
	cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	var origin string
	if err := db.Pool().QueryRow(ctx, `SELECT origin FROM hatamoto_entities WHERE id = $1`, cp.ID).Scan(&origin); err != nil || origin != "owner" {
		t.Errorf("origin = %q, %v", origin, err)
	}
}

func TestDuplicateTermsFailureKeepsTheCopy(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	src := f.page(t, "t", "en", `{}`, nil)
	f.terms.mapped = []uuid.UUID{uuid.New()}
	f.terms.fail = true
	cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
	if err != nil || cp == nil {
		t.Fatalf("copy = %v, %v", cp, err)
	}
}

func TestDuplicateOfImportedEntryDropsImportKey(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{}`)
	src, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{
		SchemaID: f.schemaID, Name: "Imported", Slug: "imported", Locale: "en",
		Attributes: json.RawMessage(`{}`),
		Metadata:   json.RawMessage(`{"k":"v","import_key":"wp:` + uuid.NewString() + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
	if err != nil || cp.Slug != "imported-copy" {
		t.Fatalf("copy = %+v, %v", cp, err)
	}
	got, err := f.svc.GetEntity(ctx, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.Unmarshal(got.Metadata, &meta)
	if _, has := meta["import_key"]; has || meta["k"] != "v" {
		t.Errorf("metadata = %s", got.Metadata)
	}
}

func TestDuplicateOfSharedEntryStaysShared(t *testing.T) {
	ctx := context.Background()
	d, f := newDuplicateFixture(t, `{"locale_neutral":true}`)
	useLocales(t, f.svc, "en", "fr")
	src := f.page(t, "shared", "", `{}`, nil)
	if src.Locale != LocaleShared {
		t.Fatalf("source locale = %q", src.Locale)
	}
	cp, err := d.Duplicate(ctx, uuid.Nil, src.ID)
	if err != nil || cp.Locale != LocaleShared || cp.Slug != "shared-copy" {
		t.Fatalf("copy = %+v, %v", cp, err)
	}
}
