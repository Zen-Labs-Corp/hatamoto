package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/siteaccess"
	"hatamoto.dev/core/internal/staticexport"
)

func TestStaticFetch_RequestShape(t *testing.T) {
	var got *http.Request
	var uncounted bool
	s := &Server{router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		uncounted = redirect.IsUncounted(r.Context())
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body"))
	})}
	base, _ := url.Parse("https://example.com")
	src := &staticSource{s: s, base: base}

	var body bytes.Buffer
	resp, err := src.Fetch(context.Background(), "/blog/x?y=1", &body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusTeapot || body.String() != "body" || resp.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("resp = %+v body = %q", resp, body.String())
	}
	if got.Host != "example.com" || got.URL.Path != "/blog/x" || got.URL.RawQuery != "y=1" {
		t.Fatalf("request = %s %s", got.Host, got.URL)
	}
	if got.RemoteAddr != "127.0.0.1:0" || got.TLS == nil {
		t.Fatalf("remote = %q tls = %v", got.RemoteAddr, got.TLS != nil)
	}
	for _, h := range []string{"Accept", "Accept-Encoding", "Cookie"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("%s = %q, want unset", h, v)
		}
	}
	if got.Header.Get("User-Agent") != "hatamoto-static-export" || got.Header.Get("Sec-Purpose") != "prefetch" {
		t.Errorf("headers = %v", got.Header)
	}
	if !uncounted {
		t.Error("redirect hits are counted under the export")
	}
}

func TestStaticAgentUANotMatched(t *testing.T) {
	if a := agents.Default().Match("hatamoto-static-export"); a != nil {
		t.Fatalf("the export user agent matches agent %q", a.ID)
	}
}

func TestStaticFetch_PanicBecomesError(t *testing.T) {
	s := &Server{router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })}
	base, _ := url.Parse("https://example.com")
	src := &staticSource{s: s, base: base}
	if _, err := src.Fetch(context.Background(), "/", &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStaticSite_RefusesGateOn(t *testing.T) {
	s := &Server{cfg: &config.Config{}, accessSettings: func(context.Context) (siteaccess.Settings, error) {
		return siteaccess.Settings{Mode: siteaccess.ModeMaintenance}, nil
	}}
	s.cfg.PublicSite.BaseURL = "https://example.com"
	_, err := (&staticSource{s: s}).Site(context.Background())
	if !errors.Is(err, staticexport.ErrSiteAccessOn) {
		t.Fatalf("err = %v", err)
	}
}

func TestStaticSite_RefusesNoBaseURL(t *testing.T) {
	s := &Server{cfg: &config.Config{}, accessSettings: func(context.Context) (siteaccess.Settings, error) {
		return siteaccess.Settings{Mode: siteaccess.ModeOpen}, nil
	}}
	_, err := (&staticSource{s: s}).Site(context.Background())
	if !errors.Is(err, staticexport.ErrRefused) {
		t.Fatalf("err = %v", err)
	}
}

func TestStaticSite_ResolvesBaseAndAdmin(t *testing.T) {
	s := &Server{cfg: &config.Config{}, accessSettings: func(context.Context) (siteaccess.Settings, error) {
		return siteaccess.Settings{Mode: siteaccess.ModeOpen}, nil
	}}
	s.cfg.PublicSite.BaseURL = " https://example.com/ "
	s.cfg.Admin.Resolved = "/k3x9-admin"
	site, err := (&staticSource{s: s}).Site(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if site.Base.String() != "https://example.com" || site.AdminPrefix != "/k3x9-admin" {
		t.Fatalf("site = %+v", site)
	}
}
