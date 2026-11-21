package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/safefetch"
)

func probeFor(allowPrivate bool) *reachProbe {
	p := newReachProbe()
	c := safefetch.NewClient(safefetch.ClientOptions{AllowPrivate: allowPrivate, Timeout: 2 * time.Second})
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p.client = c
	p.now = func() time.Time { return time.Unix(500, 0) }
	return p
}

func TestReachProbe(t *testing.T) {
	t.Parallel()

	var method string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	p := probeFor(true)
	if st, at := p.result(); st != admin.HostingCheckUnknown || !at.IsZero() {
		t.Fatalf("before any check = %q %v", st, at)
	}
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"ok", http.StatusOK, admin.HostingCheckOK},
		{"redirect is an answer", http.StatusMovedPermanently, admin.HostingCheckOK},
		{"server error", http.StatusBadGateway, admin.HostingCheckFailed},
		{"missing", http.StatusNotFound, admin.HostingCheckFailed},
	} {
		status = tc.status
		p.check(context.Background(), srv.URL)
		if st, at := p.result(); st != tc.want || !at.Equal(time.Unix(500, 0)) {
			t.Errorf("%s: result = %q %v, want %q", tc.name, st, at, tc.want)
		}
		if method != http.MethodHead {
			t.Errorf("%s: probe used %s, want HEAD", tc.name, method)
		}
	}
}

func TestReachProbeUnreachableFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	p := probeFor(true)
	p.check(context.Background(), url)
	if st, _ := p.result(); st != admin.HostingCheckFailed {
		t.Fatalf("result = %q", st)
	}
}

// A private address is never fetched; an unset site is not checked either.
func TestReachProbeBlockedAndUnsetAreNotChecked(t *testing.T) {
	t.Parallel()
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	t.Cleanup(srv.Close)

	p := probeFor(false) // the production setting
	p.check(context.Background(), srv.URL)
	if st, _ := p.result(); st != admin.HostingCheckUnknown || hit {
		t.Fatalf("loopback: result %q, reached %v", st, hit)
	}
	p = probeFor(true)
	p.check(context.Background(), "")
	if st, _ := p.result(); st != admin.HostingCheckUnknown {
		t.Fatalf("unset site: result %q", st)
	}
}

func TestFolderState(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("folder check is not available here")
	}
	root := t.TempDir()
	if got := folderState(root); got != admin.HostingCheckOK {
		t.Errorf("existing dir = %q", got)
	}
	if got := folderState(filepath.Join(root, "public")); got != admin.HostingCheckMissing {
		t.Errorf("missing dir under a writable parent = %q", got)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := folderState(file); got != admin.HostingCheckNotWritable {
		t.Errorf("a file = %q", got)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write everywhere")
	}
	ro := filepath.Join(root, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	if got := folderState(ro); got != admin.HostingCheckNotWritable {
		t.Errorf("read-only dir = %q", got)
	}
	if got := folderState(filepath.Join(ro, "public")); got != admin.HostingCheckNotWritable {
		t.Errorf("missing dir under a read-only parent = %q", got)
	}
}

// The export stages in the parent and renames, so a writable folder under a
// read-only parent still fails.
func TestFolderStateExistingWritableDirUnderReadOnlyParent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("folder check is not available here")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write everywhere")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "public")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if got := folderState(dir); got != admin.HostingCheckNotWritable {
		t.Errorf("writable dir under a read-only parent = %q, want not writable", got)
	}
}
