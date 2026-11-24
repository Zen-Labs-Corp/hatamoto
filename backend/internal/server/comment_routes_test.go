package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/siteaccess"
)

func commentRoutesRouter(t *testing.T) http.Handler {
	t.Helper()
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		commentConfirm: stub("confirm"), commentSignIn: stub("signin"), commentPost: stub("post"), commentEdit: stub("edit"), commentDelete: stub("delete"),
	})
	return h
}

func commentRoutesDo(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestCommentRoutesReachTheirHandlers(t *testing.T) {
	h := commentRoutesRouter(t)
	id := uuid.NewString()
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/-/comments/" + id, "post"},
		{"POST", "/-/comments/edit/" + id, "edit"},
		{"POST", "/-/comments/delete/" + id, "delete"},
		{"GET", "/-/comments/confirm/tok", "confirm"},
		{"POST", "/-/comments/confirm/tok", "confirm"},
		{"GET", "/-/comments/signin", "signin"},
		{"POST", "/-/comments/signin", "signin"},
		{"GET", "/-/comments/signin/tok", "signin"},
		{"POST", "/-/comments/signin/tok", "signin"},
		{"POST", "/-/comments/signout", "signin"},
	} {
		if got := commentRoutesDo(h, c.method, c.path).Body.String(); got != c.want {
			t.Errorf("%s %s -> %q, want %q", c.method, c.path, got, c.want)
		}
	}
	// Sign-out is its own route, not the comment post for an entity called "signout".
	if got := commentRoutesDo(h, "POST", "/-/comments/signout").Body.String(); got == "post" {
		t.Error("POST /-/comments/signout reached the comment-post handler")
	}
	// A GET of the submit path is not the submit handler.
	for _, p := range []string{"/-/comments/" + id, "/-/comments/edit/" + id, "/-/comments/delete/" + id} {
		if w := commentRoutesDo(h, "GET", p); w.Body.String() == "post" || w.Body.String() == "edit" || w.Body.String() == "delete" {
			t.Errorf("GET %s reached a comment handler", p)
		}
	}
}

func TestCommentRoutesPerClientLimiter(t *testing.T) {
	h := commentRoutesRouter(t)
	id := uuid.NewString()
	limited := false
	for range 30 {
		if commentRoutesDo(h, "POST", "/-/comments/"+id).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("POST /-/comments/{id} is not rate-limited")
	}
}

func TestCommentRoutesSiteAccessGate(t *testing.T) {
	h := commentRoutesRouter(t)
	set := siteaccess.Settings{Mode: siteaccess.ModeMaintenance, RetryAfter: 900, Epoch: 1}
	s := &Server{accessSettings: func(context.Context) (siteaccess.Settings, error) { return set, nil }}
	gated := s.siteAccessGate(h)
	id := uuid.NewString()
	for _, p := range []string{"/-/comments/" + id, "/-/comments/edit/" + id, "/-/comments/delete/" + id} {
		w := commentRoutesDo(gated, "POST", p)
		if w.Code < 400 || w.Body.String() == "post" || w.Body.String() == "edit" || w.Body.String() == "delete" {
			t.Errorf("maintenance POST %s: %d %q", p, w.Code, w.Body.String())
		}
	}
	set = siteaccess.Settings{Mode: siteaccess.ModeOpen}
	if got := commentRoutesDo(gated, "POST", "/-/comments/"+id).Body.String(); got != "post" {
		t.Errorf("open site: %q", got)
	}
}
