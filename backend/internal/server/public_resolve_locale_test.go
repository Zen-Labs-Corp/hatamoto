package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

type pageSchemaLister struct{}

func (pageSchemaLister) List(context.Context) ([]*entity.EntitySchema, error) {
	return []*entity.EntitySchema{{Slug: "page", Name: "Page"}}, nil
}

// scopedRepo hides the rows outside the locale scope of ctx, like the public
// repository does.
type scopedRepo struct{ fakeResolveRepo }

func (r *scopedRepo) GetPublishedBySlugAndSchemas(ctx context.Context, slug string, schemas []string) (*entity.Entity, error) {
	e, err := r.fakeResolveRepo.GetPublishedBySlugAndSchemas(ctx, slug, schemas)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(entity.VisibleLocales(ctx), e.Locale) {
		return nil, entity.ErrEntityNotFound
	}
	return e, nil
}

type localeRouteFixture struct {
	deps       routeDeps
	redirected []string
	pageScope  []string
	homeScope  []string
	homeCalls  int
}

func newLocaleRouteFixture(t *testing.T, prefixAll bool, entries ...entity.LocaleEntry) *localeRouteFixture {
	t.Helper()
	reg := entity.NewURLRegistry(pageSchemaLister{}, nil)
	if len(entries) > 0 {
		set, err := entity.NewLocaleSet("en", entries, prefixAll, "")
		if err != nil {
			t.Fatal(err)
		}
		reg.SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
	}
	about := fixtureEntity("page", "about", "")
	about.Locale = "fr"
	welcome := fixtureEntity("page", "welcome", "")
	welcome.Locale = "en"
	f := &localeRouteFixture{}
	f.deps = routeDeps{
		snapshot: reg.Snapshot,
		repo:     &scopedRepo{fakeResolveRepo{rows: []*entity.Entity{about, welcome}}},
		checkRedirect: func(_ context.Context, path string) *redirect.Redirect {
			f.redirected = append(f.redirected, path)
			return nil
		},
		loadPage: func(r *http.Request, _ string) (*PublicPageResponse, pageOutcome, error) {
			f.pageScope = entity.VisibleLocales(r.Context())
			return &PublicPageResponse{}, pageOutcome{Status: 200}, nil
		},
		loadHome: func(r *http.Request) (*PublicPageResponse, pageOutcome, error) {
			f.homeCalls++
			f.homeScope = entity.VisibleLocales(r.Context())
			return &PublicPageResponse{}, pageOutcome{Status: 200}, nil
		},
	}
	return f
}

var fixtureLocales = []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}, {Tag: "nl", Segment: "nl"}}

func TestResolveRoute_LocaleScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		prefixAll bool
		entries   []entity.LocaleEntry
		path      string
		wantScope []string
		wantLoc   string
		wantRest  string
		wantURL   string
		notFound  bool
		wantHome  bool
	}{
		{name: "prefixed page", entries: fixtureLocales, path: "/fr/about", wantScope: []string{"fr", "und"}, wantLoc: "fr", wantRest: "/about", wantURL: "/fr/about"},
		{name: "default unprefixed", entries: fixtureLocales, path: "/about", wantScope: []string{"en", "und"}, wantLoc: "en", wantRest: "/about", notFound: true},
		{name: "default content unprefixed", entries: fixtureLocales, path: "/welcome", wantScope: []string{"en", "und"}, wantLoc: "en", wantRest: "/welcome", wantURL: "/welcome"},
		{name: "segment needs a whole match", entries: fixtureLocales, path: "/frx/about", wantLoc: "en", notFound: true, wantRest: "/frx/about"},
		{name: "locale home", entries: fixtureLocales, path: "/fr", wantScope: []string{"fr", "und"}, wantLoc: "fr", wantRest: "/", wantHome: true},
		{name: "locale home trailing slash", entries: fixtureLocales, path: "/nl/", wantScope: []string{"nl", "und"}, wantLoc: "nl", wantRest: "/", wantHome: true},
		{name: "prefix-all unprefixed is shared only", prefixAll: true, entries: []entity.LocaleEntry{{Tag: "en", Segment: "en"}, {Tag: "fr", Segment: "fr"}}, path: "/about", wantLoc: "und", notFound: true, wantRest: "/about"},
		{name: "single locale", path: "/about", notFound: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLocaleRouteFixture(t, tt.prefixAll, tt.entries...)
			res, _, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, tt.path, httptest.NewRequest("GET", "/x", nil))
			if err != nil || res == nil {
				t.Fatalf("got %+v %v", res, err)
			}
			if res.NotFound != tt.notFound || res.Locale != tt.wantLoc && len(tt.entries) > 0 {
				t.Fatalf("result = %+v", res)
			}
			if tt.wantHome != (f.homeCalls == 1) {
				t.Errorf("home calls = %d, want home %v", f.homeCalls, tt.wantHome)
			}
			if tt.wantHome && !reflect.DeepEqual(f.homeScope, tt.wantScope) {
				t.Errorf("home scope = %v, want %v", f.homeScope, tt.wantScope)
			}
			if !tt.wantHome && !tt.notFound && !reflect.DeepEqual(f.pageScope, tt.wantScope) {
				t.Errorf("page scope = %v, want %v", f.pageScope, tt.wantScope)
			}
			if len(tt.entries) > 0 && res.Rest != tt.wantRest {
				t.Errorf("rest = %q, want %q", res.Rest, tt.wantRest)
			}
			if tt.wantURL != "" && res.CanonicalURL != tt.wantURL {
				t.Errorf("canonical = %q, want %q", res.CanonicalURL, tt.wantURL)
			}
		})
	}
}

