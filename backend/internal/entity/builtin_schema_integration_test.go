//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"
)

// The person type is the one built-in a site may extend (ISC-31, 32): fields can
// be added and settings edited, core fields stay, other built-ins stay shut.
func TestPersonSchemaExtension_Integration(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	repo := NewPostgresSchemaRepository(db)
	svc := NewService(repo, NewPostgresEntityRepository(db), slog.Default())
	person, err := svc.GetSchemaBySlug(ctx, PersonSchemaSlug)
	if err != nil {
		t.Fatal(err)
	}
	orig := person
	t.Cleanup(func() {
		_, _ = repo.Update(ctx, orig.ID, &UpdateSchemaInput{Schema: &orig.Schema, Settings: &orig.Settings})
	})

	// CoreFields is the seeded property set.
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(person.Schema, &doc); err != nil {
		t.Fatal(err)
	}
	var seeded []string
	for k := range doc.Properties {
		seeded = append(seeded, k)
	}
	slices.Sort(seeded)
	core := slices.Clone(PersonCoreFields)
	slices.Sort(core)
	if !slices.Equal(seeded, core) {
		t.Fatalf("PersonCoreFields %v differ from the seeded schema %v", core, seeded)
	}

	edit := func(f func(props map[string]any)) json.RawMessage {
		var m map[string]any
		_ = json.Unmarshal(person.Schema, &m)
		f(m["properties"].(map[string]any))
		b, _ := json.Marshal(m)
		return b
	}
	update := func(in *UpdateSchemaInput) error { _, err := svc.UpdateSchema(ctx, person.ID, in); return err }

	add := edit(func(p map[string]any) {
		p["pronouns"] = map[string]any{"type": "string", "title": "Pronouns", "x-hm-group": "Contact"}
	})
	if err := update(&UpdateSchemaInput{Schema: &add}); err != nil {
		t.Fatalf("adding a field: %v", err)
	}
	pattern := json.RawMessage(`{"url_pattern":"/team/{slug}","archive_title":"Our team","archive_crumb_label":"Team"}`)
	if err := update(&UpdateSchemaInput{Settings: &pattern}); err != nil {
		t.Fatalf("editing settings: %v", err)
	}
	got, _ := svc.GetSchemaBySlug(ctx, PersonSchemaSlug)
	if st, _ := got.URLSettings(); st.URLPattern != "/team/{slug}" || st.ArchiveTitle != "Our team" {
		t.Errorf("settings = %s", got.Settings)
	}
	person = got

	removed := edit(func(p map[string]any) { delete(p, "job_title") })
	retyped := edit(func(p map[string]any) { p["job_title"] = map[string]any{"type": "number"} })
	for name, in := range map[string]*UpdateSchemaInput{"remove": {Schema: &removed}, "retype": {Schema: &retyped}} {
		if err := update(in); !errors.Is(err, ErrCannotModifyBuiltin) {
			t.Errorf("%s a core field: %v, want ErrCannotModifyBuiltin", name, err)
		}
	}
	renamed := "People"
	if err := update(&UpdateSchemaInput{Name: &renamed}); !errors.Is(err, ErrCannotModifyBuiltin) {
		t.Errorf("rename: %v", err)
	}
	if err := svc.DeleteSchema(ctx, person.ID); !errors.Is(err, ErrCannotDeleteBuiltin) {
		t.Errorf("delete: %v", err)
	}
	if post, err := svc.GetSchemaBySlug(ctx, "post"); err == nil {
		if err := update2(ctx, svc, post, &pattern); !errors.Is(err, ErrCannotModifyBuiltin) {
			t.Errorf("another built-in type: %v, want ErrCannotModifyBuiltin", err)
		}
	}
}

func update2(ctx context.Context, svc *Service, s *EntitySchema, settings *json.RawMessage) error {
	_, err := svc.UpdateSchema(ctx, s.ID, &UpdateSchemaInput{Settings: settings})
	return err
}
