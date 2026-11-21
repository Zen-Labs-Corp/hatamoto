package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
)

const (
	testSiteOrigin = "https://static.example.net"
	testLiveOrigin = "https://live.example.net"
)

func activeHosting() statichost.StaticHosting {
	return statichost.StaticHosting{Static: true, Live: testLiveOrigin, Site: testSiteOrigin}
}

func TestStaticCORS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		hs       statichost.StaticHosting
		origin   string
		wantACAO string
		wantVary bool
		wantSet  bool
	}{
		{"inactive", statichost.StaticHosting{}, testSiteOrigin, "", false, false},
		{"site", activeHosting(), testSiteOrigin, testSiteOrigin, true, true},
		{"evil", activeHosting(), "https://evil.example", "", true, false},
		{"null", activeHosting(), "null", "", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hd := http.Header{}
			if got := staticCORS(hd, tc.hs, tc.origin); got != tc.wantSet {
				t.Errorf("returned %v, want %v", got, tc.wantSet)
			}
			if got := hd.Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Errorf("ACAO = %q, want %q", got, tc.wantACAO)
			}
			if got := hd.Get("Vary") == "Origin"; got != tc.wantVary {
				t.Errorf("Vary Origin = %v, want %v", got, tc.wantVary)
			}
			if hd.Get("Access-Control-Allow-Credentials") != "" {
				t.Error("credentials must never be allowed")
			}
			if tc.name == "inactive" && len(hd) != 0 {
				t.Errorf("inactive must add no headers: %v", hd)
			}
		})
	}
}

func TestStaticPreflight(t *testing.T) {
	t.Parallel()
	hd := http.Header{}
	if !staticPreflight(hd, activeHosting(), testSiteOrigin, "POST, OPTIONS") {
		t.Fatal("S must be answered")
	}
	if hd.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" ||
		hd.Get("Access-Control-Allow-Headers") != "Content-Type, X-Page-URL" ||
		hd.Get("Access-Control-Max-Age") != "600" ||
		hd.Get("Access-Control-Allow-Origin") != testSiteOrigin {
		t.Errorf("headers = %v", hd)
	}
	if hd.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must never be allowed")
	}

	hd = http.Header{}
	if staticPreflight(hd, activeHosting(), "https://evil.example", "POST, OPTIONS") {
		t.Fatal("evil origin must be refused")
	}
	if hd.Get("Access-Control-Allow-Methods") != "" || hd.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("refused preflight leaked headers: %v", hd)
	}
}

func TestSetStaticHostingRoundTrip(t *testing.T) {
	t.Parallel()
	s := &Server{}
	if s.staticHosting().Active() {
		t.Fatal("zero holder must be served mode")
	}
	s.setStaticHosting(activeHosting())
	if !s.staticHosting().Active() {
		t.Fatal("holder must be active after set")
	}
}

func TestHostingSnapshot(t *testing.T) {
	t.Parallel()
	static := staticexport.Settings{Mode: staticexport.ModeStatic, LiveOrigin: testLiveOrigin}
	for _, tc := range []struct {
		name   string
		set    staticexport.Settings
		found  bool
		site   string
		active bool
		zero   bool
	}{
		{"no row", static, false, testSiteOrigin, false, true},
		{"served", staticexport.Settings{Mode: staticexport.ModeServed, LiveOrigin: testLiveOrigin}, true, testSiteOrigin, false, false},
		{"static", static, true, testSiteOrigin, true, false},
		{"invalid live origin", staticexport.Settings{Mode: staticexport.ModeStatic, LiveOrigin: "ftp://x"}, true, testSiteOrigin, false, false},
	} {
		var h statichost.Holder
		h.Store(hostingSnapshot(tc.set, tc.found, tc.site)) // the holder normalises and drops invalid origins
		got := h.Load()
		if got.Active() != tc.active || (tc.zero && got != statichost.StaticHosting{}) {
			t.Errorf("%s: snapshot %+v, active %v want %v", tc.name, got, got.Active(), tc.active)
		}
	}
}

func TestRefreshStaticHostingFrom(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.cfg = &config.Config{}
	s.cfg.PublicSite.BaseURL = testSiteOrigin
	s.refreshStaticHostingFrom(context.Background(), func(context.Context) (staticexport.Settings, bool, error) {
		return staticexport.Settings{Mode: staticexport.ModeStatic, LiveOrigin: testLiveOrigin}, true, nil
	})
	if got := s.staticHosting(); !got.Active() || got.Site != testSiteOrigin || got.Live != testLiveOrigin {
		t.Fatalf("snapshot = %+v, want active", got)
	}
	s.refreshStaticHostingFrom(context.Background(), func(context.Context) (staticexport.Settings, bool, error) {
		return staticexport.Settings{}, false, errors.New("db down")
	})
	if !s.staticHosting().Active() {
		t.Error("a read error dropped the previous snapshot")
	}
}

func TestRefreshStaticHostingNoDB(t *testing.T) {
	t.Parallel()
	s := &Server{}
	s.refreshStaticHosting(context.Background())
	if got := s.staticHosting(); got != (statichost.StaticHosting{}) {
		t.Errorf("snapshot = %+v, want zero", got)
	}
}

func TestStaticFetch_MarksExport(t *testing.T) {
	t.Parallel()
	var marked bool
	s := &Server{router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marked = statichost.IsExport(r.Context())
	})}
	base, _ := url.Parse(testSiteOrigin)
	if _, err := (&staticSource{s: s, base: base}).Fetch(context.Background(), "/about", io.Discard); err != nil {
		t.Fatal(err)
	}
	if !marked {
		t.Error("the export request is not marked with statichost.WithExport")
	}
}
