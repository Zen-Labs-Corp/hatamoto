//go:build integration

package server

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
)

// TestAdminPluginCommenter: a commenter a plugin vouches for (no email) is
// listed with its origin, and export and erase work by id.
func TestAdminPluginCommenter(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := comment.New(env.db, env.cfg.Secret, logger)
	entities := entity.NewService(entity.NewPostgresSchemaRepository(env.db), entity.NewPostgresEntityRepository(env.db), logger)
	a := commentsAdmin{svc: svc, db: env.db, entities: entities}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	sch, err := entities.CreateSchema(ctx, &entity.CreateSchemaInput{
		Name: "Pc Adm", Slug: "pc-adm",
		Schema:   jsontext.Value(`{"type":"object","properties":{}}`),
		Settings: jsontext.Value(`{"url_pattern":"/pc-adm/{slug}"}`),
	})
	must(err)
	ent, err := entities.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: sch.ID, Name: "Pc Entity", Slug: "pc-entity", Attributes: jsontext.Value(`{}`)})
	must(err)

	id, _, err := svc.EnsurePluginCommenter(ctx, "forum", "acct-1")
	must(err)
	_, err = svc.Store(ctx, comment.Draft{
		EntityID: ent.ID, CommenterID: &id, AuthorName: "Pia", BodyRaw: "hello", BodyHTML: "<p>hello</p>",
		BodyHash: "h", SpamVerdict: "allow", Status: comment.StatusApproved, Locale: "und",
	})
	must(err)

	page, err := a.Commenters(ctx, "", 1)
	must(err)
	if len(page.Rows) != 1 || page.Rows[0].ID != id.String() || page.Rows[0].Origin != "forum" {
		t.Fatalf("Commenters = %+v, want one row with origin forum", page.Rows)
	}

	raw, err := a.ExportCommenter(ctx, id.String())
	must(err)
	out := string(raw)
	if !strings.Contains(out, `"origin_plugin": "forum"`) || !strings.Contains(out, `"email": ""`) || !strings.Contains(out, "hello") {
		t.Errorf("export = %s", out)
	}

	deleted, anonymised, err := a.EraseCommenter(ctx, id.String())
	must(err)
	if deleted != 1 || anonymised != 0 {
		t.Errorf("erase = %d deleted, %d anonymised, want 1, 0", deleted, anonymised)
	}
	if _, err := a.ExportCommenter(ctx, uuid.NewString()); err == nil {
		t.Error("export of an unknown commenter succeeded")
	}
}
