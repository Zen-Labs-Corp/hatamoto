package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/redirect"
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
	if got.GenError != "The folder contains characters a web server config cannot hold." || got.Caddy != "" || got.Nginx != "" {
		t.Fatalf("setup = %+v", got)
	}
}

// The refusal names the value that was unsafe, not always the folder.
func TestHostingSetupUnsafeHeaderNamesTheHeader(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, t.TempDir())
	fx.h.setup.headers = func(context.Context, string) []secHeader {
		return []secHeader{{"Content-Security-Policy", "default-src ${evil}"}}
	}
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := `The header "Content-Security-Policy" contains characters a web server config cannot hold.`; got.GenError != want || got.Caddy != "" {
		t.Fatalf("GenError = %q, want %q", got.GenError, want)
	}
}

func TestHostingSetupNeedsSiteAddress(t *testing.T) {
	t.Parallel()
	fx := setupFixture(t, t.TempDir())
	fx.site = ""
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.GenError != "Set the site address in Site settings first." || got.Caddy != "" {
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

// The upload ceilings reach the generated nginx config: the public one (media
// and form files, plus framing) server-wide, the larger admin one (the WordPress
// export) inside the admin location only.
func TestHostingSetupPassesUploadLimit(t *testing.T) {
	t.Parallel()
	if got := uploadLimitMB(10); got != max(10, form.MaxFileUploadMB)+5 {
		t.Errorf("public limit for the default media size = %d", got)
	}
	if got := uploadLimitMB(1000); got != 1005 {
		t.Errorf("public limit for 1000 MB media = %d", got)
	}
	if got, floor := adminUploadLimitMB(10), int(importjob.MaxWXRBytes>>20); got <= floor || got <= uploadLimitMB(10) {
		t.Errorf("admin limit = %d, must exceed the export limit %d MB and the public one", got, floor)
	}
	fx := setupFixture(t, t.TempDir())
	fx.h.setup.maxBodyMB, fx.h.setup.adminMB = uploadLimitMB(10), adminUploadLimitMB(10)
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server, admin, ok := strings.Cut(got.Nginx, "location ^~ /admin/ {")
	if !ok || !strings.Contains(server, fmt.Sprintf("client_max_body_size %dm;", uploadLimitMB(10))) ||
		strings.Contains(server, fmt.Sprintf("client_max_body_size %dm;", adminUploadLimitMB(10))) ||
		!strings.Contains(admin, fmt.Sprintf("client_max_body_size %dm;", adminUploadLimitMB(10))) {
		t.Errorf("nginx config limits are wrong:\n%s", got.Nginx)
	}
}

func sftpSetupFixture(t *testing.T) *hostingFixture {
	t.Helper()
	fx := setupFixture(t, "")
	fx.store.set = staticexport.Settings{Mode: staticexport.ModeStatic, Target: staticexport.TargetSFTP,
		SFTPHost: "ssh.example.com", SFTPPort: 22, SFTPUser: "site", SFTPAuth: staticexport.SFTPAuthPassword,
		SFTPDir: "public_html", SFTPHostKey: "SHA256:pinned", SFTPHTAccess: true}
	sealed, err := staticexport.SealSFTPSecret(fx.h.key, "pw")
	if err != nil {
		t.Fatal(err)
	}
	fx.store.set.SFTPSecretSealed = sealed
	fx.h.setup.rules = func(context.Context) ([]*redirect.Redirect, error) {
		return []*redirect.Redirect{
			{SourcePath: "/old", TargetURL: "/new", StatusCode: 301},
			{SourcePath: "/blog/*", TargetURL: "/posts/$1", StatusCode: 302},
		}, nil
	}
	return fx
}

func TestHostingSetupSFTPPreview(t *testing.T) {
	t.Parallel()
	fx := sftpSetupFixture(t)
	key := staticexport.SFTPKey("site", "ssh.example.com", 22, "public_html")
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Saved || got.Target != staticexport.TargetSFTP || got.HostKey != "SHA256:pinned" || !got.HTAccess ||
		got.RemoteDir != "public_html" || got.Remote != admin.HostingCheckUnknown {
		t.Fatalf("setup = %+v", got)
	}
	if !strings.Contains(got.HTAccessBlock, "RedirectMatch 301 ^/old/?$ /new") || !strings.Contains(got.HTAccessBlock, "RewriteRule") {
		t.Fatalf("block = %q", got.HTAccessBlock)
	}
	if got.Exact != 1 || got.Wildcard != 1 || !got.RedirectsSplit || got.RedirectsTotal != 2 || got.Caddy != "" || got.Nginx != "" {
		t.Fatalf("setup = %+v", got)
	}
	// Newest run to THIS target decides; another target's runs are ignored; a running row is skipped.
	fx.runs.runs = []staticexport.Run{
		{Target: "sftp:other@ssh.example.com:22/public_html", Status: admin.HostingRunFailed},
		{Target: key, Status: admin.HostingRunRunning},
		{Target: key, Status: admin.HostingRunOK},
		{Target: key, Status: admin.HostingRunFailed},
	}
	if got, _ = fx.h.Setup(context.Background()); got.Remote != admin.HostingCheckOK {
		t.Fatalf("remote = %q, want ok", got.Remote)
	}
	fx.runs.runs = []staticexport.Run{{Target: key, Status: admin.HostingRunFailed}, {Target: key, Status: admin.HostingRunOK}}
	if got, _ = fx.h.Setup(context.Background()); got.Remote != admin.HostingCheckFailed {
		t.Fatalf("remote = %q, want failed", got.Remote)
	}
	// No rule source: no preview, and no dial either (there is no probe on the deps).
	fx.h.setup.rules = nil
	if got, _ = fx.h.Setup(context.Background()); got.HTAccessBlock != "" || got.RedirectsSplit {
		t.Fatalf("setup without rules = %+v", got)
	}
}

func TestHostingSetupSFTPIgnoresStaleDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, staticexport.RedirectsFile), []byte("/stale /x 301\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx := sftpSetupFixture(t)
	fx.store.set.DirPath = dir
	got, err := fx.h.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Caddy != "" || got.Nginx != "" || got.Folder != admin.HostingCheckUnknown || got.Exact != 1 {
		t.Fatalf("a stale directory leaked into the SFTP setup: %+v", got)
	}
}
