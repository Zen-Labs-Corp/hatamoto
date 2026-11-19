package redirect

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

type upsertRecorder struct {
	*fakeStore
	mu         sync.Mutex
	paths      []string
	referrers  []string
	userAgents []string
}

func (u *upsertRecorder) Upsert404Entry(_ context.Context, path, referrer, userAgent string) error {
	u.mu.Lock()
	u.paths = append(u.paths, path)
	u.referrers = append(u.referrers, referrer)
	u.userAgents = append(u.userAgents, userAgent)
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

// Paths beyond maxMatchPath are not logged; shorter ones still go through the
// ignore patterns.
func TestLog404PolicyOverlongPathIsNotLogged(t *testing.T) {
	long := "/" + strings.Repeat("a", maxMatchPath) + ".php"
	short := "/" + strings.Repeat("a", maxMatchPath-10) + ".php"
	got := logged(func() Log404Policy { return Log404Policy{Ignore: []string{"*.php"}} }, long, short)
	if len(got) != 0 {
		t.Fatalf("got %d paths, want none", len(got))
	}
}

func TestLog404AsyncBoundsFields(t *testing.T) {
	rec := &upsertRecorder{fakeStore: newFakeStore()}
	s := NewServiceWithStore(rec, slog.Default())
	big := strings.Repeat("é", 1<<19) // 1 MB
	s.Log404Async("/missing", big, big)
	s.Log404Async("/"+strings.Repeat("a", maxMatchPath), "r", "ua") // 2049 bytes
	s.Close()
	if len(rec.paths) != 1 || rec.paths[0] != "/missing" {
		t.Fatalf("paths = %d, want only /missing", len(rec.paths))
	}
	for name, v := range map[string]string{"referrer": rec.referrers[0], "user agent": rec.userAgents[0]} {
		if len(v) == 0 || len(v) > 512 || !utf8.ValidString(v) {
			t.Errorf("%s: len %d valid=%v", name, len(v), utf8.ValidString(v))
		}
	}
}

// A multibyte rune straddling the byte limit is dropped whole, not split.
func TestClipFieldBacksOffToRuneStart(t *testing.T) {
	in := "a" + strings.Repeat("é", maxLoggedField) // é starts at odd offsets, so the limit lands mid-rune
	if utf8.RuneStart(in[maxLoggedField]) {
		t.Fatal("test input must straddle the limit")
	}
	got := clipField(in)
	if len(got) != maxLoggedField-1 || !utf8.ValidString(got) {
		t.Errorf("len %d valid=%v, want %d and valid", len(got), utf8.ValidString(got), maxLoggedField-1)
	}
}
