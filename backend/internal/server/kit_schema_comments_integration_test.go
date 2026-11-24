//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
)

// TestKitSchemaCommentsIntegration drives the kit seam against a migrated database.
func TestKitSchemaCommentsIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ents := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	seam := KitSchemaComments(comment.New(env.db, env.cfg.Secret, logger), env.db, ents)

	if _, err := seam.SchemaComments(ctx, "kit-nobody"); !errors.Is(err, entity.ErrSchemaNotFound) {
		t.Fatalf("unknown slug err = %v", err)
	}

	sch, err := ents.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Kit Story", Slug: "kit-story",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/kit/{slug}","comments":{"mode":"open"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := seam.SchemaComments(ctx, "kit-story")
	if err != nil || st.ID != sch.ID.String() || st.Builtin || st.HasPolicy || st.Order != "" || st.Policy.Enabled {
		t.Fatalf("fresh state = %+v, %v", st, err)
	}

	want := comment.Policy{Enabled: false, MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}
	if err := seam.SetSchemaComments(ctx, "kit-story", "rank", want); err != nil {
		t.Fatal(err)
	}
	// "rank" is a thread order a plugin applies; the seam stores it as Settings > Comments does.
	if st, err = seam.SchemaComments(ctx, "kit-story"); err != nil || st.Order != "rank" || st.Policy != want || !st.HasPolicy {
		t.Fatalf("state after write = %+v, %v", st, err)
	}
	kept, err := ents.GetSchema(ctx, sch.ID)
	if err != nil || !strings.Contains(string(kept.Settings), "url_pattern") || !strings.Contains(string(kept.Settings), `"mode"`) {
		t.Errorf("settings lost keys: %s, %v", kept.Settings, err)
	}

	bad := want
	bad.MaxDepth = 99
	before := string(kept.Settings)
	if err := seam.SetSchemaComments(ctx, "kit-story", "rank", bad); err == nil || !strings.Contains(err.Error(), "max_depth") {
		t.Errorf("max_depth 99 err = %v", err)
	}
	if after, _ := ents.GetSchema(ctx, sch.ID); string(after.Settings) != before {
		t.Errorf("settings changed on a refused write: %s", after.Settings)
	}

	var pageID string
	if err := env.db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entity_schemas (name, slug, schema, is_builtin) VALUES ('Kit Builtin', 'kit-builtin', '{"type":"object"}', true) RETURNING id`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	if st, err = seam.SchemaComments(ctx, "kit-builtin"); err != nil || !st.Builtin {
		t.Errorf("built-in state = %+v, %v", st, err)
	}
	if err := seam.SetSchemaComments(ctx, "kit-builtin", "", want); err == nil {
		t.Error("a built-in type must refuse the write")
	}
}
