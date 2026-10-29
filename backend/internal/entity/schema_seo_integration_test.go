//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func newSEOIntegrationService(t *testing.T) (*Service, *EntitySchema) {
	t.Helper()
	ctx := context.Background()
	db := newIntegrationDB(t)
	repo := NewPostgresSchemaRepository(db)
	svc := NewService(repo, NewPostgresEntityRepository(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	created, err := repo.Create(ctx, &CreateSchemaInput{
		Name: "Lock Doc", Slug: "lock-doc-it",
		Schema:   json.RawMessage(`{"type":"object","properties":{}}`),
		Settings: json.RawMessage(`{"url_pattern":"/lock/{slug}"}`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, created.ID) })
	return svc, created
}

func settingsOf(t *testing.T, svc *Service, id uuid.UUID) map[string]json.RawMessage {
	t.Helper()
	got, err := svc.GetSchema(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got.Settings, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// waitForSchemaLockWaiters polls (10s deadline) until n sessions wait on a lock
// in a statement that touches the schema table, so a holder is released only
// once every waiter really queues behind it.
func waitForSchemaLockWaiters(t *testing.T, n int) {
	t.Helper()
	db := newIntegrationDB(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got int
		err := db.Pool().QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE '%hatamoto_entity_schemas%' AND pid <> pg_backend_pid()`).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d session(s) waiting on the schema row lock, want %d", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdSchemaRow locks the schema row in a transaction until the returned
// release is called, which commits and reports the holder's error.
func holdSchemaRow(t *testing.T, svc *Service, id uuid.UUID) (release func() error) {
	t.Helper()
	ctx := context.Background()
	gate := make(chan struct{})
	locked := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- svc.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := svc.schemaRepo.GetByIDForUpdate(ctx, tx, id); err != nil {
				close(locked)
				return err
			}
			close(locked)
			<-gate
			return nil
		})
	}()
	<-locked
	var once sync.Once
	var result error
	release = func() error {
		once.Do(func() {
			close(gate)
			result = <-done
		})
		return result
	}
	t.Cleanup(func() { _ = release() }) // a failed test must not leave the lock held
	return release
}

// Two archive patches on one schema row, both blocked behind a test
// transaction that holds the row lock, both survive.
func TestUpdateSchemaSEOSettingsConcurrentWithSchemaEdit(t *testing.T) {
	ctx := context.Background()
	svc, sc := newSEOIntegrationService(t)
	release := holdSchemaRow(t, svc, sc.ID)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, p := range []SchemaSEOPatch{
		{ArchiveTitle: strp("Locked Title")},
		{ArchiveDescription: strp("Locked description")},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.UpdateSchemaSEOSettings(ctx, sc.ID, p)
		}()
	}
	waitForSchemaLockWaiters(t, 2)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	m := settingsOf(t, svc, sc.ID)
	if string(m["archive_title"]) != `"Locked Title"` || string(m["archive_description"]) != `"Locked description"` {
		t.Fatalf("a concurrent patch was lost: %v", m)
	}
	if string(m["url_pattern"]) != `"/lock/{slug}"` {
		t.Fatalf("url_pattern lost: %v", m)
	}
}

// The editor write and an archive patch both queue behind a holder of the row
// lock. The editor queues first, so it commits first and the patch then reads
// the editor's settings: the editor's sitemap key and the patch both survive.
func TestUpdateSchemaTakesTheSchemaRowLock(t *testing.T) {
	ctx := context.Background()
	svc, sc := newSEOIntegrationService(t)
	release := holdSchemaRow(t, svc, sc.ID)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		settings := json.RawMessage(`{"url_pattern":"/lock/{slug}","sitemap":{"priority":0.7}}`)
		_, err := svc.UpdateSchema(ctx, sc.ID, &UpdateSchemaInput{Settings: &settings})
		errs <- err
	}()
	waitForSchemaLockWaiters(t, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- svc.UpdateSchemaSEOSettings(ctx, sc.ID, SchemaSEOPatch{ArchiveTitle: strp("After edit")})
	}()
	waitForSchemaLockWaiters(t, 2)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	m := settingsOf(t, svc, sc.ID)
	if string(m["archive_title"]) != `"After edit"` || m["sitemap"] == nil {
		t.Fatalf("editor key or patch lost: %v", m)
	}
}

// The schema row lock must not block entity inserts: their foreign key takes
// FOR KEY SHARE on the schema row, which FOR NO KEY UPDATE does not conflict with.
func TestSchemaRowLockDoesNotBlockEntityInsert(t *testing.T) {
	ctx := context.Background()
	svc, sc := newSEOIntegrationService(t)

	release := holdSchemaRow(t, svc, sc.ID)

	insertCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	created, err := svc.entityRepo.Create(insertCtx, nil, &CreateEntityInput{
		SchemaID: sc.ID, Name: "Lock probe", Attributes: json.RawMessage(`{}`),
	})
	if herr := release(); herr != nil {
		t.Fatal(herr)
	}
	if err != nil {
		t.Fatalf("entity insert blocked or failed while the schema row was locked: %v", err)
	}
	t.Cleanup(func() { _ = svc.entityRepo.Delete(ctx, created.ID) })
}
