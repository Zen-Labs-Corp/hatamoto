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

// Two archive patches on one schema row, started together while a test
// transaction holds the row lock, both survive.
func TestUpdateSchemaSEOSettingsConcurrentWithSchemaEdit(t *testing.T) {
	ctx := context.Background()
	svc, sc := newSEOIntegrationService(t)

	// Hold the row lock so both patches queue behind it, then release.
	release := make(chan struct{})
	locked := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- svc.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := svc.schemaRepo.GetByIDForUpdate(ctx, tx, sc.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

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
	close(release)
	if err := <-holderDone; err != nil {
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

// The editor write takes the same row lock: it waits for a holder, and an SEO
// patch committed around it keeps the editor's unrelated sitemap key.
func TestUpdateSchemaTakesTheSchemaRowLock(t *testing.T) {
	ctx := context.Background()
	svc, sc := newSEOIntegrationService(t)

	release := make(chan struct{})
	locked := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- svc.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := svc.schemaRepo.GetByIDForUpdate(ctx, tx, sc.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	edited := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		settings := json.RawMessage(`{"url_pattern":"/lock/{slug}","sitemap":{"priority":0.7}}`)
		_, err := svc.UpdateSchema(ctx, sc.ID, &UpdateSchemaInput{Settings: &settings})
		edited <- err
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("UpdateSchema finished while another transaction held the row lock")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if err := <-edited; err != nil {
		t.Fatal(err)
	}

	if err := svc.UpdateSchemaSEOSettings(ctx, sc.ID, SchemaSEOPatch{ArchiveTitle: strp("After edit")}); err != nil {
		t.Fatal(err)
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

	release := make(chan struct{})
	locked := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- svc.schemaRepo.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := svc.schemaRepo.GetByIDForUpdate(ctx, tx, sc.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	insertCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	created, err := svc.entityRepo.Create(insertCtx, nil, &CreateEntityInput{
		SchemaID: sc.ID, Name: "Lock probe", Attributes: json.RawMessage(`{}`),
	})
	close(release)
	if herr := <-holderDone; herr != nil {
		t.Fatal(herr)
	}
	if err != nil {
		t.Fatalf("entity insert blocked or failed while the schema row was locked: %v", err)
	}
	t.Cleanup(func() { _ = svc.entityRepo.Delete(ctx, created.ID) })
}
