//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// crossLocaleRig is a flagged (all-languages) type on a two-language site.
func crossLocaleRig(t *testing.T, settings string) (*Service, uuid.UUID, func(slug, locale string, parent *uuid.UUID) (*Entity, error)) {
	t.Helper()
	svc, schemaID, _ := localeFixture(t, settings)
	useLocales(t, svc, "en", "fr")
	mk := func(slug, locale string, parent *uuid.UUID) (*Entity, error) {
		return svc.CreateEntity(context.Background(), nil, &CreateEntityInput{
			SchemaID: schemaID, Name: slug, Slug: slug, Locale: locale, ParentID: parent, Attributes: jsontext.Value(`{}`),
		})
	}
	return svc, schemaID, mk
}

// An all-languages entry (no locale given) against an existing English one.
func TestCreateEntity_UndVsLanguageSlugRefused(t *testing.T) {
	_, _, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	if _, err := mk("about", "en", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := mk("about", "", nil); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Fatalf("und create over en = %v, want ErrSlugAlreadyExists", err)
	}
}

func TestCreateEntity_LanguageVsUndSlugRefused(t *testing.T) {
	_, _, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	und, err := mk("about", "", nil)
	if err != nil || und.Locale != LocaleShared {
		t.Fatalf("und create: %+v, %v", und, err)
	}
	if _, err := mk("about", "en", nil); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Fatalf("en create over und = %v, want ErrSlugAlreadyExists", err)
	}
}

func TestCreateEntity_TwoLanguagesSameSlugStillOK(t *testing.T) {
	_, _, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	for _, loc := range []string{"en", "fr"} {
		if _, err := mk("about", loc, nil); err != nil {
			t.Fatalf("%s: %v", loc, err)
		}
	}
}

func TestUpdateEntity_SlugOrLocaleChangeIntoClashRefused(t *testing.T) {
	ctx := context.Background()
	svc, _, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	if _, err := mk("about", "en", nil); err != nil {
		t.Fatal(err)
	}
	und, err := mk("other", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	slug := "about"
	if _, err := svc.UpdateEntity(ctx, und.ID, nil, &UpdateEntityInput{Slug: &slug}); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("slug change into clash = %v, want ErrSlugAlreadyExists", err)
	}

	// An existing clash (und beside en, written raw) does not excuse a locale
	// change: en -> fr still lands beside the und row.
	en2, err := mk("later", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.entityRepo.(*PostgresEntityRepository).db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (schema_id, name, slug, locale, status, attributes) SELECT schema_id, 'later', 'later', 'und', 'draft', '{}' FROM hatamoto_entities WHERE id = $1`,
		en2.ID); err != nil {
		t.Fatal(err)
	}
	fr := "fr"
	if _, err := svc.UpdateEntity(ctx, en2.ID, nil, &UpdateEntityInput{Locale: &fr}); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("locale change into clash = %v, want ErrSlugAlreadyExists", err)
	}
}

func TestUpdateEntity_NestedParentChangeIntoClashRefused(t *testing.T) {
	ctx := context.Background()
	svc, _, mk := crossLocaleRig(t, `{"locale_neutral":true,"url_pattern":"/{path}"}`)
	p1, err := mk("p1", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := mk("p2", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mk("kid", "en", &p1.ID); err != nil {
		t.Fatal(err)
	}
	// Same slug under another parent is fine across the split (nested scope).
	kid2, err := mk("kid", "", &p2.ID)
	if err != nil {
		t.Fatalf("und kid under another parent: %v", err)
	}
	if _, err := mk("kid", "", &p1.ID); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("und kid under the same parent = %v, want ErrSlugAlreadyExists", err)
	}
	if _, err := svc.UpdateEntity(ctx, kid2.ID, nil, &UpdateEntityInput{ParentID: &p1.ID}); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("parent change into clash = %v, want ErrSlugAlreadyExists", err)
	}
}

// A clash that already exists must not block unrelated edits.
func TestUpdateEntity_ExistingClashEditableWhenSlugLocaleUntouched(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	en, err := mk("about", "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	var undID uuid.UUID = uuid.New()
	if _, err := svc.entityRepo.(*PostgresEntityRepository).db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entities (id, schema_id, name, slug, locale, status, attributes) VALUES ($1,$2,'about','about','und','draft','{}')`,
		undID, schemaID); err != nil {
		t.Fatal(err)
	}
	name := "About us"
	for _, id := range []uuid.UUID{en.ID, undID} {
		if _, err := svc.UpdateEntity(ctx, id, nil, &UpdateEntityInput{Name: &name}); err != nil {
			t.Errorf("rename %s: %v", id, err)
		}
	}
}

func TestCreateEntity_UndVsLanguageSlugConcurrent(t *testing.T) {
	_, _, mk := crossLocaleRig(t, `{"locale_neutral":true}`)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, loc := range []string{"", "en"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = mk("race", loc, nil)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrSlugAlreadyExists):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 2 racing creates succeeded (%v), want exactly 1", ok, errs)
	}
}
