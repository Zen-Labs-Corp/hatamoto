//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
)

// condRig is a service on a real database with one schema that accepts any
// object, and a cleanup of everything it created.
func condRig(t *testing.T) (*Service, uuid.UUID) {
	t.Helper()
	db := newIntegrationDB(t)
	ctx := context.Background()
	schemaID := uuid.New()
	slug := "cond-" + uuid.NewString()[:8]
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
	svc := NewService(NewPostgresSchemaRepository(db), NewPostgresEntityRepository(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, schemaID
}

// condEdit stores attrs as a new version, conditional on version.
func condEdit(svc *Service, id uuid.UUID, version int, attrs string) (*Entity, error) {
	a := jsontext.Value(attrs)
	return svc.UpdateEntity(context.Background(), id, nil, &UpdateEntityInput{Attributes: &a, ExpectedVersion: &version})
}

// A restore and an update from the same base: exactly one stores.
func TestRestoreRacesUpdateIntegration(t *testing.T) {
	svc, schemaID := condRig(t)
	ctx := context.Background()
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "r", Attributes: jsontext.Value(`{"n":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	version := e.Version
	for round := 1; round <= 10; round++ {
		// Leave the live content different from version 1, so the restore stores.
		cur, err := condEdit(svc, e.ID, version, fmt.Sprintf(`{"n":1,"round":%d}`, round))
		if err != nil {
			t.Fatalf("round %d: setup: %v", round, err)
		}
		version = cur.Version

		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = svc.RestoreVersion(ctx, e.ID, nil, e.Version)
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = condEdit(svc, e.ID, version, fmt.Sprintf(`{"n":2,"round":%d}`, round))
		}()
		wg.Wait()

		ok, conflicts := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrVersionConflict):
				conflicts++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if ok != 1 || conflicts != 1 {
			t.Fatalf("round %d: %d stored, %d conflicts (errs %v)", round, ok, conflicts, errs)
		}
		got, err := svc.GetEntity(ctx, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Version != version+1 {
			t.Fatalf("round %d: version %d, want %d", round, got.Version, version+1)
		}
		version = got.Version
	}
}

// A status change conditional on a version another editor already moved past
// is refused and leaves the status alone.
func TestStatusIfAfterAttributeEditIntegration(t *testing.T) {
	svc, schemaID := condRig(t)
	ctx := context.Background()
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "s", Attributes: jsontext.Value(`{"n":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	n := e.Version
	moved, err := condEdit(svc, e.ID, n, `{"n":1}`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.UpdateEntityStatusIf(ctx, e.ID, nil, StatusPublished, &n)
	var vc *VersionConflictError
	if !errors.As(err, &vc) || vc.Current != moved.Version {
		t.Fatalf("stale status: err = %v, want conflict at %d", err, moved.Version)
	}
	got, err := svc.GetEntity(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDraft {
		t.Fatalf("status = %s after a refused change, want draft", got.Status)
	}

	cur := moved.Version
	pub, err := svc.UpdateEntityStatusIf(ctx, e.ID, nil, StatusPublished, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Status != StatusPublished || pub.Version != cur {
		t.Fatalf("published = %s at version %d, want published at %d", pub.Status, pub.Version, cur)
	}

	if _, err := svc.UpdateEntityStatusIf(ctx, uuid.New(), nil, StatusPublished, &cur); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("unknown entity: err = %v, want ErrEntityNotFound", err)
	}
}

// RestoreVersionIf refuses a stale precondition and, with the current one,
// stores the old content as a new version.
func TestRestoreIfIntegration(t *testing.T) {
	svc, schemaID := condRig(t)
	ctx := context.Background()
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "p", Attributes: jsontext.Value(`{"n":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := condEdit(svc, e.ID, e.Version, `{"n":1}`)
	if err != nil {
		t.Fatal(err)
	}
	v3, err := condEdit(svc, e.ID, v2.Version, `{"n":2}`)
	if err != nil {
		t.Fatal(err)
	}

	stale := v2.Version
	if _, err := svc.RestoreVersionIf(ctx, e.ID, nil, e.Version, &stale); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale restore: err = %v, want conflict", err)
	}
	got, err := svc.GetEntity(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != v3.Version {
		t.Fatalf("version = %d after a refused restore, want %d", got.Version, v3.Version)
	}

	cur := v3.Version
	restored, err := svc.RestoreVersionIf(ctx, e.ID, nil, e.Version, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != cur+1 {
		t.Fatalf("version = %d, want %d", restored.Version, cur+1)
	}
	var attrs struct {
		N int `json:"n"`
	}
	if err := jsonx.Unmarshal(restored.Attributes, &attrs, jsonx.StoredLoose); err != nil || attrs.N != 0 {
		t.Fatalf("attributes = %s (%v), want version 1's {\"n\":0}", restored.Attributes, err)
	}
}
