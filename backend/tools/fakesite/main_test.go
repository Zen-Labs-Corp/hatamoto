package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func logLines(t *testing.T, b *bytes.Buffer) []logLine {
	t.Helper()
	var res []logLine
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		var ll logLine
		if err := json.Unmarshal([]byte(l), &ll); err != nil {
			t.Fatalf("bad log line %q: %v", l, err)
		}
		res = append(res, ll)
	}
	return res
}

func setup(t *testing.T) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{"a.json": "{}", "x/index.html": "hi"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	srv := httptest.NewServer(newHandler(dir, &buf))
	t.Cleanup(srv.Close)
	return srv, &buf
}

func TestLogsQueryAndHeaders(t *testing.T) {
	srv, buf := setup(t)
	req, _ := http.NewRequest("GET", srv.URL+"/a.json?q=1", nil)
	req.Header.Set("User-Agent", "ua-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ls := logLines(t, buf)
	if len(ls) != 1 {
		t.Fatalf("want 1 line, got %d", len(ls))
	}
	l := ls[0]
	if l.Method != "GET" || l.Path != "/a.json" || l.Query != "q=1" || l.Status != 200 || l.Headers["User-Agent"][0] != "ua-test" {
		t.Fatalf("unexpected line %+v", l)
	}
}

func TestMissingFileIs404AndLogged(t *testing.T) {
	srv, buf := setup(t)
	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ls := logLines(t, buf)
	if resp.StatusCode != 404 || len(ls) != 1 || ls[0].Status != 404 {
		t.Fatalf("status %d lines %+v", resp.StatusCode, ls)
	}
}

func TestNoRedirectsNoListings(t *testing.T) {
	srv, _ := setup(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for path, want := range map[string]int{"/x/index.html": 200, "/x/": 404, "/": 404, "/../a.json": 200} {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: status %d want %d", path, resp.StatusCode, want)
		}
	}
}
