//go:build integration

package entity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPublicSlugTaken(t *testing.T) {
	f := newPublicLocaleFixture(t)
	ctx := context.Background()
	draft := "draft-" + uuid.NewString()[:8]
	if _, err := f.repo.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,attributes)
		SELECT $1, id, $2, $2, $3, 'draft', '{}' FROM hatamoto_entity_schemas WHERE slug = $4`,
		uuid.New(), draft, f.site, f.schemaSlug); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, schema, slug, locale string
		want                       bool
	}{
		{"site language by default", f.schemaSlug, f.sameSlug, "", true},
		{"explicit locale", f.schemaSlug, f.sameSlug, f.other, true},
		{"only in the other locale, default scope", f.schemaSlug, f.onlySlug, "", false},
		{"only in the other locale, that locale", f.schemaSlug, f.onlySlug, f.other, true},
		{"unknown slug", f.schemaSlug, "nope-" + uuid.NewString()[:8], "", false},
		{"other schema", "no-such-schema", f.sameSlug, "", false},
		{"a draft never counts", f.schemaSlug, draft, "", false},
	} {
		got, err := f.repo.SlugTaken(ctx, tc.schema, tc.slug, tc.locale)
		if err != nil || got != tc.want {
			t.Errorf("%s: taken=%v err=%v, want %v", tc.name, got, err, tc.want)
		}
	}
}
