package app

import (
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/middleware"
)

// Booting must switch both cookies to the neutral names, and serve must do it.
func TestUseNeutralCookieNames(t *testing.T) {
	oldS, oldC := auth.SessionCookieName, middleware.CSRFCookieName
	t.Cleanup(func() { auth.SessionCookieName, middleware.CSRFCookieName = oldS, oldC })

	useNeutralCookieNames(true)
	if auth.SessionCookieName != "__Host-s" || middleware.CSRFCookieName != "__Host-c" {
		t.Fatalf("secure names: %q %q", auth.SessionCookieName, middleware.CSRFCookieName)
	}
	useNeutralCookieNames(false)
	if auth.SessionCookieName != "s" || middleware.CSRFCookieName != "c" {
		t.Fatalf("plain names: %q %q", auth.SessionCookieName, middleware.CSRFCookieName)
	}

	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "useNeutralCookieNames(cfg.AdminCookieSecure)") {
		t.Fatal("serve no longer calls useNeutralCookieNames(cfg.AdminCookieSecure)")
	}
}

// Boot must check PUBLIC_API_PREFIX against the resolved (stored or pinned)
// admin address; Config.Validate only sees the pinned one.
func TestServeChecksPrefixAgainstResolvedAdmin(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "config.CheckPrefixAgainstAdmin(cfg.PublicSite.APIPrefix, adminAddr)") {
		t.Fatal("serve no longer calls config.CheckPrefixAgainstAdmin with the resolved admin address")
	}
}

// admin-url refuses an address that is the first segment of PUBLIC_API_PREFIX
// only if the CLI hands the configured prefix to the site view.
func TestAdminURLPassesAPIPrefixToSite(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "site.apiPrefix = cfg.PublicSite.APIPrefix") {
		t.Fatal("admin-url no longer copies cfg.PublicSite.APIPrefix into the site view")
	}
}
