//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

type cascadeRig struct {
	t       *testing.T
	s       *Server
	eng     *changeset.Engine
	res     commentfmt.Result
	mu      sync.Mutex
	deleted []sdk.CommentEvent
}

func newCascadeRig(t *testing.T) *cascadeRig {
	t.Helper()
	env := newWireEnv(t, nil)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &cascadeRig{t: t}
	r.s = New(env.cfg, env.db, logger)
	r.s.hooks = hooks.New(logger)
	r.s.comments = comment.New(env.db, env.cfg.Secret, logger)
	r.s.comments.Register(newCommentEventObserver(r.s.hooks))
	r.s.hooks.AddAction(sdk.CommentDeleted.Name, 0, func(_ context.Context, p any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.deleted = append(r.deleted, p.(sdk.CommentEvent))
	})
	r.s.wireCommentCascade()
	r.eng = &changeset.Engine{DB: env.db, Logger: logger, Writers: map[changeset.Kind]changeset.KindWriter{
		changeset.KindEntity: changeset.EntityWriter{Svc: r.s.entityService},
	}}
	if _, err := env.db.Pool().Exec(context.Background(),
		`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'Casc','casc','{"type":"object"}','{"url_pattern":"/{slug}"}')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	var err error
	if r.res, err = commentfmt.Render("hello"); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *cascadeRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.s.db.Pool().Exec(context.Background(), q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// entityWithComments creates an entity with three comments: approved, pending (confirmed, never approved) and a draft.
func (r *cascadeRig) entityWithComments(slug string) uuid.UUID {
	r.t.Helper()
	ctx := context.Background()
	id := uuid.New()
	r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,locale,status,published_at,attributes)
		SELECT $1, id, $2, $2, 'en', 'published', now(), '{}'::jsonb FROM hatamoto_entity_schemas WHERE slug = 'casc'`, id, slug)
	draft := func() comment.Comment {
		c, err := r.s.comments.Store(ctx, comment.Draft{
			EntityID: id, Email: "a@example.test", AuthorName: "Ann", BodyRaw: "hello", BodyHTML: r.res.HTML,
			BodyHash: r.res.BodyHash, SpamVerdict: "allow", Status: comment.StatusDraft, Locale: "en",
		})
		if err != nil {
			r.t.Fatal(err)
		}
		return c
	}
	for i := 0; i < 2; i++ {
		c := draft()
		if _, err := r.s.comments.Confirm(ctx, c.ID); err != nil {
			r.t.Fatal(err)
		}
		if i == 0 {
			if _, err := r.s.comments.Approve(ctx, c.ID, nil); err != nil {
				r.t.Fatal(err)
			}
		}
	}
	draft() // never public: never announced
	return id
}

func (r *cascadeRig) events() []sdk.CommentEvent {
	r.s.hooks.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdk.CommentEvent(nil), r.deleted...)
}

func (r *cascadeRig) comments(id uuid.UUID) int {
	r.t.Helper()
	var n int
	if err := r.s.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_comments WHERE entity_id = $1`, id).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func TestCommentCascadeDeleteEntity(t *testing.T) {
	r := newCascadeRig(t)
	id := r.entityWithComments("gone")
	if err := r.s.entityService.DeleteEntity(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	ev := r.events()
	if len(ev) != 1 {
		t.Fatalf("comment.deleted events = %d, want 1 (only the approved one: a held comment and a draft were never public): %+v", len(ev), ev)
	}
	for _, e := range ev {
		if e.EntityID != id.String() || e.BodyHTML != "" {
			t.Fatalf("event = %+v", e)
		}
	}
	if r.comments(id) != 0 {
		t.Fatal("the comments survived the entity")
	}
}

// stageDelete stages the entity's delete in a new changeset, with extra items.
func (r *cascadeRig) stageDelete(id uuid.UUID, extra ...changeset.Item) int64 {
	r.t.Helper()
	ctx := context.Background()
	cs, err := r.eng.Begin(ctx, uuid.New(), "cascade")
	if err != nil {
		r.t.Fatal(err)
	}
	r.exec(`UPDATE hatamoto_entities SET origin = $2 WHERE id = $1`, id, changeset.Origin("cascade"))
	for _, it := range append([]changeset.Item{{Kind: changeset.KindEntity, Op: changeset.OpDelete, TargetID: id.String()}}, extra...) {
		if err := r.eng.Stage(ctx, cs, it); err != nil {
			r.t.Fatal(err)
		}
	}
	return cs
}

func TestCommentCascadeChangesetDelete(t *testing.T) {
	r := newCascadeRig(t)
	ctx := context.Background()

	// Rolled back (a later item fails): nothing announced, nothing removed.
	kept := r.entityWithComments("kept")
	bad, _ := jsonx.Marshal(map[string]any{"schema": "no-such-type", "name": "x", "slug": "x", "attributes": map[string]any{}})
	cs := r.stageDelete(kept, changeset.Item{Kind: changeset.KindEntity, Op: changeset.OpUpsert, SourceKey: "bad", Payload: bad})
	if _, err := r.eng.Publish(ctx, cs); err == nil {
		t.Fatal("the publish with an invalid item must fail")
	}
	if got := r.events(); len(got) != 0 || r.comments(kept) != 3 {
		t.Fatalf("rollback: %d events, %d comments; want 0 and 3", len(got), r.comments(kept))
	}

	// Committed: one event per announced comment, after the commit.
	cs = r.stageDelete(kept)
	if _, err := r.eng.Publish(ctx, cs); err != nil {
		t.Fatal(err)
	}
	if got := r.events(); len(got) != 1 || r.comments(kept) != 0 {
		t.Fatalf("publish: %d events, %d comments; want 1 and 0", len(got), r.comments(kept))
	}
}
