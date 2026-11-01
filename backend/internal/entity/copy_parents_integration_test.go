//go:build integration

package entity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

const nestedSettings = `{"url_pattern":"/{path}"}`

// memberIn returns id's group member in locale, nil when there is none.
func (f *copyFixture) memberIn(t *testing.T, id uuid.UUID, locale string) *Member {
	t.Helper()
	ms, err := f.groups.Members(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ms {
		if ms[i].Locale == locale {
			return &ms[i]
		}
	}
	return nil
}

func (f *copyFixture) parentOf(t *testing.T, id uuid.UUID) *uuid.UUID {
	t.Helper()
	e, err := f.svc.GetEntity(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e.ParentID
}

// Pricing under Products under Company: a fr copy of Pricing with parents
// creates drafts of both ancestors, each linked, outermost first.
func TestCopyServiceCreatesMissingParents(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	company := f.page(t, "company", "en", `{}`, nil)
	products := f.page(t, "products", "en", `{}`, &company.ID)
	pricing := f.page(t, "pricing", "en", `{}`, &products.ID)

	missing, err := f.copies.MissingParents(ctx, pricing.ID, "fr")
	if err != nil || len(missing) != 2 || missing[0].ID != company.ID || missing[1].ID != products.ID {
		t.Fatalf("missing = %+v, %v; want Company then Products", missing, err)
	}

	cp, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr", CreateParents: true, Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	prodFR, compFR := f.memberIn(t, products.ID, "fr"), f.memberIn(t, company.ID, "fr")
	if prodFR == nil || compFR == nil {
		t.Fatalf("parents not created: products=%v company=%v", prodFR, compFR)
	}
	if p := f.parentOf(t, cp.ID); p == nil || *p != prodFR.ID {
		t.Errorf("copy parent = %v, want %v", p, prodFR.ID)
	}
	if p := f.parentOf(t, prodFR.ID); p == nil || *p != compFR.ID {
		t.Errorf("products copy parent = %v, want %v", p, compFR.ID)
	}
	if p := f.parentOf(t, compFR.ID); p != nil {
		t.Errorf("company copy parent = %v, want none", p)
	}
	if prodFR.Status != string(StatusDraft) || compFR.Status != string(StatusDraft) {
		t.Errorf("parents %s/%s, want drafts even when the copy is published", prodFR.Status, compFR.Status)
	}
	if m := f.memberIn(t, pricing.ID, "fr"); m == nil || m.Status != string(StatusPublished) {
		t.Errorf("pricing copy = %+v, want published", m)
	}

	// Parents now exist: nothing is missing, and a new locale reuses nothing.
	if missing, _ := f.copies.MissingParents(ctx, pricing.ID, "fr"); len(missing) != 0 {
		t.Errorf("missing after create = %+v", missing)
	}
}

func TestCopyServiceReusesParentCopy(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	products := f.page(t, "products", "en", `{}`, nil)
	pricing := f.page(t, "pricing", "en", `{}`, &products.ID)
	prodFR, err := f.copies.CreateCopy(ctx, f.user, products.ID, CopyOptions{Locale: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr", CreateParents: true})
	if err != nil {
		t.Fatal(err)
	}
	if p := f.parentOf(t, cp.ID); p == nil || *p != prodFR.ID {
		t.Errorf("parent = %v, want the existing %v", p, prodFR.ID)
	}
	// Unticked or ticked, an existing parent copy is used.
	cp2, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "es", CreateParents: false})
	if err != nil {
		t.Fatal(err)
	}
	if p := f.parentOf(t, cp2.ID); p != nil {
		t.Errorf("es copy parent = %v, want top level (no es parent copy, parents off)", p)
	}
}

func TestCopyServiceWithoutParentsIsTopLevel(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	products := f.page(t, "products", "en", `{}`, nil)
	pricing := f.page(t, "pricing", "en", `{}`, &products.ID)
	cp, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if p := f.parentOf(t, cp.ID); p != nil {
		t.Errorf("parent = %v, want top level", p)
	}
	if f.memberIn(t, products.ID, "fr") != nil {
		t.Error("a parent copy was created although parents were off")
	}
}

// A slug clash fails the copy by default and retries once with the locale when
// AutoSlug is on. Parents made for a copy always retry.
func TestCopyServiceSlugClash(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	products := f.page(t, "products", "en", `{}`, nil)
	pricing := f.page(t, "pricing", "en", `{}`, &products.ID)
	// An unlinked fr page already owns /pricing at top level.
	f.page(t, "pricing", "fr", `{}`, nil)
	if _, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr"}); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Fatalf("clash = %v, want ErrSlugAlreadyExists", err)
	}
	cp, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr", AutoSlug: true})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Slug != "pricing-fr" {
		t.Errorf("slug = %q, want pricing-fr", cp.Slug)
	}

	// The parent's slug is taken by an unlinked fr page: its copy retries by itself.
	f.page(t, "products", "fr", `{}`, nil)
	other := f.page(t, "other", "en", `{}`, &products.ID)
	cp2, err := f.copies.CreateCopy(ctx, f.user, other.ID, CopyOptions{Locale: "fr", CreateParents: true})
	if err != nil {
		t.Fatalf("parent clash: %v", err)
	}
	parent, _ := f.svc.GetEntity(ctx, *f.parentOf(t, cp2.ID))
	if parent.Slug != "products-fr" {
		t.Errorf("parent slug = %q, want products-fr", parent.Slug)
	}
}

