package entity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
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

// PatchSchemaSEOTx runs no hooks (the caller fires SchemaChanged after its
// commit); UpdateSchemaSEOSettings fires them once, and not when it fails.
func TestPatchSchemaSEOTxRunsNoHooks(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr)
	calls := 0
	svc.SetOnSchemaChange(func() { calls++ })

	if err := svc.PatchSchemaSEOTx(context.Background(), nil, existing.ID, SchemaSEOPatch{ArchiveTitle: strp("Docs")}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("hook ran %d times inside the tx, want 0", calls)
	}
	svc.SchemaChanged()
	if calls != 1 {
		t.Fatalf("SchemaChanged ran the hook %d times, want 1", calls)
	}

	if err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{ArchiveTitle: strp("More")}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("hook calls = %d after a committed patch, want 2", calls)
	}
	if err := svc.UpdateSchemaSEOSettings(context.Background(), uuid.New(), SchemaSEOPatch{ArchiveTitle: strp("X")}); err == nil {
		t.Fatal("want error for a missing schema")
	}
	if calls != 2 {
		t.Fatalf("hook ran for a failed patch: %d calls", calls)
	}
}

func TestUpdateSchemaSEOSettingsTypeKeys(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr, func(s *EntitySchema) {
		s.Settings = json.RawMessage(`{"future_key":1}`)
	})
	off := false
	err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{
		SchemaOrgType: strp("Article"), ArticleType: strp("NewsArticle"), FAQFromContent: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(sr.schemas[existing.ID].Settings, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"future_key": `1`, "schema_org_type": `"Article"`, "article_type": `"NewsArticle"`, "faq_from_content": `false`,
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	st, _ := sr.schemas[existing.ID].URLSettings()
	if st.FAQEnabled() {
		t.Error("FAQ should be off")
	}

	on := true
	err = svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, SchemaSEOPatch{
		SchemaOrgType: strp(""), ArticleType: strp(""), FAQFromContent: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := string(sr.schemas[existing.ID].Settings); s != `{"future_key":1}` {
		t.Fatalf("settings = %s", s)
	}
}

func TestUpdateSchemaSEOSettingsRejectsBadTypes(t *testing.T) {
	t.Parallel()
	svc, sr, _ := newTestService()
	existing := seedSchema(t, sr)
	for field, p := range map[string]SchemaSEOPatch{
		"schema_org_type": {SchemaOrgType: strp("FAQPage")},
		"article_type":    {ArticleType: strp("WebPage")},
	} {
		err := svc.UpdateSchemaSEOSettings(context.Background(), existing.ID, p)
		var se *SettingsError
		if !errors.As(err, &se) || se.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}
