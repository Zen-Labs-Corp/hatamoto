package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/pluginhost"

	"github.com/google/uuid"
)

// scanRepo answers the form usage scan and nothing else.
type scanRepo struct {
	entity.EntityRepository
	ents  []*entity.Entity
	scans int
}

func (r *scanRepo) ScanFormCandidates(_ context.Context, fn func(*entity.Entity) bool) error {
	r.scans++
	for _, e := range r.ents {
		if !fn(e) {
			break
		}
	}
	return nil
}

func TestEntityEventsResetFormUsage(t *testing.T) {
	doc, _ := jsonx.Marshal(map[string]any{"page_doc": map[string]any{"v": 2, "nodes": []any{
		map[string]any{"id": "a", "t": "form", "p": map[string]any{"slug": "contact"}}}}})
	repo := &scanRepo{ents: []*entity.Entity{{
		ID: uuid.New(), Name: "Contact", Slug: "contact", Status: entity.StatusPublished,
		Attributes: doc, Schema: &entity.EntitySchema{Slug: "page"},
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: log, hooks: hooks.New(log), infra: &Infrastructure{}}
	s.formUsage = entity.NewFormUsageIndex(entity.NewService(nil, repo, log))
	s.registerRenderInvalidation()
	sink := pluginhost.NewEntitySink(s.hooks, log)
	ctx := context.Background()

	got, err := s.adminGrowth().FormUsage(ctx)
	if err != nil || len(got["contact"]) != 1 {
		t.Fatalf("usage = %v, %v", got, err)
	}
	if _, err := s.formUsage.Get(ctx); err != nil || repo.scans != 1 {
		t.Fatalf("second read rescanned: scans = %d, err %v", repo.scans, err)
	}

	for i, kind := range []entity.EventKind{
		entity.EventCreated, entity.EventUpdated, entity.EventPublished, entity.EventDeleted,
	} {
		sink.Emit(ctx, entity.Event{Kind: kind, ID: uuid.New(), Slug: "x"})
		s.hooks.Wait()
		if _, err := s.formUsage.Get(ctx); err != nil {
			t.Fatal(err)
		}
		if want := i + 2; repo.scans != want {
			t.Errorf("%s: scans = %d, want %d (index not reset)", kind, repo.scans, want)
		}
	}
}