// A copy that fails after its parents were made deletes them again.
func TestCopyServiceFailureDeletesParents(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	db := newIntegrationDB(t)
	company := f.page(t, "company", "en", `{}`, nil)
	pricing := f.page(t, "pricing", "en", `{}`, &company.ID)
	// Only the link of the page named pricing fails; its parent links fine.
	if _, err := db.Pool().Exec(ctx, `
		CREATE OR REPLACE FUNCTION fail_pricing_link() RETURNS trigger AS $$
		BEGIN IF NEW.slug = 'pricing' THEN RAISE EXCEPTION 'forced link failure'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_pricing_link BEFORE UPDATE OF translation_group ON hatamoto_entities
		FOR EACH ROW EXECUTE FUNCTION fail_pricing_link()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_pricing_link ON hatamoto_entities; DROP FUNCTION IF EXISTS fail_pricing_link()`)
	})
	if _, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr", CreateParents: true}); err == nil {
		t.Fatal("expected the forced link failure")
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, f.schemaID).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows = %d (%v), want only the 2 sources", n, err)
	}
}

// A shared (und) ancestor serves every locale: the copy sits under it, it is
// not listed as missing and no copy of it is made.
func TestCopyServiceSharedParent(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	db := newIntegrationDB(t)
	docs := f.page(t, "docs", "en", `{}`, nil)
	install := f.page(t, "install", "en", `{}`, &docs.ID)
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET locale = $2 WHERE id = $1`, docs.ID, LocaleShared); err != nil {
		t.Fatal(err)
	}
	missing, err := f.copies.MissingParents(ctx, install.ID, "fr")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %+v, %v; want none", missing, err)
	}
	cp, err := f.copies.CreateCopy(ctx, f.user, install.ID, CopyOptions{Locale: "fr", CreateParents: true})
	if err != nil {
		t.Fatalf("copy under a shared parent: %v", err)
	}
	if p := f.parentOf(t, cp.ID); p == nil || *p != docs.ID {
		t.Errorf("parent = %v, want the shared page %v", p, docs.ID)
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1`, f.schemaID).Scan(&n); err != nil || n != 3 {
		t.Errorf("rows = %d (%v), want the 2 sources and the copy", n, err)
	}
}

// An ancestor of another schema ends the walk, but the parents below it that
// still need a copy stay listed and are made at the top level.
func TestCopyServiceForeignAncestorKeepsMissingParents(t *testing.T) {
	ctx := context.Background()
	f := newCopyFixture(t, nestedSettings)
	db := newIntegrationDB(t)
	otherSchema := uuid.New()
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		otherSchema, "fa-"+uuid.NewString()[:8], nestedSettings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, otherSchema)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, otherSchema)
	})
	root, err := f.svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: otherSchema, Name: "root", Slug: "root", Locale: "en", Attributes: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	products := f.page(t, "products", "en", `{}`, nil)
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET parent_id = $2 WHERE id = $1`, products.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	pricing := f.page(t, "pricing", "en", `{}`, &products.ID)
	missing, err := f.copies.MissingParents(ctx, pricing.ID, "fr")
	if err != nil || len(missing) != 1 || missing[0].ID != products.ID {
		t.Fatalf("missing = %+v, %v; want Products", missing, err)
	}
	cp, err := f.copies.CreateCopy(ctx, f.user, pricing.ID, CopyOptions{Locale: "fr", CreateParents: true})
	if err != nil {
		t.Fatal(err)
	}
	prodFR := f.memberIn(t, products.ID, "fr")
	if prodFR == nil {
		t.Fatal("no fr copy of Products")
	}
	if p := f.parentOf(t, cp.ID); p == nil || *p != prodFR.ID {
		t.Errorf("parent = %v, want the fr Products %v", p, prodFR.ID)
	}
}
