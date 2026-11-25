package server

import (
	"context"
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
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
)

const dryStored = `{"content":"<p>a</p>","other":"x"}`

// dryServe sends a PATCH or PUT with a query string and Content-Type to
// handleUpdateEntity over a published entry at version 3.
func dryServe(t *testing.T, repo *versionRepo, gate entity.ReviewGate, method, query, contentType, body string, ifMatch ...string) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(method, "/api/v1/entities/"+id.String()+query, strings.NewReader(body))
	req.SetPathValue("id", id.String())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range ifMatch {
		req.Header.Add("If-Match", h)
	}
	ctx := middleware.ContextWithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), Role: "editor"})
	ctx = authzctx.With(ctx, authz.Subject{UserID: uid.String(), KeyID: "k1", Scopes: []string{"*"}, CreatorRole: role})
	rec := httptest.NewRecorder()
	s.handleUpdateEntity(rec, req.WithContext(ctx))
	return rec
}

type dryAnswer struct {
	DryRun        bool `json:"dry_run"`
	Version       int  `json:"version"`
	PendingReview bool `json:"pending_review"`
	Changes       []struct {
		Path   string `json:"path"`
		Before any    `json:"before"`
		After  any    `json:"after"`
	} `json:"changes"`
	Errors []string `json:"errors"`
}

func decodeDry(t *testing.T, rec *httptest.ResponseRecorder) dryAnswer {
	t.Helper()
	var a dryAnswer
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return a
}

func TestEntityDryRunAnswersDiffAndStoresNothing(t *testing.T) {
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/json", `{"attributes":{"content":"<p>b</p>","other":"x"}}`)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"3"` {
		t.Fatalf("got %d etag %q: %s", rec.Code, rec.Header().Get("ETag"), rec.Body)
	}
	a := decodeDry(t, rec)
	if !a.DryRun || a.Version != 3 || len(a.Changes) != 1 || a.Changes[0].Path != "content" || len(a.Errors) != 0 {
		t.Fatalf("answer %+v", a)
	}
	if repo.writes != 0 {
		t.Fatalf("writes = %d", repo.writes)
	}
}

func TestEntityDryRunAfterIsSanitised(t *testing.T) {
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/json", `{"attributes":{"content":"<p>b</p><script>alert(1)</script>","other":"x"}}`)
	a := decodeDry(t, rec)
	if rec.Code != http.StatusOK || len(a.Changes) != 1 || a.Changes[0].Path != "content" {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if s, _ := a.Changes[0].After.(string); strings.Contains(s, "<script") {
		t.Fatalf("after not sanitised: %q", s)
	}
}

func TestEntityDryRunPUT(t *testing.T) {
	rec := dryServe(t, &versionRepo{attrs: dryStored}, nil, "PUT", "?dry_run=1", "application/json", `{"attributes":{"content":"<p>b</p>","other":"x"}}`)
	if rec.Code != http.StatusOK || !decodeDry(t, rec).DryRun {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

func TestEntityDryRunMergePatch(t *testing.T) {
	const body = `{"attributes":{"content":"<p>b</p>"}}`
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/merge-patch+json", body)
	a := decodeDry(t, rec)
	if rec.Code != http.StatusOK || len(a.Changes) != 1 || a.Changes[0].Path != "content" || repo.writes != 0 {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	// Control: replace drops "other".
	a = decodeDry(t, dryServe(t, &versionRepo{attrs: dryStored}, nil, "PATCH", "?dry_run=1", "application/json", body))
	if len(a.Changes) != 2 || a.Changes[1].Path != "other" || a.Changes[1].After != nil {
		t.Fatalf("replace: %+v", a.Changes)
	}
	// PUT never merges.
	a = decodeDry(t, dryServe(t, &versionRepo{attrs: dryStored}, nil, "PUT", "?dry_run=1", "application/merge-patch+json", body))
	if len(a.Changes) != 2 {
		t.Fatalf("put: %+v", a.Changes)
	}
}

func TestEntityDryRunMergeNull(t *testing.T) {
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/merge-patch+json", `{"attributes":null}`)
	a := decodeDry(t, rec)
	if rec.Code != http.StatusOK || len(a.Errors) != 1 || !strings.Contains(a.Errors[0], "null in a merge patch") || a.Changes == nil || len(a.Changes) != 0 || repo.writes != 0 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

func TestEntityDryRunStaleIfMatch(t *testing.T) {
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/json", `{"attributes":{"content":"<p>b</p>"}}`, `"2"`)
	if rec.Code != http.StatusPreconditionFailed || conditionalBody(t, rec) != 3 || repo.writes != 0 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

func TestEntityDryRunHeldStagesNothing(t *testing.T) {
	gate := &countingGate{}
	repo := &versionRepo{attrs: dryStored}
	rec := dryServe(t, repo, gate, "PATCH", "?dry_run=1", "application/json", `{"attributes":{"content":"<p>b</p>"}}`)
	if rec.Code != http.StatusOK || !decodeDry(t, rec).PendingReview || gate.submits != 0 || repo.writes != 0 {
		t.Fatalf("got %d submits %d: %s", rec.Code, gate.submits, rec.Body)
	}
}

func TestEntityDryRunBadValue(t *testing.T) {
	const body = `{"name":"New"}`
	for _, q := range []string{"?dry_run=maybe", "?dry_run=", "?dry_run"} {
		repo := &versionRepo{}
		if rec := dryServe(t, repo, nil, "PATCH", q, "application/json", body); rec.Code != http.StatusBadRequest || repo.writes != 0 {
			t.Fatalf("%s: got %d writes %d", q, rec.Code, repo.writes)
		}
	}
	repo := &versionRepo{}
	if rec := dryServe(t, repo, nil, "PATCH", "?dry_run=TRUE", "application/json", body); rec.Code != http.StatusOK || repo.writes != 0 {
		t.Fatalf("TRUE: got %d writes %d", rec.Code, repo.writes)
	}
	repo = &versionRepo{}
	if rec := dryServe(t, repo, nil, "PATCH", "?dry_run=0", "application/json", body); rec.Code != http.StatusOK || repo.writes != 1 {
		t.Fatalf("0: got %d writes %d: %s", rec.Code, repo.writes, rec.Body)
	}
}

func TestEntityDryRunRefusesPosition(t *testing.T) {
	for _, body := range []string{`{"position":1}`, `{"name":"x","position":2}`} {
		repo := &versionRepo{}
		rec := dryServe(t, repo, nil, "PATCH", "?dry_run=1", "application/json", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dry_run does not cover moves") || repo.moves != 0 {
			t.Fatalf("%s: got %d: %s", body, rec.Code, rec.Body)
		}
	}
}
