//go:build integration

package server

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/user"
)

// completeProfile gives a test user the name and photo the admin asks for at
// first sign-in, so the profile gate lets their requests through, and removes
// the person again when the test ends. It writes with SQL: going through the
// entity service would load the URL registry before a test adds its schemas.
func completeProfile(t *testing.T, srv *Server, u *user.User) {
	t.Helper()
	ctx := context.Background()
	id, slug := uuid.New(), "it-person-"+uuid.NewString()[:8]
	if _, err := srv.db.Pool().Exec(ctx, `
		INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, published_at, attributes)
		SELECT $1, s.id, 'Test Person', $2, 'published', now(), '{"public": false, "photo": "/media/it.jpg"}'::jsonb
		FROM hatamoto_entity_schemas s WHERE s.slug = 'person'`, id, slug); err != nil {
		t.Fatalf("make the person of %s: %v", u.Email, err)
	}
	if _, err := srv.db.Pool().Exec(ctx, `INSERT INTO hatamoto_user_people (user_id, entity_id) VALUES ($1, $2)`, u.ID, id); err != nil {
		t.Fatalf("link the person of %s: %v", u.Email, err)
	}
	t.Cleanup(func() {
		_, _ = srv.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_entities WHERE id = $1`, id)
	})
}
