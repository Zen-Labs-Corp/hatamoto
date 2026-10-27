//go:build integration

package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// scratchDSN creates an empty database next to DATABASE_URL's and returns its DSN.
func scratchDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_serve_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// startServe builds the binary and runs `hatamoto serve` in an empty working
// directory with ONLY DATABASE_URL (plus a free port, so parallel runs do not
// collide) in its environment. It returns the base URL once /ready answers.
func startServe(t *testing.T, dsn string, extraEnv ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hatamoto")
	build := exec.Command("go", "build", "-o", bin, "../cmd/hatamoto")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	cmd := exec.Command(bin, "serve")
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir,
		"DATABASE_URL=" + dsn,
		fmt.Sprintf("API_PORT=%d", port), "API_HOST=127.0.0.1",
	}, extraEnv...)
	var logs strings.Builder
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return base
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server never became ready:\n%s", logs.String())
	return ""
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	// Like a browser opening the address: the admin refuses anything else.
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// `hatamoto serve` boots with DATABASE_URL as the only configuration: secrets
// are generated into ./data/secrets, every other required variable has a
// neutral default, migrations run, and the seeded home page renders.
func TestServeBootsWithOnlyDatabaseURL_Integration(t *testing.T) {
	base := startServe(t, scratchDSN(t))
	if code, body, _ := get(t, base+"/"); code != 200 || !strings.Contains(body, "My Hatamoto site") {
		t.Fatalf("GET / = %d\n%s", code, body)
	}
	if code, body, _ := get(t, base+"/health"); code != 200 {
		t.Fatalf("GET /health = %d %s", code, body)
	}
	// A fresh install serves the wizard under a random address, so the default
	// address is the standard 404 (fresh_install_integration_test.go follows the
	// wizard under the generated address).
	for _, p := range []string{"/admin/login", "/admin/setup"} {
		if code, _, _ := get(t, base+p); code != 404 {
			t.Fatalf("GET %s = %d, want the standard 404", p, code)
		}
	}
}
