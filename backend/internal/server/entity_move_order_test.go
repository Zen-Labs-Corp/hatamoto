//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/middleware"
)

// A published page moved through the order API keeps its old URL as a redirect,
// and the order observer (the page cache purge) runs.
func TestMoveEntityWritesRedirectAndPurges(t *testing.T) {
	r := newNestedRig(t)
	purged := 0
	r.svc.SetOrderObserver(func(context.Context) { purged++ })
	services := r.page("services", nil)
	seo := r.page("seo", nil)

	res, err := r.svc.MoveEntity(r.ctx, seo.ID, nil, entity.MoveInput{ParentID: &services.ID, Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Position != 1 || res.Total != 1 {
		t.Errorf("placed %d of %d", res.Position, res.Total)
	}
	if got := r.redirectTarget("/seo"); got != "/services/seo" {
		t.Errorf("redirect /seo -> %q, want /services/seo", got)
	}
	r.expect("/services/seo", 200)
	if purged != 1 {
		t.Errorf("order observer ran %d times, want 1", purged)
	}
}

// PATCH /entities/{id} with only position is a move: no edit, no version.
func TestPatchEntityPosition(t *testing.T) {
	r := newNestedRig(t)
	a := r.page("a-page", nil)
	b := r.page("b-page", nil)
	userID := uuid.New()
	r.exec(`INSERT INTO hatamoto_users (id, email) VALUES ($1, $2)`, userID, userID.String()+"@example.test")
	t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_users WHERE id = $1`, userID) })
	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/v1/entities/"+b.ID.String(), strings.NewReader(body))
		req.SetPathValue("id", b.ID.String())
		req = req.WithContext(middleware.ContextWithClaims(req.Context(), &auth.Claims{UserID: userID}))
		rec := httptest.NewRecorder()
		r.srv.handleUpdateEntity(rec, req)
		return rec
	}
	rec := patch(`{"position":1}`)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	var got entity.Entity
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != b.ID || got.SortOrder != 1 || got.Version != b.Version {
		t.Errorf("response: id %s sort_order %d version %d", got.ID, got.SortOrder, got.Version)
	}
	rows, err := r.svc.OrderRows(r.ctx, r.pageSch, b.Locale)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != b.ID || rows[1].ID != a.ID {
		t.Errorf("order after patch: %v", rows)
	}
	if rec := patch(`{"position":1,"parent_id":"` + a.ID.String() + `"}`); rec.Code != 200 {
		t.Errorf("position with a parent: %d %s", rec.Code, rec.Body)
	}
	if rec := patch(`{"parent_id":"` + b.ID.String() + `","position":1}`); rec.Code != 422 || !strings.Contains(rec.Body.String(), "parent_cycle") {
		t.Errorf("own parent: %d %s", rec.Code, rec.Body)
	}
}
