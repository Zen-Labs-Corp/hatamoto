package redirect

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type upsertRecorder struct {
	*fakeStore
	mu    sync.Mutex
	paths []string
}

func (u *upsertRecorder) Upsert404Entry(_ context.Context, path, _, _ string) error {
	u.mu.Lock()
	u.paths = append(u.paths, path)
	u.mu.Unlock()
	return nil
}

// logged runs Log404Async for each path and returns what reached the store.
func logged(policy func() Log404Policy, paths ...string) []string {
	rec := &upsertRecorder{fakeStore: newFakeStore()}
	s := NewServiceWithStore(rec, slog.Default())
	if policy != nil {
		s.SetLogPolicy(policy)
	}
	for _, p := range paths {
		s.Log404Async(p, "", "ua")
	}
	s.Close() // drains the queue
	return rec.paths
}

func TestLog404PolicyNilLogsEverything(t *testing.T) {
	if got := logged(nil, "/a", "/b.php"); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestLog404PolicyZeroValueLogs(t *testing.T) {
	got := logged(func() Log404Policy { return Log404Policy{} }, "/a")
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestLog404PolicyDisabledSkips(t *testing.T) {
	got := logged(func() Log404Policy { return Log404Policy{Disabled: true} }, "/a", "/b")
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestLog404PolicyIgnore(t *testing.T) {
	got := logged(func() Log404Policy {
		return Log404Policy{Ignore: []string{"*.php", "/wp-admin/*"}}
	}, "/x.php", "/x.php?a=1", "/wp-admin/setup", "/about", "/a.phps")
	if len(got) != 2 || got[0] != "/about" || got[1] != "/a.phps" {
		t.Fatalf("got %v", got)
	}
}

func TestMatchIgnore(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/exact", "/exact", true},
		{"/exact", "/exact/", false},
		{"/exact", "/exact?x=1", true},
		{"/Exact", "/exact", false},
		{"*.php", "/a/b.php", true},
		{"*.php", "/a/b.php?q=.html", true},
		{"*.php", "/a/b.html?q=x.php", false},
		{"/wp-admin/*", "/wp-admin/", true},
		{"/wp-admin/*", "/wp-admin", false},
		{"/a/*/c", "/a/b/c", true},
		{"/a/*/c", "/a/b/d/c", true},
		{"/a/*/c", "/a/b/d", false},
		{"/*/feed", "/en/feed", true},
		{"*", "/anything", true},
		{"/a*b*c", "/aXbYbZc", true},
		{"/a*b*c", "/aXbYbZ", false},
		{"/a.b", "/aXb", false},
	}
	for _, c := range cases {
		if got := matchIgnore([]string{c.pattern}, c.path); got != c.want {
			t.Errorf("%q vs %q = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
	if matchIgnore(nil, "/a") {
		t.Error("no patterns must not match")
	}
}

// Paths beyond maxMatchPath skip pattern matching and are logged.
func TestLog404PolicyOverlongPathIsLogged(t *testing.T) {
	long := "/" + strings.Repeat("a", maxMatchPath) + ".php"
	short := "/" + strings.Repeat("a", maxMatchPath-10) + ".php"
	got := logged(func() Log404Policy { return Log404Policy{Ignore: []string{"*.php"}} }, long, short)
	if len(got) != 1 || got[0] != long {
		t.Fatalf("got %d paths", len(got))
	}
}