// Redirect rules see the path as requested, before the locale segment is
// stripped.
func TestResolveRoute_LocaleRedirectBeforeStrip(t *testing.T) {
	t.Parallel()
	f := newLocaleRouteFixture(t, false, fixtureLocales...)
	f.deps.checkRedirect = func(_ context.Context, path string) *redirect.Redirect {
		f.redirected = append(f.redirected, path)
		return &redirect.Redirect{TargetURL: "/moved", StatusCode: 301}
	}
	res, _, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, "/fr/old", httptest.NewRequest("GET", "/x", nil))
	if err != nil || res == nil || res.Redirect == nil || res.Redirect.Target != "/moved" {
		t.Fatalf("got %+v %v", res, err)
	}
	if !reflect.DeepEqual(f.redirected, []string{"/fr/old"}) {
		t.Errorf("redirect lookup saw %v", f.redirected)
	}
}

func TestHiddenLocalePrefix404(t *testing.T) {
	t.Parallel()
	entries := []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr", Hidden: true}, {Tag: "nl", Segment: "nl"}}
	for _, path := range []string{"/fr", "/fr/", "/fr/about", "/fr/anything/deep"} {
		f := newLocaleRouteFixture(t, false, entries...)
		var logged string
		f.deps.log404 = func(p, _, _ string) { logged = p }
		res, out, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, path, httptest.NewRequest("GET", "/x", nil))
		if err != nil || res == nil || !res.NotFound || out.Status != http.StatusNotFound {
			t.Fatalf("%s: got %+v %+v %v", path, res, out, err)
		}
		if f.homeCalls != 0 || f.pageScope != nil {
			t.Errorf("%s: a loader ran for a hidden locale", path)
		}
		if logged != strings.TrimSuffix(path, "/") {
			t.Errorf("%s: logged %q", path, logged)
		}
	}
	// A public sibling still resolves.
	f := newLocaleRouteFixture(t, false, entries...)
	res, _, err := newRouteTestServer().resolveRouteWith(context.Background(), f.deps, "/nl", httptest.NewRequest("GET", "/x", nil))
	if err != nil || res == nil || res.NotFound || f.homeCalls != 1 {
		t.Fatalf("public locale home: %+v %v", res, err)
	}
}

func TestLookupAgentEntityHiddenLocale(t *testing.T) {
	t.Parallel()
	f := newLocaleRouteFixture(t, false, entity.LocaleEntry{Tag: "en"}, entity.LocaleEntry{Tag: "fr", Segment: "fr", Hidden: true})
	snap, err := f.deps.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id, found, err := lookupAgentEntity(context.Background(), f.deps.repo, snap, "/fr/about")
	if err != nil || found || id != nil {
		t.Fatalf("hidden locale resolved: %v %v %v", id, found, err)
	}
}
