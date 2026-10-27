//go:build integration

package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// wireEnv is a migrated scratch database and a config for it.
type wireEnv struct {
	cfg *config.Config
	db  *database.DB
}

func newWireEnv(t *testing.T, extra map[string]string) *wireEnv {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; needs Postgres")
	}
	t.Setenv("DATABASE_URL", scratchMigratedDB(t, base))
	t.Setenv("JWT_SECRET", strings.Repeat("w", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "wire-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Wire IT")
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_WORKER", "off")
	t.Setenv("PUBLIC_BASE_URL", "")
	for k, v := range extra {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// What app's first boot seeds, and the address store needs.
	if _, err := db.Pool().Exec(context.Background(), `INSERT INTO hatamoto_site_config (company_name) VALUES ('Wire IT') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return &wireEnv{cfg: cfg, db: db}
}

// server builds a server that serves the admin at addr, as app boot would
// after resolving it, and logs to the returned buffer.
func (e *wireEnv) server(t *testing.T, addr string) (*Server, *bytes.Buffer) {
	t.Helper()
	cfg := *e.cfg
	cfg.Admin.Resolved = addr
	var logs bytes.Buffer
	s := New(&cfg, e.db, slog.New(slog.NewTextHandler(&logs, nil)))
	if s.renderErr != nil {
		t.Fatal(s.renderErr)
	}
	t.Cleanup(func() { s.stopProbes() })
	return s, &logs
}

func (e *wireEnv) addOwner(t *testing.T, s *Server) {
	t.Helper()
	hash, _ := auth.HashPassword("a-long-enough-password")
	if _, err := s.userRepo.Create(context.Background(), &user.CreateUserInput{Email: "owner@example.test", Role: user.RoleOwner, Name: "Owner"}, hash); err != nil {
		t.Fatal(err)
	}
}

func get(s *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "198.51.100.9:5000"
	// A top-level navigation, as the sealed-admin guard requires of a page GET.
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// After the stored address is changed and the server rebuilt (the restart),
// the old address is the standard 404 and the new one serves the login page.
func TestOldAddressNotFoundAfterChangeAndRestart(t *testing.T) {
	e := newWireEnv(t, nil)
	s1, _ := e.server(t, "/admin")
	e.addOwner(t, s1)
	if rec := get(s1, "/admin/login"); rec.Code != 200 {
		t.Fatalf("before the change /admin/login = %d", rec.Code)
	}

	next, err := adminpath.Generate()
	if err != nil {
		t.Fatal(err)
	}
	st := adminpath.NewStore(e.db.Pool())
	if err := st.Set(context.Background(), next, "test"); err != nil {
		t.Fatal(err)
	}
	stored, err := st.Get(context.Background())
	if err != nil || stored.Path == nil || *stored.Path != next {
		t.Fatalf("stored %v, %v", stored, err)
	}

	s2, _ := e.server(t, *stored.Path)
	old := get(s2, "/admin/login")
	guess := get(s2, "/zz-guess/login")
	if old.Code != 404 {
		t.Fatalf("old address after the change = %d, want 404", old.Code)
	}
	if strings.ReplaceAll(old.Body.String(), "/admin/login", "/zz-guess/login") != guess.Body.String() && old.Body.String() != guess.Body.String() {
		t.Errorf("the old address is not the public 404\n--- old ---\n%.400s\n--- guess ---\n%.400s", old.Body.String(), guess.Body.String())
	}
	if rec := get(s2, next+"/login"); rec.Code != 200 {
		t.Fatalf("new address login = %d, want 200", rec.Code)
	}
}

// robots.txt names the admin only at the default address.
func TestRobotsNeverRevealsNonDefault(t *testing.T) {
	e := newWireEnv(t, nil)
	next, _ := adminpath.Generate()
	s, _ := e.server(t, next)
	body := get(s, "/robots.txt").Body.String()
	if strings.Contains(body, next) || strings.Contains(body, "admin") || !strings.Contains(body, "Disallow: /api") {
		t.Errorf("robots.txt at a non-default address:\n%s", body)
	}
	s, _ = e.server(t, "/admin")
	if body = get(s, "/robots.txt").Body.String(); !strings.Contains(body, "Disallow: /admin\n") {
		t.Errorf("robots.txt at the default address lost its admin line:\n%s", body)
	}
}

// The first-run log line carries the real address, so the URL it prints works
// and the default address is the standard 404.
func TestSetupLogLineUsesPrefix(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		base string
		want func(prefix string) string
	}{
		{"generated code", nil, "", func(p string) string { return "Setup: open " + p + "/setup and enter code " }},
		{"configured base url", map[string]string{"PUBLIC_BASE_URL": "https://site.example.test/"}, "https://site.example.test", func(p string) string {
			return "Setup: open https://site.example.test" + p + "/setup and enter code "
		}},
		{"pinned code", map[string]string{"HATAMOTO_SETUP_CODE": "pinned-code-for-it-1"}, "", func(p string) string {
			return "Setup: open " + p + "/setup and enter the setup code from HATAMOTO_SETUP_CODE"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newWireEnv(t, tc.env)
			prefix, _ := adminpath.Generate()
			s, logs := e.server(t, prefix)
			if !strings.Contains(logs.String(), tc.want(prefix)) {
				t.Fatalf("log does not say %q:\n%s", tc.want(prefix), logs.String())
			}
			if strings.Contains(logs.String(), "/admin/setup") {
				t.Errorf("log still names /admin/setup:\n%s", logs.String())
			}
			if rec := get(s, prefix+"/setup"); rec.Code != 200 {
				t.Errorf("%s/setup = %d, want the wizard", prefix, rec.Code)
			}
			if rec := get(s, "/admin/setup"); rec.Code != http.StatusNotFound {
				t.Errorf("/admin/setup = %d, want 404", rec.Code)
			}
		})
	}
}
