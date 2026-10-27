package admin

import (
	"errors"
	"strings"
	"testing"
)

func bannerOf(kind, msg string) *UpdateBanner {
	return &UpdateBanner{Kind: kind, Message: msg, Href: "/admin/settings/updates", Label: "View updates"}
}

func TestOwnerUpdateBanner(t *testing.T) {
	for _, c := range []struct{ kind, msg, class string }{
		{"rolled_back", "Update rolled back: v1.4.2 failed its health check and the site went back to v1.4.1.", "hm-callout--danger"},
		{"blocked", "An update is blocked: plugin hello needs SDK ^0.8.", "hm-callout--warning"},
		{"backup_failed", "The update was not installed because the backup before it failed: not enough free disk space.", "hm-callout--danger"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			f := baseUpdates()
			f.banner = bannerOf(c.kind, c.msg)
			g := newUpdatesRig(t, f)
			rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("GET = %d", rec.Code)
			}
			body := rec.Body.String()
			for _, w := range []string{strings.ReplaceAll(c.msg, "+", "&#43;"), c.class, `href="/admin/settings/updates"`, "View updates ›", `data-hm-update-banner="` + c.kind + `"`} {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			// An error-toned banner is never dismissible; the others are.
			if got, want := strings.Contains(body, "data-hm-callout-dismiss"), c.class != "hm-callout--danger"; got != want {
				t.Errorf("dismissible = %v, want %v", got, want)
			}
			compareGolden(t, "pages/banner-"+c.kind+".html", normalizePage(body))
		})
	}
}

func TestUpdateBannerAbsent(t *testing.T) {
	const marker = "data-hm-update-banner"
	mk := func() *updatesRig {
		f := baseUpdates()
		f.banner = bannerOf("rolled_back", "Update rolled back")
		return newUpdatesRig(t, f)
	}
	t.Run("editor", func(t *testing.T) {
		g := mk()
		if body := g.do("GET", "/admin/settings/general", "editor-token", nil, nil).Body.String(); strings.Contains(body, marker) {
			t.Error("editor sees the banner")
		}
		if g.svc.bannerGet != 0 {
			t.Errorf("service asked %d times for an editor", g.svc.bannerGet)
		}
	})
	t.Run("updates page", func(t *testing.T) {
		g := mk()
		if body := g.do("GET", "/admin/settings/updates", "owner-token", nil, nil).Body.String(); strings.Contains(body, marker) {
			t.Error("banner on the Updates page")
		}
	})
	t.Run("post render", func(t *testing.T) {
		g := mk()
		rec := g.do("POST", "/admin/settings/updates", "owner-token", goodUpdatesForm(), nil)
		_ = rec
		if g.svc.bannerGet != 0 {
			t.Errorf("banner computed on a non-GET (%d)", g.svc.bannerGet)
		}
	})
	t.Run("service error", func(t *testing.T) {
		g := mk()
		g.svc.bannerEr = errors.New("boom")
		rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), marker) {
			t.Errorf("code %d or banner shown on error", rec.Code)
		}
	})
	t.Run("nothing to say", func(t *testing.T) {
		g := newUpdatesRig(t, baseUpdates())
		if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, marker) {
			t.Error("banner with nil banner")
		}
	})
	t.Run("no service", func(t *testing.T) {
		g := newUpdatesRig(t, nil)
		if rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), marker) {
			t.Error("banner without a service")
		}
	})
}
