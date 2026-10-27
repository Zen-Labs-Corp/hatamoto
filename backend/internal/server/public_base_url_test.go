package server

import (
	"net/http/httptest"
	"testing"

	"hatamoto.dev/core/internal/config"
)

func TestPublicBaseURLPrefersConfiguredOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/platform", nil)

	s := &Server{cfg: &config.Config{}}
	if got := s.publicBaseURL(r); got != "http://127.0.0.1:8080" {
		t.Fatalf("unset PUBLIC_BASE_URL must fall back to the request origin, got %q", got)
	}

	s.cfg.PublicSite.BaseURL = " https://example.test/ "
	if got := s.publicBaseURL(r); got != "https://example.test" {
		t.Fatalf("configured origin must win (trimmed, no trailing slash), got %q", got)
	}

	r.Header.Set("X-Forwarded-Host", "attacker.test")
	if got := s.publicBaseURL(r); got != "https://example.test" {
		t.Fatalf("configured origin must ignore forwarded host headers, got %q", got)
	}
}
