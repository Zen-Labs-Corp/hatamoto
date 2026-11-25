package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
)

// versionRepo is one published entry at a fixed version that honours the
// version a write expects, like the Postgres repository does.
type versionRepo struct {
	entity.EntityRepository
	mu        sync.Mutex
	createdBy uuid.UUID
	version   int
	writes    int
	moves     int    // SetPosition calls
	race      bool   // Update loses a race the caller did not name
	bigAttrs  bool   // the entry carries attributes over the compression threshold
	attrs     string // stored attributes; "" = {}
}

func (r *versionRepo) entry(id uuid.UUID) *entity.Entity {
	attrs := []byte(`{}`)
	if r.attrs != "" {
		attrs = []byte(r.attrs)
	}
	if r.bigAttrs {
		attrs = []byte(`{"body":"` + strings.Repeat("x", 2*httpx.CompressMinSize) + `"}`)
	}
	return &entity.Entity{ID: id, Name: "E", Slug: "e", Status: entity.StatusPublished, Version: r.version, Attributes: attrs,
		CreatedBy: &r.createdBy, Schema: &entity.EntitySchema{Slug: "page", Schema: []byte(`{}`)}}
}

func (r *versionRepo) GetByID(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entry(id), nil
}

func (r *versionRepo) Update(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.race || (in.ExpectedVersion != nil && *in.ExpectedVersion != r.version) {
		return nil, &entity.VersionConflictError{Current: r.version}
	}
	r.writes++
	r.version++
	return r.entry(id), nil
}

// SetPosition and OrderRows make versionRepo an entity.OrderStore.
func (r *versionRepo) SetPosition(context.Context, uuid.UUID, int) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moves++
	return 2, 5, nil
}

func (r *versionRepo) OrderRows(context.Context, uuid.UUID, string) ([]*entity.Entity, error) {
	return nil, nil
}

func (r *versionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, u *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	return r.UpdateStatusIf(ctx, id, u, st, nil)
}

func (r *versionRepo) UpdateStatusIf(_ context.Context, id uuid.UUID, _ *uuid.UUID, st entity.EntityStatus, expected *int) (*entity.Entity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if expected != nil && *expected != r.version {
		return nil, &entity.VersionConflictError{Current: r.version}
	}
	r.writes++
	e := r.entry(id)
	e.Status = st
	return e, nil
}

func (r *versionRepo) GetVersion(_ context.Context, id uuid.UUID, v int) (*entity.EntityVersion, error) {
	return &entity.EntityVersion{EntityID: id, Version: v, Attributes: []byte(`{"restored":true}`)}, nil
}

// countingGate holds every change and counts what it was asked to stage.
type countingGate struct{ submits int }

func (g *countingGate) Plan(context.Context, *entity.Entity, entity.ReviewKind) (entity.ReviewPlan, error) {
	return heldPlan, nil
}

func (g *countingGate) Submit(context.Context, entity.ReviewChange, entity.ReviewPlan) (*entity.SentForReviewError, error) {
	g.submits++
	return &entity.SentForReviewError{ChangesetID: 7}, nil
}

type condRoute struct {
	method, body string
	handler      func(*Server, http.ResponseWriter, *http.Request)
}

var (
	condUpdate  = condRoute{"PATCH", `{"name":"New"}`, (*Server).handleUpdateEntity}
	condStatus  = condRoute{"PATCH", `{"status":"published"}`, (*Server).handleUpdateEntityStatus}
	condRestore = condRoute{"POST", ``, (*Server).handleRestoreEntityVersion}
	condGet     = condRoute{"GET", ``, (*Server).handleGetEntity}
)

// condCall runs route against a published entry at version 3 with the given
// If-Match header lines.
func condCall(t *testing.T, repo *versionRepo, gate entity.ReviewGate, route condRoute, ifMatch ...string) *httptest.ResponseRecorder {
	t.Helper()
	return condServe(t, repo, gate, route, nil, ifMatch...)
}

// condServe is condCall with the handler optionally wrapped in middleware, and
// a client that accepts gzip.
func condServe(t *testing.T, repo *versionRepo, gate entity.ReviewGate, route condRoute, wrap func(http.Handler) http.Handler, ifMatch ...string) *httptest.ResponseRecorder {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uid := uuid.New()
	repo.createdBy = uid
	if repo.version == 0 {
		repo.version = 3
	}
	svc := entity.NewService(nil, repo, logger)
	role := "editor"
	if gate != nil {
		svc.SetReviewGate(gate)
		role = "author"
	}
	s := &Server{logger: logger, entityService: svc}
	id := uuid.New()
	req := httptest.NewRequest(route.method, "/api/v1/entities/"+id.String(), strings.NewReader(route.body))
	req.SetPathValue("id", id.String())
	req.SetPathValue("version", "1")
	req.Header.Set("Accept-Encoding", "gzip")
	for _, h := range ifMatch {
		req.Header.Add("If-Match", h)
	}
	ctx := middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Role: "editor"})
	ctx = authzctx.With(ctx, authz.Subject{UserID: uid.String(), KeyID: "k1", Scopes: []string{"*"}, CreatorRole: role})
	rec := httptest.NewRecorder()
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { route.handler(s, w, r) })
	if wrap != nil {
		h = wrap(h)
	}
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func conditionalBody(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var b struct {
		Current int `json:"current_version"`
	}
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return b.Current
}

