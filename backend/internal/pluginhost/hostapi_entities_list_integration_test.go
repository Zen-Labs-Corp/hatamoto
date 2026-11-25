//go:build integration

package pluginhost

import (
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// seedMany inserts n published entities of the rig's schema with distinct created_at.
func seedMany(t *testing.T, r *termsITRig, n int) {
	t.Helper()
	r.exec(t, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale, attributes, published_at, created_at)
		SELECT s.id, 'bulk-' || g || '-' || $2::text, 'bulk-' || g || '-' || $2::text, 'published', hatamoto_site_language(),
		       '{}'::jsonb, now() - interval '1 day', now() - g * interval '1 second'
		  FROM hatamoto_entity_schemas s, generate_series(1, $3::int) g WHERE s.slug = $1`, r.schema, r.tag, n)
}

// A limit above the documented maximum is clamped to it, not dropped to the
// repository's fallback page size.
func TestEntitiesListLimitAboveMax(t *testing.T) {
	r := newTermsITRig(t)
	seedMany(t, r, 300)
	for _, c := range []struct{ limit, want int }{{500, entity.MaxListLimit}, {150, 150}, {0, 50}} {
		args := map[string]any{"schema_slug": r.schema}
		if c.limit > 0 {
			args["limit"] = c.limit
		}
		p := r.list(t, args)
		if len(p.Items) != c.want {
			t.Errorf("limit %d: got %d items, want %d", c.limit, len(p.Items), c.want)
		}
		if p.NextCursor == nil {
			t.Errorf("limit %d: next_cursor is nil with 300 entities", c.limit)
		}
	}
}

// next_cursor reaches every entity, and the last page has none.
func TestEntitiesListCursorPaging(t *testing.T) {
	r := newTermsITRig(t)
	seedMany(t, r, 300)
	seen := map[string]bool{}
	var cursor *string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("cursor did not terminate")
		}
		args := map[string]any{"schema_slug": r.schema, "limit": 100}
		if cursor != nil {
			args["cursor"] = *cursor
		}
		p := r.list(t, args)
		for _, it := range p.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			seen[it.ID] = true
		}
		if p.NextCursor == nil {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 300 {
		t.Fatalf("paged %d entities, want 300", len(seen))
	}
}
