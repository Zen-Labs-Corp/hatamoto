package config

import (
	"hatamoto.dev/core/internal/adminpath"
	"strings"
	"testing"
	"time"
)

func TestAgentAnalyticsConfigDefaultsOff(t *testing.T) {
	t.Setenv("AGENT_LOG_INGEST", "")
	t.Setenv("AGENT_LOG_INGEST_SECRET", "")
	t.Setenv("AGENT_LOG_CADDY_PATH", "")
	got := loadAgentAnalyticsConfig()
	if got.LogIngestEnabled || got.LogIngestSecret != "" || got.CaddyLogPath != "" || got.LogIngestActive() {
		t.Fatalf("defaults must be off/empty, got %+v", got)
	}
}

func TestAgentAnalyticsConfigEnv(t *testing.T) {
	t.Setenv("AGENT_LOG_INGEST", "true")
	t.Setenv("AGENT_LOG_INGEST_SECRET", "s3cret")
	t.Setenv("AGENT_LOG_CADDY_PATH", "/var/log/caddy/x.log")
	got := loadAgentAnalyticsConfig()
	if !got.LogIngestEnabled || got.LogIngestSecret != "s3cret" || got.CaddyLogPath != "/var/log/caddy/x.log" || !got.LogIngestActive() {
		t.Fatalf("got %+v", got)
	}
}

func TestAgentAnalyticsLogIngestInactiveCases(t *testing.T) {
	cases := map[string]AgentAnalyticsConfig{
		"flag on, no secret": {LogIngestEnabled: true},
		"secret, flag off":   {LogIngestSecret: "s"},
		"tail path only":     {CaddyLogPath: "/x"},
	}
	for name, c := range cases {
		if c.LogIngestActive() {
			t.Errorf("%s: must not be active", name)
		}
	}
}

func TestAgentAnalyticsNoDevSecretDerivation(t *testing.T) {
	t.Setenv("AGENT_LOG_INGEST", "1")
	t.Setenv("AGENT_LOG_INGEST_SECRET", "")
	t.Setenv("JWT_SECRET", "dev-jwt")
	t.Setenv("ENVIRONMENT", "development")
	if loadAgentAnalyticsConfig().LogIngestActive() {
		t.Fatal("secret must never be derived")
	}
}

func TestWorkerOnUnlessOptedOut(t *testing.T) {
	for env, want := range map[string]bool{"": true, "on": true, "off": false, "OFF": false} {
		t.Setenv("HATAMOTO_WORKER", env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.EnableWorker != want {
			t.Errorf("HATAMOTO_WORKER=%q: EnableWorker=%v, want %v", env, cfg.Server.EnableWorker, want)
		}
	}
}

func TestPageCacheTTL(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
		warn bool
	}{
		{"", DefaultPageCacheTTL, false},
		{"10m", 10 * time.Minute, false},
		{"30s", 30 * time.Second, false},
		{"24h", 24 * time.Hour, false},
		{"720h", 30 * 24 * time.Hour, false},
		{"721h", DefaultPageCacheTTL, true},
		{"29s", DefaultPageCacheTTL, true},
		{"0", DefaultPageCacheTTL, true},
		{"-5m", DefaultPageCacheTTL, true},
		{"soon", DefaultPageCacheTTL, true},
		{"300", DefaultPageCacheTTL, true}, // no unit
	}
	for _, c := range cases {
		t.Setenv("HATAMOTO_PAGE_CACHE_TTL", c.env)
		got, warns := loadPageCacheTTL(nil)
		if got != c.want || (len(warns) > 0) != c.warn {
			t.Errorf("%q: got %s, warnings %v; want %s, warn=%v", c.env, got, warns, c.want, c.warn)
		}
	}
}

func TestLoadSurfacesPageCacheTTLWarning(t *testing.T) {
	t.Setenv("HATAMOTO_PAGE_CACHE_TTL", "bogus")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, w := range cfg.Warnings {
		warned = warned || strings.Contains(w, "HATAMOTO_PAGE_CACHE_TTL")
	}
	if cfg.PageCacheTTL != DefaultPageCacheTTL || !warned {
		t.Fatalf("ttl %s warnings %v", cfg.PageCacheTTL, cfg.Warnings)
	}
}

