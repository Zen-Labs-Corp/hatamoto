package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/agentpolicystate"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/entity"
)

func TestAgentPolicyRestWriteAgentRefusal(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		handled  bool
		wantBody string
	}{
		{"denied", &agentpolicystate.DeniedError{Reason: "not on weekends"}, true, "not on weekends"},
		{"denied wrapped", fmt.Errorf("x: %w", &agentpolicystate.DeniedError{Reason: "no"}), true, "refused by the agent policy: no"},
		{"needs review", agentpolicystate.ErrNeedsReview, true, agentpolicystate.ErrNeedsReview.Error()},
		{"policy failed", agentpolicystate.ErrPolicy, true, agentpolicystate.ErrPolicy.Error()},
		{"other", errors.New("boom"), false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if got := writeAgentRefusal(rec, c.err); got != c.handled {
				t.Fatalf("handled = %v, want %v", got, c.handled)
			}
			if !c.handled {
				if rec.Body.Len() != 0 {
					t.Fatalf("wrote %q for an unrelated error", rec.Body)
				}
				return
			}
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), c.wantBody) {
				t.Fatalf("got %d %s, want 403 with %q", rec.Code, rec.Body, c.wantBody)
			}
		})
	}
}

func TestAgentPolicyRestDeniedReasonReachesTheResponse(t *testing.T) {
	gate := fakeGate{planErr: &agentpolicystate.DeniedError{Reason: "not on weekends"}}
	for _, c := range []struct {
		name, method, body string
		handler            func(*Server, http.ResponseWriter, *http.Request)
	}{
		{"status", "PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus},
		{"update", "PUT", `{"name":"New"}`, (*Server).handleUpdateEntity},
		{"restore", "POST", ``, (*Server).handleRestoreEntityVersion},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec, repo := reviewCall(t, gate, "owner", c.method, c.body, c.handler)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not on weekends") {
				t.Fatalf("got %d %s, want 403 with the reason", rec.Code, rec.Body)
			}
			if repo.writes != 0 {
				t.Errorf("the live row was written %d times", repo.writes)
			}
		})
	}
}

type linkSourceRepo struct {
	entity.EntityRepository
	status entity.EntityStatus
	err    error
	gets   int
}

func (r *linkSourceRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	r.gets++
	if r.err != nil {
		return nil, r.err
	}
	return &entity.Entity{ID: id, Slug: "s", Locale: "en", Status: r.status, Schema: &entity.EntitySchema{Slug: "page"}}, nil
}

type fixedPolicy struct{ d agentpolicystate.Decision }

func (p fixedPolicy) Decide(context.Context, agentpolicystate.Request) (agentpolicystate.Decision, error) {
	return p.d, nil
}

func linkServer(repo *linkSourceRepo) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{logger: logger, entityService: entity.NewService(nil, repo, logger)}
}

var linkKey = auditctx.Actor{Name: "api-key:ci", Kind: audit.KindAPIKey, KeyID: "6f1c0c0e-5d0a-4a43-9a52-0d3f4c1f9e11", Via: "api"}

func linkRuntime(t *testing.T, p agentpolicystate.Policy) *[]auditstate.Event {
	t.Helper()
	agentpolicystate.Reset()
	t.Cleanup(agentpolicystate.Reset)
	events := &[]auditstate.Event{}
	if p != nil {
		agentpolicystate.Register(p)
	}
	agentpolicystate.SetRuntime(agentpolicystate.Runtime{
		Record: func(_ context.Context, ev auditstate.Event) { *events = append(*events, ev) },
		Changes: func(context.Context, string, time.Time, int) ([]agentpolicystate.Change, error) {
			return nil, nil
		},
	})
	return events
}

func TestAgentPolicyRestLinkInsert(t *testing.T) {
	deny := fixedPolicy{agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "no links today"}}
	review := fixedPolicy{agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeRequireReview}}
	allow := fixedPolicy{agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeAllow}}
	cases := []struct {
		name      string
		policy    agentpolicystate.Policy
		status    entity.EntityStatus
		refused   bool
		wantBody  string
		wantEvent int
	}{
		{"deny published", deny, entity.StatusPublished, true, "no links today", 1},
		{"deny scheduled", deny, entity.StatusScheduled, true, "no links today", 1},
		{"review cannot stage", review, entity.StatusPublished, true, agentpolicystate.ErrNeedsReview.Error(), 1},
		{"allow", allow, entity.StatusPublished, false, "", 0},
		{"draft is not asked", deny, entity.StatusDraft, false, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := linkRuntime(t, c.policy)
			rec := httptest.NewRecorder()
			got := linkServer(&linkSourceRepo{status: c.status}).refuseAgentLinkAs(context.Background(), rec, linkKey, uuid.New())
			if got != c.refused {
				t.Fatalf("refused = %v, want %v (%d %s)", got, c.refused, rec.Code, rec.Body)
			}
			if c.refused && (rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), c.wantBody)) {
				t.Fatalf("got %d %s, want 403 with %q", rec.Code, rec.Body, c.wantBody)
			}
			if len(*events) != c.wantEvent {
				t.Fatalf("deny events = %d, want %d", len(*events), c.wantEvent)
			}
			if c.wantEvent > 0 && ((*events)[0].Action != agentpolicystate.DenyAction || (*events)[0].KeyID != linkKey.KeyID) {
				t.Fatalf("event = %+v", (*events)[0])
			}
		})
	}
}

func TestAgentPolicyRestLinkInsertSourceErrors(t *testing.T) {
	linkRuntime(t, fixedPolicy{agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeAllow}})
	for _, c := range []struct {
		err  error
		code int
	}{{entity.ErrEntityNotFound, http.StatusNotFound}, {errors.New("db down"), http.StatusInternalServerError}} {
		rec := httptest.NewRecorder()
		if !linkServer(&linkSourceRepo{err: c.err}).refuseAgentLinkAs(context.Background(), rec, linkKey, uuid.New()) || rec.Code != c.code {
			t.Fatalf("err %v: got %d, want %d", c.err, rec.Code, c.code)
		}
	}
}

func TestAgentPolicyRestLinkInsertNothingRegisteredFetchesNothing(t *testing.T) {
	agentpolicystate.Reset()
	repo := &linkSourceRepo{status: entity.StatusPublished}
	req := httptest.NewRequest("POST", "/api/v1/linking/insert", nil)
	rec := httptest.NewRecorder()
	if linkServer(repo).refuseAgentLink(rec, req, uuid.New()) || repo.gets != 0 || rec.Body.Len() != 0 {
		t.Fatalf("refused or fetched with no policy: gets = %d, body %q", repo.gets, rec.Body)
	}
	// A registered policy but a request that is not a key: still nothing.
	linkRuntime(t, fixedPolicy{agentpolicystate.Decision{Outcome: agentpolicystate.OutcomeDeny, Reason: "x"}})
	if linkServer(repo).refuseAgentLink(rec, req, uuid.New()) || repo.gets != 0 {
		t.Fatalf("a non-key request was checked: gets = %d", repo.gets)
	}
}
