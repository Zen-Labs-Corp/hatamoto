package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/adminpagestate"
)

func bannerRig(t *testing.T, name string, fn adminpagestate.BannerFunc) *rig {
	t.Helper()
	return newRig(t, func(d *Deps) {
		d.ModuleBanners = []adminpagestate.NamedBanner{{Name: name, Fn: fn}}
	})
}

func dangerBanner(context.Context) ([]adminpagestate.Banner, error) {
	return []adminpagestate.Banner{{Kind: "danger", Message: "Example needs attention.", Href: "/settings/modules/example", Label: "Review"}}, nil
}

func TestModuleBannerOwnerOnly(t *testing.T) {
	t.Parallel()
	g := bannerRig(t, "example", dangerBanner)
	rec := g.do("GET", "/admin", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{`data-hm-module-banner="example"`, "Example needs attention.", `href="/admin/settings/modules/example"`, "hm-callout--danger", "Review"} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	rec = g.do("GET", "/admin", "editor-token", nil, nil)
	if strings.Contains(rec.Body.String(), "data-hm-module-banner") {
		t.Error("editor must not see a module banner")
	}
}

func TestModuleBannerProviderFailuresShowNothing(t *testing.T) {
	t.Parallel()
	cases := map[string]adminpagestate.BannerFunc{
		"error": func(context.Context) ([]adminpagestate.Banner, error) { return nil, errors.New("boom") },
		"panic": func(context.Context) ([]adminpagestate.Banner, error) { panic("boom") },
		"block": func(ctx context.Context) ([]adminpagestate.Banner, error) { <-ctx.Done(); return nil, ctx.Err() },
		"kind": func(context.Context) ([]adminpagestate.Banner, error) {
			return []adminpagestate.Banner{{Kind: "loud", Message: "x"}, {Kind: "info"}}, nil
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := bannerRig(t, "example", fn)
			start := time.Now()
			rec := g.do("GET", "/admin", "owner-token", nil, nil)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-hm-module-banner") {
				t.Fatalf("status %d, banner shown: %v", rec.Code, strings.Contains(rec.Body.String(), "data-hm-module-banner"))
			}
			if time.Since(start) > 2*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
		})
	}
}

func TestModuleBannerDropsUnsafeHref(t *testing.T) {
	t.Parallel()
	g := bannerRig(t, "example", func(context.Context) ([]adminpagestate.Banner, error) {
		return []adminpagestate.Banner{{Kind: "info", Message: "Hello.", Href: "//evil.example"}}, nil
	})
	body := g.do("GET", "/admin", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Hello.") || strings.Contains(body, "evil.example") {
		t.Fatalf("banner should show without the link")
	}
}

func TestModuleBannersNilWithoutProviders(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	req := httptest.NewRequest("GET", "/admin", nil)
	if got := g.a.moduleBanners(req, &CurrentUser{Role: "owner"}); got != nil {
		t.Fatalf("got %v", got)
	}
}

func countingBanner(n *atomic.Int32) adminpagestate.BannerFunc {
	return func(ctx context.Context) ([]adminpagestate.Banner, error) {
		n.Add(1)
		return dangerBanner(ctx)
	}
}

func TestModuleBannerSkippedOnFragment(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	g := bannerRig(t, "example", countingBanner(&n))
	hdr := map[string]string{"HX-Request": "true", "HX-Target": "main"}
	if rec := g.do("GET", "/admin", "owner-token", nil, hdr); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if n.Load() != 0 {
		t.Fatalf("provider ran %d times on a fragment request", n.Load())
	}
	g.do("GET", "/admin", "owner-token", nil, nil)
	if n.Load() != 1 {
		t.Fatalf("provider ran %d times on a full page, want 1", n.Load())
	}
}

func TestModuleBannerRunsOnceOnRenderFailure(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	g := newSudoRig(t, func(d *Deps) {
		d.ModulePages = []adminpagestate.Page{failingPage()}
		d.ModuleBanners = []adminpagestate.NamedBanner{{Name: "example", Fn: countingBanner(&n)}}
	})
	rec := g.do("GET", "/admin/settings/modules/failing/parse", "owner-token", nil, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if n.Load() != 1 {
		t.Fatalf("provider ran %d times, want 1", n.Load())
	}
}
