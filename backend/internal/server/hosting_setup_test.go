package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/staticexport"
)

func TestUpstreamAddr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		host string
		port int
		want string
	}{
		{"", 8090, "127.0.0.1:8090"},
		{"0.0.0.0", 8090, "127.0.0.1:8090"},
		{"::", 8090, "127.0.0.1:8090"},
		{"127.0.0.1", 9000, "127.0.0.1:9000"},
		{"10.0.0.5", 8090, "10.0.0.5:8090"},
		{"::1", 8090, "[::1]:8090"},
		{"[::1]", 8090, "[::1]:8090"},
		{"app.internal", 80, "app.internal:80"},
	} {
		if got := upstreamAddr(tc.host, tc.port); got != tc.want {
			t.Errorf("upstreamAddr(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func setupFixture(t *testing.T, dir string) *hostingFixture {
	t.Helper()
	fx := newHostingFixture(t)
	fx.store.found = true
	fx.store.set = staticexport.Settings{Mode: staticexport.ModeStatic, Target: staticexport.TargetDir, DirPath: dir}
	fx.h.setup = hostingSetupDeps{
		adminPath: func() string { return "/admin" },
		upstream:  "127.0.0.1:8090",
		headers: func(context.Context, string) []secHeader {
			return []secHeader{{"X-Content-Type-Options", "nosniff"}, {"X-Frame-Options", "SAMEORIGIN"}}
		},
	}
	return fx
}

func TestHostingSetupDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rules := "/old /new 301\n/gone /elsewhere 302\n/blog/:p1 /posts/:p1 301\n"
	if err := os.WriteFile(filepath.Join(dir, staticexport.RedirectsFile), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	fx := setupFixture(t, dir)
	fx.h.setup.folder = func(string) string { return admin.HostingCheckOK }
	fx.h.setup.reach = func() (string, time.Time) { return admin.HostingCheckFailed, time.Unix(100, 0) }

	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Saved || !got.SameHost || got.Site != "https://www.example.com" || got.Upstream != "127.0.0.1:8090" {
		t.Fatalf("setup = %+v", got)
	}
	if got.Exact != 2 || got.Wildcard != 1 || !got.RedirectsSplit || got.RedirectsTotal != 3 {
		t.Errorf("redirects = %d exact, %d wildcard, split %v, total %d", got.Exact, got.Wildcard, got.RedirectsSplit, got.RedirectsTotal)
	}
	if !strings.Contains(got.Caddy, "reverse_proxy 127.0.0.1:8090") || !strings.Contains(got.Caddy, "/admin") {
		t.Errorf("caddy lacks the proxy or the admin path:\n%s", got.Caddy)
	}
	if !strings.Contains(got.Nginx, "proxy_pass http://127.0.0.1:8090") || !strings.Contains(got.Nginx, "/admin") {
		t.Errorf("nginx lacks the proxy or the admin path:\n%s", got.Nginx)
	}
	if got.Folder != admin.HostingCheckOK || got.Reach != admin.HostingCheckFailed || !got.ReachAt.Equal(time.Unix(100, 0)) {
		t.Errorf("checks = %q %q %v", got.Folder, got.Reach, got.ReachAt)
	}
	if strings.Join(got.HeaderNames, ",") != "X-Content-Type-Options,X-Frame-Options" {
		t.Errorf("header names = %v", got.HeaderNames)
	}
}

// No export yet and no checks wired: counts of zero, checks unknown, config still generated.
func TestHostingSetupWithoutExport(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, filepath.Join(t.TempDir(), "public"))
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Exact != 0 || got.Wildcard != 0 || got.Caddy == "" || got.Folder != admin.HostingCheckUnknown || got.Reach != admin.HostingCheckUnknown {
		t.Fatalf("setup = %+v", got)
	}
}

func TestHostingSetupSeparateLiveOrigin(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, t.TempDir())
	fx.store.set.LiveOrigin = "https://live.example.com"
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.SameHost || !strings.Contains(got.Caddy, "live.example.com") {
		t.Fatalf("setup = %+v", got)
	}
}

func TestHostingSetupUnsafeDirIsRefused(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, "/srv/site;evil")
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.GenError == "" || got.Caddy != "" || got.Nginx != "" {
		t.Fatalf("setup = %+v", got)
	}
}

func TestHostingSetupS3HasNoConfig(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, "")
	fx.store.set = staticexport.Settings{Mode: staticexport.ModeStatic, Target: staticexport.TargetS3, Bucket: "site"}
	fx.runs.runs = []staticexport.Run{
		{Status: admin.HostingRunFailed, Redirects: 99},
		{Status: admin.HostingRunOK, Redirects: 7},
		{Status: admin.HostingRunOK, Redirects: 1},
	}
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Saved || got.Caddy != "" || got.Nginx != "" || got.RedirectsSplit || got.RedirectsTotal != 7 {
		t.Fatalf("setup = %+v", got)
	}
}

func TestHostingSetupNothingSaved(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, "")
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Saved || got.Caddy != "" {
		t.Fatalf("setup = %+v", got)
	}
}
