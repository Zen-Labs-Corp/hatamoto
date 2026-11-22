package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

// reviewRepo is a published entry; every write is counted, so a test sees
// whether the live row was touched.
type reviewRepo struct {
	entity.EntityRepository
	createdBy uuid.UUID
	writes    int
}

func (r *reviewRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: entity.StatusPublished, Attributes: []byte(`{}`), CreatedBy: &r.createdBy,
		Schema: &entity.EntitySchema{Slug: "page", Schema: []byte(`{}`)}}, nil
}

func (r *reviewRepo) Update(_ context.Context, id uuid.UUID, _ *uuid.UUID, _ *entity.UpdateEntityInput) (*entity.Entity, error) {
	r.writes++
	return &entity.Entity{ID: id, Name: "E", Slug: "e"}, nil
}

func (r *reviewRepo) UpdateStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	r.writes++
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: st}, nil
}

func (r *reviewRepo) GetVersion(_ context.Context, id uuid.UUID, v int) (*entity.EntityVersion, error) {
	return &entity.EntityVersion{EntityID: id, Version: v, Attributes: []byte(`{}`)}, nil
}

// fakeGate answers every plan with a fixed outcome.
type fakeGate struct {
	plan      entity.ReviewPlan
	planErr   error
	submitErr error
}

func (g fakeGate) Plan(context.Context, *entity.Entity, entity.ReviewKind) (entity.ReviewPlan, error) {
	return g.plan, g.planErr
}

func (g fakeGate) Submit(context.Context, entity.ReviewChange, entity.ReviewPlan) (*entity.SentForReviewError, error) {
	if g.submitErr != nil {
		return nil, g.submitErr
	}
	return &entity.SentForReviewError{ChangesetID: 42}, nil
}

var heldPlan = entity.ReviewPlan{Held: true, Stages: []entity.ReviewStage{{Name: "Review", Group: "reviewers"}}}

// reviewCall runs handler as a key made by a user with role (a subject on the
// context) against a published entry that user created, on a service with gate
// (nil = no gate).
func reviewCall(t *testing.T, gate entity.ReviewGate, role, method, body string, handler func(*Server, http.ResponseWriter, *http.Request)) (*httptest.ResponseRecorder, *reviewRepo) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uid := uuid.New()
	repo := &reviewRepo{createdBy: uid}
	svc := entity.NewService(nil, repo, logger)
	if gate != nil {
		svc.SetReviewGate(gate)
	}
	s := &Server{logger: logger, entityService: svc}
	id := uuid.New()
	req := httptest.NewRequest(method, "/api/v1/entities/"+id.String(), strings.NewReader(body))
	req.SetPathValue("id", id.String())
	req.SetPathValue("version", "1")
	ctx := middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New()})
	ctx = authzctx.With(ctx, authz.Subject{UserID: uid.String(), KeyID: "k1", Scopes: []string{"*"}, CreatorRole: role})
	rec := httptest.NewRecorder()
	handler(s, rec, req.WithContext(ctx))
	return rec, repo
}

func TestReviewRestPublishIsAccepted(t *testing.T) {
	rec, repo := reviewCall(t, fakeGate{plan: heldPlan}, "author", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", rec.Code, rec.Body)
	}
	for _, want := range []string{`"status":"pending_review"`, `"changeset_id":42`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, rec.Body)
		}
	}
	if repo.writes != 0 {
		t.Errorf("the live row was written %d times", repo.writes)
	}
}

func TestReviewRestUpdateOfPublishedIsAccepted(t *testing.T) {
	rec, repo := reviewCall(t, fakeGate{plan: heldPlan}, "author", "PUT", `{"name":"New"}`, (*Server).handleUpdateEntity)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"changeset_id":42`) {
		t.Fatalf("status = %d %s, want 202 with the changeset", rec.Code, rec.Body)
	}
	if repo.writes != 0 {
		t.Errorf("the live row was written %d times", repo.writes)
	}
}

func TestReviewRestRestoreIsAccepted(t *testing.T) {
	rec, repo := reviewCall(t, fakeGate{plan: heldPlan}, "author", "POST", ``, (*Server).handleRestoreEntityVersion)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"changeset_id":42`) {
		t.Fatalf("status = %d %s, want 202 with the changeset", rec.Code, rec.Body)
	}
	if repo.writes != 0 {
		t.Errorf("the live row was written %d times", repo.writes)
	}
	rec, repo = reviewCall(t, fakeGate{}, "editor", "POST", ``, (*Server).handleRestoreEntityVersion)
	if rec.Code != http.StatusOK || repo.writes != 1 {
		t.Fatalf("not held: status = %d %s writes = %d, want 200 and one write", rec.Code, rec.Body, repo.writes)
	}
}

func TestReviewRestPendingOtherIsConflict(t *testing.T) {
	gate := fakeGate{plan: heldPlan, submitErr: entity.ErrReviewPending}
	rec, _ := reviewCall(t, gate, "author", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "waiting for review") {
		t.Fatalf("status = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestReviewRestUnsupportedAndHold(t *testing.T) {
	rec, _ := reviewCall(t, fakeGate{plan: heldPlan, submitErr: entity.ErrReviewUnsupported}, "author", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unsupported: status = %d %s, want 422", rec.Code, rec.Body)
	}
	rec, repo := reviewCall(t, fakeGate{planErr: errors.New("policy database is down")}, "author", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "database") || repo.writes != 0 {
		t.Errorf("hold: status = %d %s writes = %d, want 403 without the cause and no write", rec.Code, rec.Body, repo.writes)
	}
}

// With no gate, or a gate that holds nothing, the answers are today's.
func TestReviewRestNoGateIsUnchanged(t *testing.T) {
	for name, gate := range map[string]entity.ReviewGate{"nil": nil, "not held": fakeGate{}} {
		t.Run(name, func(t *testing.T) {
			rec, repo := reviewCall(t, gate, "editor", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus)
			if rec.Code != http.StatusOK || repo.writes != 1 {
				t.Fatalf("status = %d %s writes = %d, want 200 and one write", rec.Code, rec.Body, repo.writes)
			}
		})
	}
}

// A key whose creator is a Contributor cannot publish through the API: the key
// route asks entity.publish before any handler runs, so it never reaches the
// review gate (plan D29, unchanged).
func TestReviewRestContributorKeyPublishStaysRefused(t *testing.T) {
	s := authz.Subject{UserID: uuid.NewString(), KeyID: "k2", Scopes: []string{"*"}, CreatorRole: "contributor"}
	ok, err := authz.Can(context.Background(), s, authz.ActionEntityPublish, authz.Object{})
	if err != nil || ok {
		t.Fatalf("Can(entity.publish) = %v, %v; want refused", ok, err)
	}
}
