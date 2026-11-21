package server

import (
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/statichost"
)

const (
	testStaticSite = "https://static.example.net"
	testStaticLive = "https://live.example.net"
)

func setStaticForTest(srv *Server) {
	srv.setStaticHosting(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
}

func TestStaticCORSPreflightFromSite(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	rec := corsDo(corsMux(srv), http.MethodOptions, "/api/v1/public/forms/contact/submit", testStaticSite, "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	hd := rec.Header()
	if hd.Get("Access-Control-Allow-Origin") != testStaticSite {
		t.Errorf("ACAO = %q", hd.Get("Access-Control-Allow-Origin"))
	}
	if hd.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" {
		t.Errorf("Allow-Methods = %q", hd.Get("Access-Control-Allow-Methods"))
	}
	if !strings.Contains(hd.Get("Access-Control-Allow-Headers"), "X-Page-URL") {
		t.Errorf("Allow-Headers = %q", hd.Get("Access-Control-Allow-Headers"))
	}
	if hd.Get("Vary") != "Origin" {
		t.Errorf("Vary = %q", hd.Get("Vary"))
	}
	assertNoCredentials(t, rec)
}

func TestStaticCORSPreflightEvilOrigin(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	rec := corsDo(corsMux(srv), http.MethodOptions, "/api/v1/public/forms/contact/submit", "https://evil.example", "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("ACAO = %q", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Methods"); v != "" {
		t.Errorf("Allow-Methods = %q", v)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}
	assertNoCredentials(t, rec)
}

func TestStaticCORSTokenAndTrackFromSite(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	h := corsMux(srv)
	rec := corsDo(h, http.MethodGet, "/api/v1/public/forms/contact/token", testStaticSite, "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
		t.Errorf("token: %d ACAO=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
	assertNoCredentials(t, rec)
	rec = corsDo(h, http.MethodPost, "/api/v1/track", testStaticSite, "", "")
	if rec.Header().Get("Access-Control-Allow-Origin") != testStaticSite {
		t.Errorf("track ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	assertNoCredentials(t, rec)
}

func TestStaticCORSNullStillAnswered(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	setStaticForTest(srv)
	rec := corsDo(corsMux(srv), http.MethodGet, "/api/v1/public/forms/contact/token", "null", "", "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Errorf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	assertNoCredentials(t, rec)
}

func TestStaticCORSServedModeUnchanged(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	h := corsMux(srv)
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		rec := corsDo(h, method, "/api/v1/public/forms/contact/token", "https://evil.example", "", "")
		if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("%s ACAO = %q", method, v)
		}
		if _, ok := rec.Header()["Vary"]; ok {
			t.Errorf("%s Vary = %q, want none", method, rec.Header().Values("Vary"))
		}
	}
}
