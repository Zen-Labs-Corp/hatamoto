package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

type fakeSlugs map[string]bool

func (f fakeSlugs) GetBySlug(_ context.Context, slug string) (*entity.Entity, error) {
	if f[slug] {
		return &entity.Entity{Slug: slug, Status: entity.EntityStatus("draft")}, nil
	}
	return nil, entity.ErrEntityNotFound
}

type fakeRedirects map[string]bool

func (f fakeRedirects) Peek(_ context.Context, path string) *redirect.Redirect {
	if f[path] {
		return &redirect.Redirect{}
	}
	return nil
}

type schemaLister []*entity.EntitySchema

func (l schemaLister) List(context.Context) ([]*entity.EntitySchema, error) { return l, nil }

// Every literal first segment the core routes (except admin) must be reserved
// both by the list the CLI and boot use (Assess with no site) and by the
// server provider. A route added without a ReservedPrefixes entry fails here.
func TestEveryFixedRoutePrefixReserved(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	sr := &siteReserved{}
	ctx := context.Background()
	checked := 0
	for _, rt := range table {
		p := rt.Pattern
		if i := strings.IndexByte(p, ' '); i >= 0 {
			p = p[i+1:]
		}
		seg := firstSegment(p)
		if seg == "" || seg == adminSeg || strings.ContainsAny(seg, "*{}") {
			continue
		}
		checked++
		// Invalid counts too: a segment such as mcp (too short) or sitemap.xsl
		// (a dot) can never be chosen as an address.
		if res := adminpath.Assess(ctx, "/"+seg, nil); res.Level != adminpath.LevelReserved && res.Level != adminpath.LevelInvalid {
			t.Errorf("route %q: Assess(/%s) = %v, want reserved or invalid (add %q to adminpath.ReservedPrefixes)", rt.Pattern, seg, res.Level, seg)
		}
		if ok, _ := sr.Serves(ctx, "/"+seg); !ok {
			t.Errorf("route %q: the server provider does not reserve /%s", rt.Pattern, seg)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d route prefixes checked; route table parsing broke", checked)
	}
	if ok, _ := sr.Serves(ctx, "/auth"); !ok {
		t.Error("/auth must be reserved")
	}
}

func TestAdminIsNotReserved(t *testing.T) {
	sr := &siteReserved{}
	if ok, _ := sr.Serves(context.Background(), "/admin"); ok {
		t.Error("/admin must not be reserved")
	}
	res := adminpath.Assess(context.Background(), "/admin", sr)
	if res.Level != adminpath.LevelWeak {
		t.Errorf("/admin assessed %v, want weak", res.Level)
	}
}

func TestServesEntitySlugAnyStatus(t *testing.T) {
	sr := &siteReserved{slugs: fakeSlugs{"draft-page": true}}
	if ok, why := sr.Serves(context.Background(), "/draft-page"); !ok || why == "" {
		t.Errorf("slug not reserved: %v %q", ok, why)
	}
	if ok, _ := sr.Serves(context.Background(), "/other-thing"); ok {
		t.Error("free slug reserved")
	}
}

func TestServesRedirectSource(t *testing.T) {
	sr := &siteReserved{redirects: fakeRedirects{"/old-news": true}}
	if ok, _ := sr.Serves(context.Background(), "/old-news"); !ok {
		t.Error("redirect source not reserved")
	}
	if ok, _ := sr.Serves(context.Background(), "/old-other"); ok {
		t.Error("free path reserved")
	}
}

func TestServesURLRegistryLiteral(t *testing.T) {
	reg := entity.NewURLRegistry(schemaLister{{
		Slug:     "journal",
		Settings: json.RawMessage(`{"url_pattern":"/journal/{slug}","archive_path":"/stories"}`),
	}}, nil)
	sr := &siteReserved{urls: reg}
	for _, p := range []string{"/journal", "/stories"} {
		if ok, _ := sr.Serves(context.Background(), p); !ok {
			t.Errorf("%s not reserved", p)
		}
	}
	if ok, _ := sr.Serves(context.Background(), "/elsewhere"); ok {
		t.Error("free path reserved")
	}
}

// fakeRedirectList serves the redirect list as the service does: a substring
// search, paged.
type fakeRedirectList []*redirect.Redirect

func (f fakeRedirectList) ListRedirects(_ context.Context, p redirect.RedirectListParams) ([]*redirect.Redirect, int, error) {
	var hit []*redirect.Redirect
	for _, r := range f {
		if strings.Contains(strings.ToLower(r.SourcePath), strings.ToLower(p.Search)) {
			hit = append(hit, r)
		}
	}
	total := len(hit)
	lo := min((p.Page-1)*p.PerPage, total)
	return hit[lo:min(lo+p.PerPage, total)], total, nil
}

func TestServesRedirectBelowSegment(t *testing.T) {
	list := fakeRedirectList{
		{SourcePath: "/shop/old-item", IsActive: true},
		{SourcePath: "/off/x", IsActive: false},
		{SourcePath: "/a/news/b", IsActive: true}, // contains /news/ but does not start there
	}
	sr := &siteReserved{listed: list}
	ctx := context.Background()
	if ok, why := sr.Serves(ctx, "/shop"); !ok || why == "" {
		t.Errorf("/shop = %v %q, want reserved by the deeper redirect", ok, why)
	}
	for _, p := range []string{"/off", "/news", "/shopping", "/other"} {
		if ok, _ := sr.Serves(ctx, p); ok {
			t.Errorf("%s reserved without an active redirect below it", p)
		}
	}
	// A match past the first page is still found.
	big := fakeRedirectList{}
	for range redirectListPageSize {
		big = append(big, &redirect.Redirect{SourcePath: "/bulk/x", IsActive: false})
	}
	big = append(big, &redirect.Redirect{SourcePath: "/bulk/late", IsActive: true})
	if ok, _ := (&siteReserved{listed: big}).Serves(ctx, "/bulk"); !ok {
		t.Error("a redirect on a later page was missed")
	}
}

// The running-site view agrees with the CLI chooser (app/admin_url_site.go):
// an address that is the first segment of PUBLIC_API_PREFIX counts as served,
// so the post-boot warning stays consistent with what the CLI refuses.
func TestServesAPIPrefixSegment(t *testing.T) {
	sr := &siteReserved{apiPrefix: "/studio/v1"}
	if ok, _ := sr.Serves(context.Background(), "/studio"); !ok {
		t.Error("/studio must be reserved while PUBLIC_API_PREFIX is /studio/v1")
	}
	if res := adminpath.Assess(context.Background(), "/studio", sr); res.Level != adminpath.LevelReserved {
		t.Errorf("/studio assessed %v, want reserved", res.Level)
	}
	if ok, _ := (&siteReserved{}).Serves(context.Background(), "/studio"); ok {
		t.Error("/studio reserved without a PUBLIC_API_PREFIX")
	}
}
