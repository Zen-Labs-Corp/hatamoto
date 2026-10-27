//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// A conditional update (ExpectedVersion) compares under the row lock: of two
// writers holding the same version exactly one stores.
func TestUpdateExpectedVersionIntegration(t *testing.T) {
	db := newIntegrationDB(t)
	ctx := context.Background()
	schemaID := uuid.New()
	slug := "expv-" + uuid.NewString()[:8]
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
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "v", Attributes: json.RawMessage(`{"n":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	update := func(version int, attrs string) error {
		a := json.RawMessage(attrs)
		_, err := svc.UpdateEntity(ctx, e.ID, nil, &UpdateEntityInput{Attributes: &a, ExpectedVersion: &version})
		return err
	}

	version := e.Version
	for round := 1; round <= 10; round++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = update(version, fmt.Sprintf(`{"n":%d,"r":%d}`, i, round))
			}()
		}
		wg.Wait()
		ok, conflicts := 0, 0
		for _, err := range errs {
			var vc *VersionConflictError
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrVersionConflict) && errors.As(err, &vc) && vc.Current == version+1:
				conflicts++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if ok != 1 || conflicts != 1 {
			t.Fatalf("round %d: %d stored, %d conflicts", round, ok, conflicts)
		}
		version++
	}
}
