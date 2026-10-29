package entity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func strp(s string) *string { return &s }

func TestUpdateSchemaSEOSettingsKeepsUnknownKeys(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr, func(s *EntitySchema) {
		s.Settings = json.RawMessage(`{"url_pattern":"/docs/{slug}","future_key":{"a":[1,2]},"archive_title":"Old"}`)
	})

	err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{
		ArchiveTitle: strp("  Docs  "), ArchiveDescription: strp("All the docs"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(sr.schemas[existing.ID].Settings, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"url_pattern": `"/docs/{slug}"`, "future_key": `{"a":[1,2]}`,
		"archive_title": `"Docs"`, "archive_description": `"All the docs"`,
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
}

func TestUpdateSchemaSEOSettingsNilUntouchedEmptyDeletes(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr, func(s *EntitySchema) {
		s.Settings = json.RawMessage(`{"archive_title":"T","archive_description":"D"}`)
	})
	if err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{ArchiveTitle: strp("")}); err != nil {
		t.Fatal(err)
	}
	st, err := sr.schemas[existing.ID].URLSettings()
	if err != nil || st.ArchiveTitle != "" || st.ArchiveDescription != "D" {
		t.Fatalf("settings = %s, %v", sr.schemas[existing.ID].Settings, err)
	}
}

func TestUpdateSchemaSEOSettingsEmptySettings(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr, func(s *EntitySchema) { s.Settings = nil })
	if err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{ArchiveTitle: strp("Docs")}); err != nil {
		t.Fatal(err)
	}
	if st, _ := sr.schemas[existing.ID].URLSettings(); st.ArchiveTitle != "Docs" {
		t.Fatalf("settings = %s", sr.schemas[existing.ID].Settings)
	}
}

func TestUpdateSchemaSEOSettingsValidates(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr)
	long := make([]byte, MaxArchiveTitleLen+1)
	for i := range long {
		long[i] = 'x'
	}
	err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{ArchiveTitle: strp(string(long))})
	var se *SettingsError
	if !errors.As(err, &se) || se.Field != "archive_title" {
		t.Fatalf("err = %v", err)
	}
}

// A built-in type other than person refuses a settings-only write; the SEO
// page shows its archive values read-only.
func TestUpdateSchemaSEOSettingsBuiltinRefused(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr, func(s *EntitySchema) { s.IsBuiltin = true; s.Slug = "page" })
	err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{ArchiveTitle: strp("X")})
	if !errors.Is(err, ErrCannotModifyBuiltin) {
		t.Fatalf("err = %v, want ErrCannotModifyBuiltin", err)
	}
}

func TestUpdateSchemaSEOSettingsRejectsNonObject(t *testing.T) {
	t.Parallel()
	if _, err := patchSchemaSEOSettings(json.RawMessage(`[1]`), SchemaSEOPatch{ArchiveTitle: strp("X")}); err == nil {
		t.Fatal("want error")
	}
}
