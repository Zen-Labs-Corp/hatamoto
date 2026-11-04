//go:build integration

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteconfig"
)

// The review service against a real database: a staged changeset with a risky
// redirect and two suggestions. Publish waits for every decision, an approved
// site_settings suggestion is applied, an unsupported kind can only be
// rejected, and Undo restores the setting and leaves an edited page alone.
func TestChangesetReviewIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 6, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tag := uuid.NewString()[:8]
	schemas := entity.NewPostgresSchemaRepository(db)
	sc, err := schemas.Create(ctx, &entity.CreateSchemaInput{Name: "rv " + tag, Slug: "rv-" + tag,
		Schema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	entSvc := entity.NewService(schemas, entity.NewPostgresEntityRepository(db), logger)
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Review IT') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	store := siteconfig.NewStore(db, logger)
	orig, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	origTagline := orig.Tagline
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = store.Update(bg, siteconfig.UpdateInput{Tagline: &origTagline})
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_ = schemas.Delete(bg, sc.ID)
		_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_redirects WHERE source_path LIKE '/' || $1 || '%'`, tag)
	})

	eng := &changeset.Engine{DB: db, Logger: logger, Writers: map[changeset.Kind]changeset.KindWriter{
		changeset.KindEntity:   changeset.EntityWriter{Svc: entSvc},
		changeset.KindRedirect: changeset.RedirectWriter{Svc: redirect.NewService(db, logger)},
	}}
	runs := &plugrun.Store{DB: db}
	rev := &changesetReviewer{eng: eng, runs: runs, settings: store, audit: func(context.Context, string, string, map[string]any) {}}

	plugin := "rv" + tag
	run, err := runs.Create(ctx, plugrun.Run{Plugin: plugin, Name: "import"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_runs WHERE id = $1`, run.ID) })
	cs, err := eng.Begin(ctx, run.ID, plugin)
	if err != nil {
		t.Fatal(err)
	}
	item := func(key, slug string) changeset.Item {
		p, _ := json.Marshal(map[string]any{"schema": sc.Slug, "name": "N " + slug, "slug": slug, "attributes": map[string]any{"content": "x"}})
		return changeset.Item{Kind: changeset.KindEntity, Op: changeset.OpUpsert, SourceKey: key, Payload: p}
	}
	redirect, _ := json.Marshal(map[string]any{"source_path": "/" + tag + "-old", "target": "/elsewhere"})
	for _, it := range []changeset.Item{
		item("a-"+tag, "rv-a-"+tag), item("b-"+tag, "rv-b-"+tag),
		{Kind: changeset.KindRedirect, Op: changeset.OpUpsert, SourceKey: "r-" + tag, Payload: redirect},
	} {
		if err := eng.Stage(ctx, cs, it); err != nil {
			t.Fatal(err)
		}
	}
	newTagline := "Reviewed " + tag
	if err := runs.AddProposal(ctx, run.ID, "site_settings", json.RawMessage(`{"tagline":"`+newTagline+`"}`)); err != nil {
		t.Fatal(err)
	}
	if err := runs.AddProposal(ctx, run.ID, "url_patterns", json.RawMessage(`{"x":"/{slug}"}`)); err != nil {
		t.Fatal(err)
	}

	d, err := rev.Review(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if d.RunName != "import" || d.Items != 3 || d.Pending != 1 || d.CanPublish || len(d.Risky) != 1 || len(d.Suggestions) != 2 {
		t.Fatalf("review = %+v", d)
	}
	site, other := d.Suggestions[0], d.Suggestions[1]
	if !site.Applicable || len(site.Fields) != 1 || site.Fields[0].Old != origTagline || site.Fields[0].New != newTagline || other.Applicable {
		t.Fatalf("suggestions = %+v", d.Suggestions)
	}

	// Nothing is decided: publish is refused and writes nothing.
	if _, _, err := rev.Publish(ctx, cs); !errors.Is(err, admin.ErrReviewUndecided) {
		t.Fatalf("publish undecided: %v", err)
	}
	if err := rev.Decide(ctx, cs, []int{3}, "approved"); err != nil {
		t.Fatal(err)
	}
	// The items are decided, the suggestions are not.
	if _, _, err := rev.Publish(ctx, cs); !errors.Is(err, admin.ErrReviewUndecided) {
		t.Fatalf("publish with undecided suggestions: %v", err)
	}
	// An unsupported kind cannot be approved, only rejected.
	if err := rev.DecideSuggestion(ctx, cs, other.ID, plugrun.ProposalApproved); !errors.Is(err, admin.ErrReviewNotApplicable) {
		t.Fatalf("approve unsupported: %v", err)
	}
	if err := rev.DecideSuggestion(ctx, cs, other.ID, plugrun.ProposalRejected); err != nil {
		t.Fatal(err)
	}
	if err := rev.DecideSuggestion(ctx, cs, site.ID, plugrun.ProposalApproved); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := store.Get(ctx); cfg.Tagline != newTagline {
		t.Fatalf("tagline = %q, want the approved one", cfg.Tagline)
	}
	if d, _ = rev.Review(ctx, cs); !d.CanPublish {
		t.Fatalf("cannot publish: %+v", d)
	}
	applied, dropped, err := rev.Publish(ctx, cs)
	if err != nil || applied != 3 || dropped != 0 {
		t.Fatalf("publish = %d, %d, %v", applied, dropped, err)
	}

	// The owner edits one page, then undoes the changeset.
	if _, err := db.Pool().Exec(ctx, `UPDATE hatamoto_entities SET name = 'Owner edit' WHERE schema_id = $1 AND slug = $2`, sc.ID, "rv-b-"+tag); err != nil {
		t.Fatal(err)
	}
	pre, err := rev.UndoPreview(ctx, cs)
	if err != nil || pre.Applied != 3 || pre.EditedSince != 1 || pre.Settings != 1 {
		t.Fatalf("preview = %+v, %v", pre, err)
	}
	restored, kept, err := rev.Undo(ctx, cs)
	if err != nil || restored != 2 || kept != 1 {
		t.Fatalf("undo = %d, %d, %v", restored, kept, err)
	}
	if cfg, _ := store.Get(ctx); cfg.Tagline != origTagline {
		t.Errorf("tagline = %q after undo, want %q", cfg.Tagline, origTagline)
	}
	var n int
	db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE schema_id = $1 AND name = 'Owner edit'`, sc.ID).Scan(&n)
	if n != 1 {
		t.Errorf("the edited page was removed by the undo")
	}
	// A revert that left an edited page stays open, so the owner can retry.
	if h, err := rev.History(ctx, 5, 0); err != nil || len(h) == 0 || h[0].ID != cs || h[0].State != changeset.StatePublished || h[0].Applied != 3 {
		t.Errorf("history = %+v, %v", h, err)
	}
	if restored, kept, err := rev.Undo(ctx, cs); err != nil || restored != 0 || kept != 1 {
		t.Errorf("second undo = %d, %d, %v", restored, kept, err)
	}
	if _, _, err := rev.Undo(ctx, cs+1000000); !errors.Is(err, admin.ErrReviewNotFound) {
		t.Errorf("undo of an unknown changeset: %v", err)
	}
}