func TestConditionalETagOnGet(t *testing.T) {
	rec := condCall(t, &versionRepo{}, nil, condGet)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"3"` {
		t.Fatalf("status = %d etag = %q", rec.Code, rec.Header().Get("ETag"))
	}
}

func TestConditionalWrites(t *testing.T) {
	routes := map[string]condRoute{"update": condUpdate, "status": condStatus, "restore": condRestore}
	// the ETag after a successful write: an edit and a restore store a version, a status change does not
	after := map[string]string{"update": `"4"`, "status": `"3"`, "restore": `"4"`}

	for name, route := range routes {
		t.Run(name, func(t *testing.T) {
			for _, ok := range [][]string{nil, {`"3"`}, {`W/"3"`}, {`*`}} {
				repo := &versionRepo{}
				rec := condCall(t, repo, nil, route, ok...)
				if rec.Code != http.StatusOK || rec.Header().Get("ETag") != after[name] || repo.writes != 1 {
					t.Errorf("If-Match %q: status = %d etag = %q writes = %d", ok, rec.Code, rec.Header().Get("ETag"), repo.writes)
				}
			}

			repo := &versionRepo{}
			rec := condCall(t, repo, nil, route, `"2"`)
			if rec.Code != http.StatusPreconditionFailed || rec.Header().Get("ETag") != `"3"` || conditionalBody(t, rec) != 3 || repo.writes != 0 {
				t.Errorf("stale: status = %d etag = %q body = %s writes = %d", rec.Code, rec.Header().Get("ETag"), rec.Body, repo.writes)
			}

			for _, bad := range [][]string{{`W/"x"`}, {`w/"3"`}, {`W/3`}, {`W/""`}, {`W/"3", W/"3"`}, {`"2", "3"`}, {`"3"`, `"3"`}, {`abc`}, {``}} {
				repo := &versionRepo{}
				rec := condCall(t, repo, nil, route, bad...)
				if rec.Code != http.StatusBadRequest || repo.writes != 0 {
					t.Errorf("If-Match %q: status = %d writes = %d, want 400 and no write", bad, rec.Code, repo.writes)
				}
			}
		})
	}
}

// httpx.Compress weakens the ETag of a compressed body to W/"N"; a client that
// echoes it back as received must still pass the check.
func TestConditionalWeakETagAfterCompression(t *testing.T) {
	compress := httpx.Compress(func() bool { return true })
	rec := condServe(t, &versionRepo{bigAttrs: true}, nil, condGet, compress)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("ETag") != `W/"3"` {
		t.Fatalf("GET: encoding = %q etag = %q, want gzip and W/\"3\"", rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"))
	}
	tag := rec.Header().Get("ETag")

	repo := &versionRepo{}
	rec = condServe(t, repo, nil, condUpdate, compress, tag)
	if rec.Code != http.StatusOK || repo.writes != 1 {
		t.Fatalf("PATCH with %s: status = %d writes = %d, want 200 and one write", tag, rec.Code, repo.writes)
	}
	repo = &versionRepo{}
	rec = condServe(t, repo, nil, condUpdate, compress, `W/"2"`)
	if rec.Code != http.StatusPreconditionFailed || repo.writes != 0 {
		t.Fatalf("stale weak tag: status = %d writes = %d, want 412", rec.Code, repo.writes)
	}
}

// A position-only change stores no version, so it is a move whatever tag came with it.
func TestConditionalMoveOnlyIgnoresTheTag(t *testing.T) {
	route := condRoute{"PATCH", `{"position":2}`, (*Server).handleUpdateEntity}
	for _, h := range []string{`"2"`, `W/"2"`, `"3"`, `*`} {
		repo := &versionRepo{}
		rec := condCall(t, repo, nil, route, h)
		if rec.Code != http.StatusOK || repo.moves != 1 || repo.writes != 0 {
			t.Errorf("If-Match %s: status = %d %s moves = %d writes = %d, want 200, one move, no update", h, rec.Code, rec.Body, repo.moves, repo.writes)
		}
	}
}

func TestConditionalWithoutHeaderKeepsTheStoredVersionETag(t *testing.T) {
	rec := condCall(t, &versionRepo{version: 9}, nil, condUpdate)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"10"` {
		t.Fatalf("status = %d etag = %q", rec.Code, rec.Header().Get("ETag"))
	}
}

// A restore that loses a race nobody named is a 409; one that named a version is a 412.
func TestConditionalRestoreRaceIsConflict(t *testing.T) {
	for _, h := range [][]string{nil, {`*`}} {
		repo := &versionRepo{race: true}
		rec := condCall(t, repo, nil, condRestore, h...)
		if rec.Code != http.StatusConflict || rec.Header().Get("ETag") != `"3"` || conditionalBody(t, rec) != 3 {
			t.Errorf("If-Match %q: status = %d etag = %q body = %s, want 409", h, rec.Code, rec.Header().Get("ETag"), rec.Body)
		}
	}
	rec := condCall(t, &versionRepo{race: true}, nil, condRestore, `"3"`)
	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("named version: status = %d, want 412", rec.Code)
	}
}

// A held change with a stale version is refused before anything is staged.
func TestConditionalHeldStaleStagesNothing(t *testing.T) {
	for name, route := range map[string]condRoute{"update": condUpdate, "status": condStatus, "restore": condRestore} {
		t.Run(name, func(t *testing.T) {
			gate := &countingGate{}
			repo := &versionRepo{}
			rec := condCall(t, repo, gate, route, `"2"`)
			if rec.Code != http.StatusPreconditionFailed || gate.submits != 0 || repo.writes != 0 {
				t.Fatalf("stale: status = %d %s submits = %d writes = %d", rec.Code, rec.Body, gate.submits, repo.writes)
			}
			rec = condCall(t, &versionRepo{}, gate, route, `"3"`)
			if rec.Code != http.StatusAccepted || gate.submits != 1 || rec.Header().Get("ETag") != "" {
				t.Fatalf("current: status = %d submits = %d etag = %q, want 202 staged without an ETag", rec.Code, gate.submits, rec.Header().Get("ETag"))
			}
		})
	}
}