func TestPageCacheTTLFromEnv(t *testing.T) {
	cases := []struct {
		env     string
		want    time.Duration
		fromEnv bool
		warn    bool
	}{
		{"", DefaultPageCacheTTL, false, false},
		{"10m", 10 * time.Minute, true, false},
		{"5s", DefaultPageCacheTTL, false, true},
		{"721h", DefaultPageCacheTTL, false, true},
	}
	for _, c := range cases {
		t.Setenv("HATAMOTO_PAGE_CACHE_TTL", c.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		warned := false
		for _, w := range cfg.Warnings {
			warned = warned || strings.Contains(w, "HATAMOTO_PAGE_CACHE_TTL")
		}
		if cfg.PageCacheTTL != c.want || cfg.PageCacheTTLFromEnv != c.fromEnv || warned != c.warn {
			t.Errorf("%q: ttl %s fromEnv %v warned %v; want %s %v %v",
				c.env, cfg.PageCacheTTL, cfg.PageCacheTTLFromEnv, warned, c.want, c.fromEnv, c.warn)
		}
	}
}

func TestValidatePublicAPIPrefix(t *testing.T) {
	for _, ok := range []string{"", "/x/v1", "/content/v2/pub", "/a_b/c-d"} {
		if err := ValidatePublicAPIPrefix(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"/api", "/api/v2", "/admin/x", "/x", "/x/", "/x/v1/", "x/v1", "/x//v1", "/X/v1", "/x/v 1", "/x/v1?a", "/p/x", "/health/x"} {
		err := ValidatePublicAPIPrefix(bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
		} else if !strings.Contains(err.Error(), "PUBLIC_API_PREFIX") {
			t.Errorf("%q: error does not name the variable: %v", bad, err)
		}
	}
}

func TestPublicAPIPrefixReservedRoots(t *testing.T) {
	for _, r := range adminpath.ReservedPrefixes {
		if r == "-" || r == "feed" || r == "sitemap" || r == ".well-known" || r == "favicon.ico" {
			continue // not a valid PUBLIC_API_PREFIX segment shape or checked below
		}
		if err := ValidatePublicAPIPrefix("/" + r + "/v1"); err == nil {
			t.Errorf("/%s/v1 accepted", r)
		}
	}
	for _, r := range []string{"-", "feed", "sitemap", "favicon.ico"} {
		if err := ValidatePublicAPIPrefix("/" + r + "/v1"); err == nil {
			t.Errorf("/%s/v1 accepted", r)
		}
	}
}

func TestCheckPrefixAgainstAdmin(t *testing.T) {
	if err := CheckPrefixAgainstAdmin("/studio/v1", "/studio"); err == nil {
		t.Error("prefix under the admin address accepted")
	}
	for _, c := range [][2]string{{"", "/studio"}, {"/x/v1", "/studio"}, {"/studio/v1", ""}} {
		if err := CheckPrefixAgainstAdmin(c[0], c[1]); err != nil {
			t.Errorf("%q vs %q: %v", c[0], c[1], err)
		}
	}
}

// Config.Validate is one of the two CheckPrefixAgainstAdmin call sites (the
// other is boot, pinned in app): a pinned admin address under the prefix fails.
func TestValidateChecksPrefixAgainstPinnedAdmin(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HATAMOTO_ADMIN_PATH", "/studio")
	t.Setenv("PUBLIC_API_PREFIX", "/studio/v1")
	c, lerr := Load()
	if lerr != nil {
		t.Fatal(lerr)
	}
	c.Admin.Path = "/studio"
	c.PublicSite.APIPrefix = "/studio/v1"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "PUBLIC_API_PREFIX") {
		t.Fatalf("Validate = %v, want the PUBLIC_API_PREFIX refusal", err)
	}
	c.PublicSite.APIPrefix = "/x/v1"
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "PUBLIC_API_PREFIX") {
		t.Fatalf("unrelated prefix refused: %v", err)
	}
}

func TestGeoIPDBEnv(t *testing.T) {
	for env, want := range map[string]string{"": "", " /data/country.mmdb ": "/data/country.mmdb"} {
		t.Setenv("HATAMOTO_GEOIP_DB", env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GeoIPDB != want {
			t.Errorf("HATAMOTO_GEOIP_DB=%q: GeoIPDB=%q, want %q", env, cfg.GeoIPDB, want)
		}
	}
}
