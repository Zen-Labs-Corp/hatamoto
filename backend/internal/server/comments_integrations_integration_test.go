//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/pluginhost"
)

// The plugin read API must see published entities of every public locale of a
// multi-locale site, shared (und) content included, and still hide a draft and
// an entity of a configured but hidden locale.
func TestPluginCommentReaderLocales_Integration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := env.db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE hatamoto_site_config SET language='en', prefix_all=false,
		locales='[{"tag":"en","segment":""},{"tag":"nl","segment":"nl"},{"tag":"fr","segment":"fr","hidden":true}]'::jsonb`)
	schemaID := uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Rdr','rdr','{"type":"object"}','{"url_pattern":"/{slug}"}')`, schemaID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(env.cfg, env.db, logger)
	s.comments = comment.New(env.db, env.cfg.Secret, logger)
	set, err := s.comments.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Enabled = true
	if err := s.comments.SaveSettings(ctx, set, nil); err != nil {
		t.Fatal(err)
	}

	res, err := commentfmt.Render("hello")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(locale, status string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
			VALUES ($1,$2,$3,$3,$4,$5,now(),'{}'::jsonb)`, id, schemaID, "e-"+locale+"-"+status, locale, status)
		c, err := s.comments.Store(ctx, comment.Draft{
			EntityID: id, Email: "a@rdr.test", AuthorName: "A", BodyRaw: "hello", BodyHTML: res.HTML,
			BodyHash: res.BodyHash, SpamVerdict: "allow", Status: comment.StatusDraft, Locale: locale,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.comments.Confirm(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.comments.Approve(ctx, c.ID, nil); err != nil {
			t.Fatal(err)
		}
		return id
	}
	en, nl, und, draft, hidden := seed("en", "published"), seed("nl", "published"), seed("und", "published"), seed("nl", "draft"), seed("fr", "published")

	r := pluginCommentReader{s: s}
	for name, id := range map[string]uuid.UUID{"en": en, "nl": nl, "und": und} {
		th, err := r.Thread(ctx, id, 1, pluginhost.ThreadOpts{})
		if err != nil || len(th.Items) != 1 {
			t.Errorf("Thread(%s) = %d items, %v; want 1", name, len(th.Items), err)
		}
	}
	if th, err := r.Thread(ctx, draft, 1, pluginhost.ThreadOpts{}); err != nil || len(th.Items) != 0 {
		t.Errorf("Thread(draft) = %d items, %v; want 0", len(th.Items), err)
	}
	if th, err := r.Thread(ctx, hidden, 1, pluginhost.ThreadOpts{}); err != nil || len(th.Items) != 0 {
		t.Errorf("Thread(hidden locale) = %d items, %v; want 0", len(th.Items), err)
	}
	counts, err := r.Counts(ctx, []uuid.UUID{en, nl, und, draft, hidden})
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]uuid.UUID{"en": en, "nl": nl, "und": und} {
		if counts[id] != 1 {
			t.Errorf("Counts(%s) = %d, want 1", name, counts[id])
		}
	}
	if counts[draft] != 0 {
		t.Errorf("Counts(draft) = %d, want 0", counts[draft])
	}
	if counts[hidden] != 0 {
		t.Errorf("Counts(hidden locale) = %d, want 0", counts[hidden])
	}
}
