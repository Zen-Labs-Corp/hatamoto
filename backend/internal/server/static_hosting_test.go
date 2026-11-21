package server

import (
	"net/http"
	"testing"

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
