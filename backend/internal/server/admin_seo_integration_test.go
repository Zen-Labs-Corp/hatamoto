//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// seoRowText is the stored SEO settings row as text, "" when there is none.
func seoRowText(t *testing.T, env *wireEnv) string {
	t.Helper()
	var s string
	err := env.db.Pool().QueryRow(context.Background(), `SELECT settings::text FROM hatamoto_seo_settings WHERE id`).Scan(&s)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	return s
}

// A Titles & meta save that fails on its second archive patch leaves the first
// patch, the settings row and the cached settings exactly as they were; the
// same save without the failure writes everything.
func TestSEOTitlesSaveRollsBackAcrossSchemasAndSettings(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	repo := entity.NewPostgresSchemaRepository(env.db)
	svc := entity.NewService(repo, entity.NewPostgresEntityRepository(env.db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	a := seoAdmin{db: env.db, store: seo.NewStore(env.db.Pool()), entities: svc}

	var ids []entity.EntitySchema
	for _, slug := range []string{"seo-tx-a", "seo-tx-b"} {
		sc, err := repo.Create(ctx, &entity.CreateSchemaInput{
			Name: slug, Slug: slug,
			Schema:   json.RawMessage(`{"type":"object","properties":{}}`),
			Settings: json.RawMessage(`{"url_pattern":"/` + slug + `/{slug}"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Delete(ctx, sc.ID) })
		ids = append(ids, *sc)
	}
	// SaveTitles locks in id order, so the second patch is the larger id.
	first, second := ids[0], ids[1]
	if bytes.Compare(first.ID[:], second.ID[:]) > 0 {
		first, second = second, first
	}

	title := func(s string) *string { return &s }
	tooLong := title(strings.Repeat("x", entity.MaxArchiveTitleLen+1))
	setSep := func(s *seo.Settings) { s.Separator = "|" }

	before, err := a.store.Get(ctx) // also fills the cache
	if err != nil {
		t.Fatal(err)
	}
	rowBefore := seoRowText(t, env)
	settingsBefore := map[string]string{}
	for _, sc := range []entity.EntitySchema{first, second} {
		settingsBefore[sc.Slug] = string(sc.Settings)
	}

	err = a.SaveTitles(ctx, []admin.SEOTitlePatch{
		{ID: second.ID, Patch: entity.SchemaSEOPatch{ArchiveTitle: tooLong}},
		{ID: first.ID, Patch: entity.SchemaSEOPatch{ArchiveTitle: title("Fine")}},
	}, setSep)
	var pe *admin.SEOPatchError
	var se *entity.SettingsError
	if !errors.As(err, &pe) || pe.ID != second.ID || !errors.As(err, &se) {
		t.Fatalf("SaveTitles = %v, want the second patch refused with a settings error", err)
	}
	for _, sc := range []entity.EntitySchema{first, second} {
		got, err := svc.GetSchema(ctx, sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Settings) != settingsBefore[sc.Slug] {
			t.Errorf("%s settings changed: %s -> %s", sc.Slug, settingsBefore[sc.Slug], got.Settings)
		}
	}
	if got := seoRowText(t, env); got != rowBefore {
		t.Errorf("seo row changed: %q -> %q", rowBefore, got)
	}
	if got, _ := a.store.Get(ctx); got.Separator != before.Separator {
		t.Errorf("cached separator = %q, want %q", got.Separator, before.Separator)
	}

	if err := a.SaveTitles(ctx, []admin.SEOTitlePatch{
		{ID: second.ID, Patch: entity.SchemaSEOPatch{ArchiveTitle: title("Second")}},
		{ID: first.ID, Patch: entity.SchemaSEOPatch{ArchiveTitle: title("First")}},
	}, setSep); err != nil {
		t.Fatalf("SaveTitles: %v", err)
	}
	for _, c := range []struct {
		sc   entity.EntitySchema
		want string
	}{{first, "First"}, {second, "Second"}} {
		sc, want := c.sc, c.want
		got, err := svc.GetSchema(ctx, sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if st, _ := got.URLSettings(); st.ArchiveTitle != want {
			t.Errorf("%s archive title = %q, want %q", sc.Slug, st.ArchiveTitle, want)
		}
	}
	if got, _ := a.store.Get(ctx); got.Separator != "|" {
		t.Errorf("separator after save = %q, want the cache refreshed to |", got.Separator)
	}
}
