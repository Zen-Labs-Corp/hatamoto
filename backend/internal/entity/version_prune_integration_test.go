//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

func versionPruneFixture(t *testing.T) (context.Context, *Service, *PostgresEntityRepository, uuid.UUID, func(string) uuid.UUID) {
	t.Helper()
	db := newIntegrationDB(t)
	ctx := context.Background()
	schemaID := uuid.New()
	slug := "vprune-" + uuid.NewString()[:8]
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`,
		schemaID, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	repo := NewPostgresEntityRepository(db)
	svc := NewService(NewPostgresSchemaRepository(db), repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// newEntity creates an entity with the given status.
	newEntity := func(status string) uuid.UUID {
		e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "v-" + uuid.NewString()[:8], Attributes: json.RawMessage(`{"n":0}`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET status = $2 WHERE id = $1`, e.ID, status); err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	return ctx, svc, repo, schemaID, newEntity
}

func versionNumbers(t *testing.T, ctx context.Context, repo *PostgresEntityRepository, id uuid.UUID) map[int]bool {
	t.Helper()
	vs, err := repo.ListVersions(ctx, id, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]bool{}
	for _, v := range vs {
		got[v.Version] = true
	}
	return got
}

func TestPruneVersionsKeepsLiveIntegration(t *testing.T) {
	ctx, _, repo, _, newEntity := versionPruneFixture(t)
	pool := repo.db.Pool()
	id := newEntity("draft")
	for v := 1; v <= 12; v++ {
		live := "NULL"
		switch v {
		case 2:
			live = "now() - interval '5 days'" // protected
		case 1:
			live = "now() - interval '40 days'" // live long ago: prunable
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			`INSERT INTO hatamoto_entity_versions (entity_id, version, attributes, live_at) VALUES ($1, $2, $3, %s)`, live),
			id, v, fmt.Sprintf(`{"n":%d}`, v+100)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.PruneVersions(ctx, 5); err != nil {
		t.Fatal(err)
	}
	got := versionNumbers(t, ctx, repo, id)
	for _, v := range []int{8, 9, 10, 11, 12, 2} {
		if !got[v] {
			t.Errorf("version %d was pruned, want kept", v)
		}
	}
	for _, v := range []int{1, 3, 4, 5, 6, 7} {
		if got[v] {
			t.Errorf("version %d kept, want pruned", v)
		}
	}
}

func TestPruneVersionsLiveDoNotUseKeepSlotsIntegration(t *testing.T) {
	ctx, _, repo, _, newEntity := versionPruneFixture(t)
	pool := repo.db.Pool()
	id := newEntity("draft")
	// The newest 5 are all live; the older 10 are not. keep=5 must still
	// retain 5 unprotected versions on top of the live ones.
	for v := 1; v <= 15; v++ {
		live := "NULL"
		if v > 10 {
			live = "now()"
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			`INSERT INTO hatamoto_entity_versions (entity_id, version, attributes, live_at) VALUES ($1, $2, $3, %s)`, live),
			id, v, fmt.Sprintf(`{"n":%d}`, v+100)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.PruneVersions(ctx, 5); err != nil {
		t.Fatal(err)
	}
	got := versionNumbers(t, ctx, repo, id)
	for v := 1; v <= 15; v++ {
		if want := v > 5; got[v] != want {
			t.Errorf("version %d kept=%v, want %v", v, got[v], want)
		}
	}
}

func TestPruneVersionsCoalesceSparesLiveIntegration(t *testing.T) {
	ctx, _, repo, _, newEntity := versionPruneFixture(t)
	pool := repo.db.Pool()
	id := newEntity("draft")
	// 1 and 2 are identical, as are 3 and 4: 1 is live (kept), 3 is not (coalesced).
	for v, live := range map[int]string{1: "now()", 2: "NULL", 3: "NULL", 4: "NULL"} {
		attrs := `{"n":1}`
		if v > 2 {
			attrs = `{"n":2}`
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			`INSERT INTO hatamoto_entity_versions (entity_id, version, attributes, live_at) VALUES ($1, $2, $3, %s)`, live),
			id, v, attrs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.PruneVersions(ctx, 50); err != nil {
		t.Fatal(err)
	}
	got := versionNumbers(t, ctx, repo, id)
	if !got[1] || !got[2] || got[3] || !got[4] {
		t.Fatalf("kept versions = %v, want 1, 2 and 4", got)
	}
}

func TestUpdateStampsLiveAtIntegration(t *testing.T) {
	ctx, svc, repo, _, newEntity := versionPruneFixture(t)
	for _, tc := range []struct {
		status string
		live   bool
	}{{"published", true}, {"draft", false}} {
		id := newEntity(tc.status)
		attrs := json.RawMessage(`{"n":1}`)
		if _, err := svc.UpdateEntity(ctx, id, nil, &UpdateEntityInput{Attributes: &attrs}); err != nil {
			t.Fatal(err)
		}
		vs, err := repo.ListVersions(ctx, id, 10)
		if err != nil || len(vs) != 1 {
			t.Fatalf("%s: versions = %d, err %v", tc.status, len(vs), err)
		}
		if (vs[0].LiveAt != nil) != tc.live {
			t.Errorf("%s: live_at = %v, want set=%v", tc.status, vs[0].LiveAt, tc.live)
		}
	}
}
