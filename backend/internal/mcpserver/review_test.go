package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

// reviewRepo is one published entry created by the key's creator; it counts writes.
type reviewRepo struct {
	entity.EntityRepository
	writes atomic.Int32
}

func (r *reviewRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	owner := fakeOwner
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: entity.StatusPublished, Attributes: []byte(`{}`), CreatedBy: &owner,
		Schema: &entity.EntitySchema{Slug: "page", Schema: []byte(`{}`)}}, nil
}

func (r *reviewRepo) Update(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ *entity.UpdateEntityInput) (*entity.Entity, error) {
	r.writes.Add(1)
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: entity.StatusPublished, Schema: &entity.EntitySchema{Slug: "page"}}, nil
}

func (r *reviewRepo) UpdateStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	r.writes.Add(1)
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: st, Schema: &entity.EntitySchema{Slug: "page"}}, nil
}

type fakeGate struct {
	held      bool
	planErr   error
	submitErr error
}

func (g fakeGate) Plan(context.Context, *entity.Entity, entity.ReviewKind) (entity.ReviewPlan, error) {
	if !g.held {
		return entity.ReviewPlan{}, g.planErr
	}
	return entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "Review", Group: "reviewers"}}}, g.planErr
}

func (g fakeGate) Submit(context.Context, entity.ReviewChange, entity.ReviewPlan) (*entity.SentForReviewError, error) {
	if g.submitErr != nil {
		return nil, g.submitErr
	}
	return &entity.SentForReviewError{ChangesetID: 42}, nil
}

// reviewSession serves the tools for a key made by role over a published entry.
func reviewSession(t *testing.T, role string, gate entity.ReviewGate) (func(tool string, args map[string]any) (string, bool), *reviewRepo, uuid.UUID) {
	t.Helper()
	repo := &reviewRepo{}
	svc := entity.NewService(nil, repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if gate != nil {
		svc.SetReviewGate(gate)
	}
	ts := newTestServerRole(t, role, []hmauth.APIKeyScope{hmauth.ScopeReadEntities, hmauth.ScopeWriteEntities}, Deps{Entities: svc})
	sess := session(t, ts)
	id := uuid.New()
	return func(tool string, args map[string]any) (string, bool) {
		args["id"] = id.String()
		return call(t, sess, tool, args)
	}, repo, id
}

func TestEntitiesPublishPendingReview(t *testing.T) {
	// An editor's key reaches the service with the tool-level publish right (plan D29).
	run, repo, _ := reviewSession(t, "editor", fakeGate{held: true})
	text, isErr := run("entities_publish", map[string]any{})
	if isErr {
		t.Fatalf("a staged publish is a result, not an error: %s", text)
	}
	m := decode(t, text)
	if m["status"] != "pending_review" || m["changeset_id"] != float64(42) {
		t.Fatalf("result = %s", text)
	}
	if repo.writes.Load() != 0 {
		t.Fatalf("the live row was written %d times", repo.writes.Load())
	}
}

func TestEntitiesUpdateOfPublishedPendingReview(t *testing.T) {
	run, repo, _ := reviewSession(t, "author", fakeGate{held: true})
	text, isErr := run("entities_update", map[string]any{"name": "New"})
	if isErr {
		t.Fatalf("a staged update is a result, not an error: %s", text)
	}
	if m := decode(t, text); m["status"] != "pending_review" || m["changeset_id"] != float64(42) {
		t.Fatalf("result = %s", text)
	}
	if repo.writes.Load() != 0 {
		t.Fatalf("the live row was written %d times", repo.writes.Load())
	}
}

// A gate that holds nothing, or none at all, leaves today's result.
func TestEntitiesUpdateNotHeldIsUnchanged(t *testing.T) {
	for name, gate := range map[string]entity.ReviewGate{"nil": nil, "not held": fakeGate{}} {
		t.Run(name, func(t *testing.T) {
			run, repo, _ := reviewSession(t, "editor", gate)
			text, isErr := run("entities_update", map[string]any{"name": "New"})
			if isErr || strings.Contains(text, "pending_review") || repo.writes.Load() != 1 {
				t.Fatalf("result = %s (error %v), writes = %d; want a normal update", text, isErr, repo.writes.Load())
			}
		})
	}
}

func TestReviewRefusalsKeepTheirText(t *testing.T) {
	cases := []struct {
		name string
		gate fakeGate
		want string
		not  string
	}{
		{"pending", fakeGate{held: true, submitErr: entity.ErrReviewPending}, "waiting for review", ""},
		{"unsupported", fakeGate{held: true, submitErr: entity.ErrReviewUnsupported}, "cannot be sent for review", ""},
		{"hold", fakeGate{planErr: errors.New("policy database is down")}, "review policy refused", "database"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run, repo, _ := reviewSession(t, "editor", c.gate)
			text, isErr := run("entities_publish", map[string]any{})
			if !isErr || !strings.Contains(text, c.want) || (c.not != "" && strings.Contains(text, c.not)) {
				t.Fatalf("result = %s (error %v), want an error containing %q", text, isErr, c.want)
			}
			if repo.writes.Load() != 0 {
				t.Fatalf("the live row was written %d times", repo.writes.Load())
			}
		})
	}
}

// publicError keeps the sentence of a staged change for tools that cannot return it as a result.
func TestPublicErrorSentForReview(t *testing.T) {
	err := publicError(nil, "builder_apply_ops", &entity.SentForReviewError{ChangesetID: 7})
	if !strings.Contains(err.Error(), "pending_review") || !strings.Contains(err.Error(), "changeset 7") {
		t.Fatalf("error = %v", err)
	}
}
