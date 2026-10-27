package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

func newRouteTestServer() *Server {
	return &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

type routeFixture struct {
	deps      routeDeps
	pageSlug  string
	scopeSeen []string
	logged    string
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	snap := fixtureSnapshot(t, map[string]string{
		"page":   "",
		"post":   "",
		"person": `{"url_pattern":"/team/{slug}","archive_path":"/team"}`,
	})
	f := &routeFixture{}
	f.deps = routeDeps{
		snapshot: func(context.Context) (*entity.URLSnapshot, error) { return snap, nil },
		repo: &fakeResolveRepo{rows: []*entity.Entity{
			fixtureEntity("page", "about", ""),
			fixtureEntity("post", "hello", ""),
			fixtureEntity("person", "ada", ""),
		}},
		log404: func(path, _, _ string) { f.logged = path },
		loadPage: func(r *http.Request, slug string) (*PublicPageResponse, pageOutcome, error) {
			f.pageSlug = slug
			sc, _ := publicScopeFrom(r.Context())
			f.scopeSeen = sc.SchemaSlugs
			return &PublicPageResponse{}, pageOutcome{Status: 200}, nil
		},
	}
	return f
}

func TestResolveRoute_Neutral(t *testing.T) {
	t.Parallel()
	f := newRouteFixture(t)
	res, out, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, "/blog/hello", httptest.NewRequest("GET", "/x?skip_redirects=true", nil))
	if err != nil || res == nil || res.Page == nil {
		t.Fatalf("got %+v %v", res, err)
	}
	if res.Kind != resolveKindEntity || res.CanonicalURL != "/blog/hello" {
		t.Errorf("result = %+v", res)
	}
	if f.pageSlug != "hello" || len(f.scopeSeen) == 0 {
		t.Errorf("slug=%q scope=%v outcome=%+v", f.pageSlug, f.scopeSeen, out)
	}
}

func TestResolveRoute_Page(t *testing.T) {
	t.Parallel()
	f := newRouteFixture(t)
	res, _, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, "/about/", httptest.NewRequest("GET", "/x", nil))
	if err != nil || res == nil || res.Page == nil {
		t.Fatalf("got %+v %v", res, err)
	}
	if f.pageSlug != "about" || res.CanonicalURL != "/about" {
		t.Errorf("slug=%q res=%+v", f.pageSlug, res)
	}
}

func TestResolveRoute_ArchiveRedirectNotFoundErrors(t *testing.T) {
	t.Parallel()
	srv := newRouteTestServer()
	req := httptest.NewRequest("GET", "/x", nil)
	ctx := context.Background()

	f := newRouteFixture(t)
	res, _, err := srv.resolveRouteWith(ctx, f.deps, "/team", req)
	if err != nil || res == nil || res.Archive == nil || res.Archive.SchemaSlug != "person" || res.Kind != resolveKindArchive {
		t.Fatalf("archive: %+v %v", res, err)
	}
	if f.pageSlug != "" {
		t.Error("archive must not load a page")
	}

	res, out, err := srv.resolveRouteWith(ctx, f.deps, "/nope", req)
	if err != nil || res == nil || !res.NotFound || !out.NotFound || f.logged != "/nope" {
		t.Fatalf("not found: %+v %+v %v logged=%q", res, out, err, f.logged)
	}

	f.deps.checkRedirect = func(_ context.Context, p string) *redirect.Redirect {
		if p == "/old" {
			return &redirect.Redirect{TargetURL: "/new", StatusCode: 301}
		}
		return nil
	}
	res, _, err = srv.resolveRouteWith(ctx, f.deps, "/old/", req)
	if err != nil || res == nil || res.Redirect == nil || res.Redirect.Target != "/new" || res.Redirect.Status != 301 {
		t.Fatalf("redirect: %+v %v", res, err)
	}

	for _, bad := range []string{"relative", "/a?b=1", "/a#frag"} {
		_, _, err := srv.resolveRouteWith(ctx, f.deps, bad, req)
		var pe *pageError
		if !errors.As(err, &pe) || pe.status != http.StatusBadRequest {
			t.Errorf("%q: err = %v", bad, err)
		}
	}

	f.deps.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return nil, errors.New("boom") }
	_, _, err = srv.resolveRouteWith(ctx, f.deps, "/blog/hello", req)
	var pe *pageError
	if !errors.As(err, &pe) || pe.status != http.StatusInternalServerError {
		t.Errorf("registry failure: err = %v", err)
	}
}
