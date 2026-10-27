package server

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactAdminPath(t *testing.T) {
	s := &Server{}
	cases := []struct{ prefix, in, want string }{
		{"/hq-x7k", "/hq-x7k", "<admin>"},
		{"/hq-x7k", "/hq-x7k/a", "<admin>/a"},
		{"/hq-x7k", "/hq-x7kzz", "/hq-x7kzz"},
		{"/hq-x7k", "/admin/x", "/admin/x"},
		{"/hq-x7k", "/", "/"},
		{"/admin", "/admin/x", "<admin>/x"},
		{"/admin", "/admin", "<admin>"},
		{"/hq-x7k", "//hq-x7k/x", "<admin>/x"},
		{"/hq-x7k", "/a/../hq-x7k/x", "<admin>/x"},
		{"/hq-x7k", "/hq-x7k/../hq-x7k/x", "<admin>/x"},
		{"/hq-x7k", "/hq-x7k/a/../../hq-x7k", "<admin>"},
		{"/hq-x7k", "/a/hq-x7k/x", "/a/<admin>/x"},
		{"/hq-x7k", "/a/../b/hq-x7kzz", "/a/../b/hq-x7kzz"},
	}
	for _, c := range cases {
		s.adminBundle.Store(&adminBundle{prefix: c.prefix})
		if got := s.redactAdminPath(c.in); got != c.want {
			t.Errorf("prefix %s: redact(%q) = %q, want %q", c.prefix, c.in, got, c.want)
		}
	}
}

func TestRedactAdminPathFollowsRuntimeSwap(t *testing.T) {
	s := &Server{}
	s.adminBundle.Store(&adminBundle{prefix: "/old-aaa"})
	s.adminBundle.Store(&adminBundle{prefix: "/new-bbb"})
	if got := s.redactAdminPath("/new-bbb/x"); got != "<admin>/x" {
		t.Errorf("new prefix not redacted: %q", got)
	}
	if got := s.redactAdminPath("/old-aaa/x"); got != "/old-aaa/x" {
		t.Errorf("retired prefix is just another path: %q", got)
	}
}

// The full chain logs the admin address as <admin>, never the secret prefix.
func TestBuildHandlerLogsRedactedAdminPath(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&buf, nil))
	s.cfg.Admin.Resolved = "/hq-x7k"
	s.setupRoutes()
	h := s.buildHandler()
	if s.stopProbes != nil {
		t.Cleanup(s.stopProbes)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/hq-x7k/settings", nil))
	out := buf.String()
	// The route shell has nil collaborators, so the request may log as a
	// recovered panic; either logger must show the redacted path.
	if !strings.Contains(out, "path=<admin>/settings") {
		t.Fatalf("no redacted path in the log:\n%s", out)
	}
	if strings.Contains(out, "hq-x7k") {
		t.Fatalf("secret prefix reached the log:\n%s", out)
	}
}
