//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSchemaRepoSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewPostgresSchemaRepository(newIntegrationDB(t))

	body := json.RawMessage(`{"type":"object","properties":{}}`)
	created, err := repo.Create(ctx, &CreateSchemaInput{Name: "Settings Doc", Slug: "settings-doc-it", Schema: body})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(ctx, created.ID) })
	if string(created.Settings) != `{}` {
		t.Fatalf("default settings = %s, want {}", created.Settings)
	}

	newSettings := json.RawMessage(`{"url_pattern":"/docs/{slug}","archive_path":"/docs"}`)
	updated, err := repo.Update(ctx, created.ID, &UpdateSchemaInput{Settings: &newSettings})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	st, err := updated.URLSettings()
	if err != nil || st.URLPattern != "/docs/{slug}" || st.ArchivePath != "/docs" {
		t.Fatalf("after update: %+v %v", st, err)
	}

	// A partial update that omits settings must keep them.
	name := "Settings Doc 2"
	again, err := repo.Update(ctx, created.ID, &UpdateSchemaInput{Name: &name})
	if err != nil {
		t.Fatalf("update name: %v", err)
	}
	if st, _ := again.URLSettings(); st.URLPattern != "/docs/{slug}" {
		t.Fatalf("settings lost on partial update: %s", again.Settings)
	}

	for name, get := range map[string]func() (*EntitySchema, error){
		"GetByID":   func() (*EntitySchema, error) { return repo.GetByID(ctx, created.ID) },
		"GetBySlug": func() (*EntitySchema, error) { return repo.GetBySlug(ctx, "settings-doc-it") },
	} {
		s, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if st, _ := s.URLSettings(); st.ArchivePath != "/docs" {
			t.Fatalf("%s settings = %s", name, s.Settings)
		}
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, s := range list {
		if s.ID == created.ID {
			found = st.ArchivePath == "/docs" && len(s.Settings) > 2
		}
	}
	if !found {
		t.Fatal("List did not return settings")
	}
}
