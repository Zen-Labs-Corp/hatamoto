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

func TestResolveRoute_CappedArchiveRedirects(t *testing.T) {
	t.Parallel()
	snap := fixtureSnapshot(t, map[string]string{
		"item":    `{"url_pattern":"/things/{slug}","archive_path":"/things","archive_groups":{"attribute":"sort","divisor":1000,"path":"v{n}","anchor_attribute":"label","redirect_entries":true}}`,
		"capped":  `{"url_pattern":"/capped/{slug}","archive_path":"/capped","archive_limit":5}`,
		"plain":   `{"url_pattern":"/plain/{slug}","archive_path":"/plain"}`,
		"noredir": `{"url_pattern":"/nr/{slug}","archive_path":"/nr","archive_groups":{"attribute":"sort","divisor":1000,"path":"v{n}"}}`,
	})
	f := newRouteFixture(t)
	f.deps.snapshot = func(context.Context) (*entity.URLSnapshot, error) { return snap, nil }
	f.deps.repo = &fakeResolveRepo{rows: []*entity.Entity{
		fixtureEntity("item", "one-0-8", `{"sort":1008,"label":"v1.0.8"}`),
		fixtureEntity("item", "no-label", `{"sort":2001}`),
		fixtureEntity("item", "no-sort", `{"label":"x"}`),
		fixtureEntity("noredir", "kept", `{"sort":1008}`),
	}}
	srv := newRouteTestServer()
	ctx := context.Background()
	resolve := func(path string) *routeResult {
		t.Helper()
		p, _, _ := strings.Cut(path, "?")
		res, _, err := srv.resolveRouteWith(ctx, f.deps, p, httptest.NewRequest("GET", path, nil))
		if err != nil || res == nil {
			t.Fatalf("%s: %+v %v", path, res, err)
		}
		return res
	}
	wantRedirect := func(path, target string) {
		t.Helper()
		res := resolve(path)
		if res.Redirect == nil || res.Redirect.Target != target || res.Redirect.Status != 301 || res.Redirect.KeepQuery {
			t.Errorf("%s: redirect = %+v, want 301 %s", path, res.Redirect, target)
		}
	}

	wantRedirect("/things/one-0-8", "/things/v1#v1.0.8")
	wantRedirect("/things/no-label", "/things/v2")
	if res := resolve("/things/no-sort"); res.Redirect != nil || res.Page == nil {
		t.Errorf("entry without a bucket must render: %+v", res)
	}
	if res := resolve("/nr/kept"); res.Redirect != nil || res.Page == nil {
		t.Errorf("redirect_entries off must render: %+v", res)
	}

	wantRedirect("/things?page=2", "/things")
	wantRedirect("/things/v1?page=1", "/things/v1")
	wantRedirect("/things/v1?page=", "/things/v1")
	wantRedirect("/capped?page=3", "/capped")
	if res := resolve("/things"); res.Redirect != nil || res.Archive == nil {
		t.Errorf("archive without ?page: %+v", res)
	}
	if res := resolve("/plain?page=2"); res.Redirect != nil || res.Archive == nil {
		t.Errorf("unconfigured archive must keep ?page: %+v", res)
	}
}

// An entry whose own URL is its group URL renders instead of redirecting to
// itself; any other entry still moves to its group page.
func TestEntryRedirectTargetSkipsOwnURL(t *testing.T) {
	t.Parallel()
	schema := &entity.EntitySchema{Slug: "item", Name: "Item", Settings: []byte(
		`{"url_pattern":"/things/{slug}","archive_path":"/things","archive_groups":{"attribute":"sort","divisor":1000,"path":"v{n}","redirect_entries":true}}`)}
	snap, err := entity.BuildURLSnapshot([]*entity.EntitySchema{schema}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := func(slug string) *resolvedPage {
		return &resolvedPage{Kind: resolveKindEntity, URL: "/things/" + slug, Entity: &entity.Entity{
			Slug: slug, Schema: schema, Attributes: []byte(`{"sort":1008}`)}}
	}
	if got := entryRedirectTarget(context.Background(), snap, "", res("v1")); got != "" {
		t.Errorf("own URL redirects to %q", got)
	}
	if got := entryRedirectTarget(context.Background(), snap, "", res("other")); got != "/things/v1" {
		t.Errorf("target = %q, want /things/v1", got)
	}
}
