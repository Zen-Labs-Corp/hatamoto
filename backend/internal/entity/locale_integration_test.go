//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func localeFixture(t *testing.T, settings string) (*Service, uuid.UUID, string) {
	t.Helper()
	db := newIntegrationDB(t)
	ctx := context.Background()
	schemaID := uuid.New()
	slug := "loc-" + uuid.NewString()[:8]
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, slug, settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	var site string
	if err := db.Pool().QueryRow(ctx, `SELECT public.hatamoto_site_language()`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewPostgresSchemaRepository(db), NewPostgresEntityRepository(db), testLogger())
	return svc, schemaID, site
}

// The same slug lives once per locale; a third write in a taken locale fails.
func TestCreateEntity_SameSlugTwoLocales(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, site := localeFixture(t, `{}`)
	mk := func(slug, locale string) (*Entity, error) {
		return svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: slug, Slug: slug, Locale: locale, Attributes: json.RawMessage(`{}`)})
	}
	en, err := mk("about", "en")
	if err != nil || en.Locale != "en" {
		t.Fatalf("en: %+v, %v", en, err)
	}
	fr, err := mk("about", "fr")
	if err != nil || fr.Locale != "fr" || fr.Slug != "about" {
		t.Fatalf("fr: %+v, %v", fr, err)
	}
	if _, err := mk("about", "en"); !errors.Is(err, ErrSlugAlreadyExists) || !strings.Contains(err.Error(), "locale") {
		t.Errorf("third en = %v, want ErrSlugAlreadyExists naming the locale", err)
	}
	if _, err := mk("about", "EN"); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("EN normalises to en and clashes: %v", err)
	}
	if _, err := mk("bad", "x"); !errors.Is(err, ErrValidationFailed) {
		t.Errorf("invalid locale = %v, want ErrValidationFailed", err)
	}

	// Empty locale is the site language, whatever it is.
	def, err := mk("plain", "")
	if err != nil || def.Locale != site {
		t.Fatalf("default locale = %+v, %v; want %q", def, err, site)
	}
	if got, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "tagged", Slug: "tagged", Locale: "en_au", Attributes: json.RawMessage(`{}`)}); err != nil || got.Locale != "en-AU" {
		t.Errorf("en_au -> %+v, %v; want en-AU", got, err)
	}

	// Update: nil leaves the locale, a tag changes it, a clash is refused.
	name := "About us"
	up, err := svc.UpdateEntity(ctx, fr.ID, nil, &UpdateEntityInput{Name: &name})
	if err != nil || up.Locale != "fr" {
		t.Fatalf("update nil locale: %+v, %v", up, err)
	}
	es := "es"
	up, err = svc.UpdateEntity(ctx, fr.ID, nil, &UpdateEntityInput{Locale: &es})
	if err != nil || up.Locale != "es" {
		t.Fatalf("update to es: %+v, %v", up, err)
	}
	enTag := "en"
	if _, err := svc.UpdateEntity(ctx, fr.ID, nil, &UpdateEntityInput{Locale: &enTag}); !errors.Is(err, ErrSlugAlreadyExists) {
		t.Errorf("update into a taken locale = %v, want ErrSlugAlreadyExists", err)
	}
	bad := "x"
	if _, err := svc.UpdateEntity(ctx, fr.ID, nil, &UpdateEntityInput{Locale: &bad}); !errors.Is(err, ErrValidationFailed) {
		t.Errorf("update invalid locale = %v, want ErrValidationFailed", err)
	}
	if got, err := svc.GetEntity(ctx, fr.ID); err != nil || got.Locale != "es" {
		t.Errorf("stored locale = %+v, %v", got, err)
	}
}

// patternConflict groups by slug and locale: a slug shared by two locales is
// not a conflict, a slug repeated under two parents in one locale is.
func TestPatternConflict_IgnoresOtherLocales(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{"url_pattern":"/{path}"}`)
	mk := func(slug, locale string, parent *uuid.UUID) *Entity {
		t.Helper()
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: slug, Slug: slug, Locale: locale, ParentID: parent, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatalf("create %s/%s: %v", slug, locale, err)
		}
		return e
	}
	p1, p2 := mk("p1", "en", nil), mk("p2", "en", nil)
	mk("solo", "en", nil)
	mk("solo", "fr", nil)
	mk("kid", "en", &p1.ID)
	mk("kid", "en", &p2.ID)

	flat := json.RawMessage(`{"url_pattern":"/notes/{slug}"}`)
	_, err := svc.UpdateSchema(ctx, schemaID, &UpdateSchemaInput{Settings: &flat})
	if err == nil {
		t.Fatal("a flat pattern with kid under two parents must fail")
	}
	if !strings.Contains(err.Error(), "kid") || strings.Contains(err.Error(), "solo") {
		t.Errorf("conflict names kid and not solo: %v", err)
	}
}
