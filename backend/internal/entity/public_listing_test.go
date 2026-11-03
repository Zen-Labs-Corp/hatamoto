//go:build integration

package entity

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// ExcludeSchemaSlugs keeps a type out of site search, total included.
func TestListPublishedFiltered_SearchExcludesSchemaSlugs(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	sfx := searchToken()
	id := uuid.New()
	name := "person-" + sfx
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes, published_at)
		SELECT $1, id, $2::text, $2::text, 'published', '{"public":true}'::jsonb, now() FROM hatamoto_entity_schemas WHERE slug = 'person'`,
		id, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, id) })

	repo := NewPostgresPublicRepository(db)
	for _, c := range []struct {
		exclude []string
		want    int
	}{{nil, 1}, {[]string{"post"}, 1}, {[]string{"person"}, 0}} {
		items, total, err := repo.ListPublishedFiltered(ctx, ListFilter{Search: sfx, ExcludeSchemaSlugs: c.exclude}, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != c.want || total != c.want {
			t.Errorf("%s exclude %v: %d items, total %d, want %d", fmt.Sprint(name), c.exclude, len(items), total, c.want)
		}
	}
}
