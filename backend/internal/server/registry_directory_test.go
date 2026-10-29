package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/permtext"
	"hatamoto.dev/core/internal/render"
)

// dirRig is a registry served over httptest, signed with a test key, read
// through a registryDirectory driven by a fake clock.
type dirRig struct {
	t       *testing.T
	key     packages.Key
	trust   packages.TrustSet
	srv     *httptest.Server
	hits    atomic.Int32
	clock   *time.Time
	logs    *bytes.Buffer
	dir     *registryDirectory
	mu      sync.Mutex
	body    []byte
	status  int
	release chan struct{} // when set, the handler waits on it
}

func newDirRig(t *testing.T) *dirRig {
	t.Helper()
	key, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	trust := packages.TrustSet{}
	trust.Add(pub)
	now := time.Now().UTC().Truncate(time.Second)
	r := &dirRig{t: t, key: key, trust: trust, clock: &now, logs: &bytes.Buffer{}, status: 200}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.hits.Add(1)
		r.mu.Lock()
		body, status, rel := r.body, r.status, r.release
		r.mu.Unlock()
		if rel != nil {
			<-rel
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	r.publish(24*time.Hour, packages.Entry{Kind: packages.KindPlugin, Name: "zeta", Version: "1.0.0", Title: "Zeta Tool", Tier: packages.TierFirstParty, Author: "hidden", Source: "https://example.org/zeta", Permissions: []string{"db", "kv"}})
	reg := &packages.Registry{URL: r.srv.URL, Client: r.srv.Client(), Trust: trust, AllowInsecure: true, Now: func() time.Time { return *r.clock }}
	r.dir = newRegistryDirectory(func() *packages.Registry { return reg }, slog.New(slog.NewTextHandler(r.logs, nil)))
	r.dir.now = func() time.Time { return *r.clock }
	return r
}

// publish replaces the served index: valid for life from the fake now.
func (r *dirRig) publish(life time.Duration, entries ...packages.Entry) {
	r.t.Helper()
	for i := range entries {
		entries[i].URL = "p/" + entries[i].Name + ".hmp"
		entries[i].SHA256 = strings.Repeat("a", 64)
	}
	idx := packages.Index{Version: 1, IssuedAt: r.clock.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: r.clock.Add(life).Format(time.RFC3339), Packages: entries}
	raw, err := json.Marshal(idx)
	if err != nil {
		r.t.Fatal(err)
	}
	doc, err := packages.SignIndex(raw, r.key)
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	r.body, r.status = doc, 200
	r.mu.Unlock()
}

func (r *dirRig) fail() {
	r.mu.Lock()
	r.status, r.body = 500, []byte("boom")
	r.mu.Unlock()
}

func (r *dirRig) advance(d time.Duration) { *r.clock = r.clock.Add(d) }

func TestRegistryDirectoryCardsSortedPluginsOnly(t *testing.T) {
	r := newDirRig(t)
	r.publish(24*time.Hour,
		packages.Entry{Kind: packages.KindPlugin, Name: "zeta", Version: "v1.0.0", Tier: packages.TierFirstParty, Author: "hidden", Source: "https://example.org/zeta", Permissions: []string{"db", "kv"}},
		packages.Entry{Kind: packages.KindTheme, Name: "aaa-theme", Version: "1.0.0"},
		packages.Entry{Kind: packages.KindPlugin, Name: "alpha", Version: "2.0.0", Title: "Alpha", Description: "Does <b>things</b>", Tier: packages.TierCommunity, Author: "acme", Source: "http://example.org/alpha"},
	)
	v := r.dir.View(context.Background())
	if v.State != render.PluginDirectoryOK || len(v.Cards) != 2 {
		t.Fatalf("view = %+v", v)
	}
	a, z := v.Cards[0], v.Cards[1]
	if a.Name != "alpha" || z.Name != "zeta" {
		t.Fatalf("order: %s, %s", a.Name, z.Name)
	}
	if a.Version != "v2.0.0" || z.Version != "v1.0.0" {
		t.Errorf("versions %q %q", a.Version, z.Version)
	}
	if a.TierLabel != "Community" || a.Author != "acme" || z.TierLabel != "First-party" || z.Author != "" {
		t.Errorf("tier/author: %+v %+v", a, z)
	}
	if a.Source != "" || z.Source != "https://example.org/zeta" {
		t.Errorf("source: %q %q", a.Source, z.Source)
	}
	if a.Search != "alpha alpha does <b>things</b>" {
		t.Errorf("search %q", a.Search)
	}
}

func TestRegistryDirectoryEmptyIndex(t *testing.T) {
	r := newDirRig(t)
	r.publish(time.Hour)
	if v := r.dir.View(context.Background()); v.State != render.PluginDirectoryEmpty {
		t.Fatalf("state %q", v.State)
	}
}

func TestRegistryDirectoryCachesForThreeMinutes(t *testing.T) {
	r := newDirRig(t)
	ctx := context.Background()
	r.dir.View(ctx)
	r.advance(directoryTTL - time.Second)
	r.dir.View(ctx)
	if n := r.hits.Load(); n != 1 {
		t.Fatalf("hits within TTL = %d", n)
	}
	r.advance(2 * time.Second)
	r.dir.View(ctx)
	if n := r.hits.Load(); n != 2 {
		t.Fatalf("hits after TTL = %d", n)
	}
}

func TestRegistryDirectoryUnavailableStates(t *testing.T) {
	t.Run("fetch error", func(t *testing.T) {
		r := newDirRig(t)
		r.fail()
		if v := r.dir.View(context.Background()); v.State != render.PluginDirectoryUnavailable {
			t.Fatalf("state %q", v.State)
		}
	})
	t.Run("untrusted signer", func(t *testing.T) {
		r := newDirRig(t)
		other, _ := packages.GenerateKey()
		r.key = other
		r.publish(time.Hour, packages.Entry{Kind: packages.KindPlugin, Name: "x", Version: "1"})
		if v := r.dir.View(context.Background()); v.State != render.PluginDirectoryUnavailable {
			t.Fatalf("state %q", v.State)
		}
	})
	t.Run("expired index", func(t *testing.T) {
		r := newDirRig(t)
		r.publish(-time.Hour, packages.Entry{Kind: packages.KindPlugin, Name: "x", Version: "1"})
		if v := r.dir.View(context.Background()); v.State != render.PluginDirectoryUnavailable {
			t.Fatalf("state %q", v.State)
		}
	})
	t.Run("registry off and nil packages", func(t *testing.T) {
		for _, reg := range []*packages.Registry{nil, {URL: "off"}} {
			var logs bytes.Buffer
			d := newRegistryDirectory(func() *packages.Registry { return reg }, slog.New(slog.NewTextHandler(&logs, nil)))
			for range 3 {
				if v := d.View(context.Background()); v.State != render.PluginDirectoryOff {
					t.Fatalf("state %q", v.State)
				}
			}
			if logs.Len() != 0 {
				t.Errorf("registry off logged: %s", logs.String())
			}
		}
	})
}

func TestRegistryDirectoryStaleWhileErrorBoundedByExpiry(t *testing.T) {
	r := newDirRig(t)
	ctx := context.Background()
	r.publish(10*time.Minute, packages.Entry{Kind: packages.KindPlugin, Name: "one", Version: "1"})
	if v := r.dir.View(ctx); v.State != render.PluginDirectoryOK {
		t.Fatalf("first: %q", v.State)
	}
	r.fail()
	r.advance(directoryTTL + time.Second) // refresh fails, index still valid
	if v := r.dir.View(ctx); v.State != render.PluginDirectoryOK || len(v.Cards) != 1 {
		t.Fatalf("stale-while-error: %+v", v)
	}
	r.advance(10 * time.Minute) // past the index's own expires_at
	if v := r.dir.View(ctx); v.State != render.PluginDirectoryUnavailable {
		t.Fatalf("after expiry: %q", v.State)
	}
}

func TestRegistryDirectoryNeverServesPastIndexExpiry(t *testing.T) {
	r := newDirRig(t)
	ctx := context.Background()
	r.publish(time.Minute, packages.Entry{Kind: packages.KindPlugin, Name: "one", Version: "1"})
	r.dir.View(ctx)
	r.fail()
	r.advance(61 * time.Second) // well inside the 3 min TTL but past expires_at
	if v := r.dir.View(ctx); v.State != render.PluginDirectoryUnavailable {
		t.Fatalf("served an expired index: %+v", v)
	}
}

func TestRegistryDirectoryLogsStateChangesOnce(t *testing.T) {
	r := newDirRig(t)
	ctx := context.Background()
	r.dir.View(ctx) // first ok: silent
	r.fail()
	for range 3 {
		r.advance(directoryTTL + time.Second)
		r.dir.View(ctx)
	}
	if n := strings.Count(r.logs.String(), "registry unavailable"); n != 1 {
		t.Fatalf("error logged %d times:\n%s", n, r.logs.String())
	}
	r.publish(24 * time.Hour)
	r.advance(directoryTTL + time.Second)
	r.dir.View(ctx)
	r.advance(directoryTTL + time.Second)
	r.dir.View(ctx)
	if n := strings.Count(r.logs.String(), "available again"); n != 1 {
		t.Fatalf("recovery logged %d times:\n%s", n, r.logs.String())
	}
}

func TestRegistryDirectorySingleFlight(t *testing.T) {
	r := newDirRig(t)
	rel := make(chan struct{})
	r.mu.Lock()
	r.release = rel
	r.mu.Unlock()
	const n = 12
	var wg sync.WaitGroup
	states := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states[i] = r.dir.View(context.Background()).State
		}()
	}
	time.Sleep(100 * time.Millisecond) // let every caller reach the lock
	close(rel)
	wg.Wait()
	if got := r.hits.Load(); got != 1 {
		t.Fatalf("registry fetched %d times for %d concurrent renders", got, n)
	}
	for i, s := range states {
		if s != render.PluginDirectoryOK {
			t.Errorf("caller %d state %q", i, s)
		}
	}
}

// A panic in the fetch must still release the in-flight channel and count as a
// failure, so later renders neither hang nor panic.
func TestRegistryDirectoryPanickingFetchRecordsFailure(t *testing.T) {
	var calls atomic.Int32
	panicking := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		panic("boom")
	})}
	reg := &packages.Registry{URL: "https://registry.example/registry.json", Client: panicking}
	var logs bytes.Buffer
	now := time.Now()
	d := newRegistryDirectory(func() *packages.Registry { return reg }, slog.New(slog.NewTextHandler(&logs, nil)))
	d.now = func() time.Time { return now }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		if v := d.View(ctx); v.State != render.PluginDirectoryUnavailable {
			t.Fatalf("state %q", v.State)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("fetches = %d, want 1 (failure remembered)", n)
	}
	if d.inflight != nil {
		t.Error("in-flight channel left open")
	}
	if !strings.Contains(logs.String(), "panicked") {
		t.Errorf("panic not logged: %s", logs.String())
	}
	now = now.Add(directoryFailureTTL + time.Second)
	d.View(ctx)
	if n := calls.Load(); n != 2 {
		t.Errorf("fetches after failure memory = %d, want 2", n)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A registry that never answers is cut at the fetch timeout and the page gets
// the unavailable state instead of hanging.
func TestRegistryDirectoryFetchTimeout(t *testing.T) {
	r := newDirRig(t)
	r.release = make(chan struct{})
	t.Cleanup(func() { close(r.release) })
	r.dir.fetchTimeout = 100 * time.Millisecond

	start := time.Now()
	v := r.dir.View(context.Background())
	if v.State != render.PluginDirectoryUnavailable {
		t.Fatalf("state %q", v.State)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("View took %v, want about the 100ms timeout", took)
	}
}

// A failed fetch is remembered for directoryFailureTTL: a dead registry is not
// refetched by every render, but is retried once the memory runs out.
func TestRegistryDirectoryFailureMemory(t *testing.T) {
	r := newDirRig(t)
	r.fail()
	ctx := context.Background()
	r.dir.View(ctx)
	r.advance(directoryFailureTTL - time.Second)
	r.dir.View(ctx)
	if n := r.hits.Load(); n != 1 {
		t.Fatalf("hits within the failure memory = %d, want 1", n)
	}
	r.advance(2 * time.Second)
	r.dir.View(ctx)
	if n := r.hits.Load(); n != 2 {
		t.Errorf("hits after the failure memory = %d, want 2", n)
	}
}

func TestRegistryDirectoryViewsSplitKindsFromOneFetch(t *testing.T) {
	r := newDirRig(t)
	r.publish(24*time.Hour,
		packages.Entry{Kind: packages.KindPlugin, Name: "zeta", Version: "1.0.0", Tier: packages.TierFirstParty},
		packages.Entry{Kind: packages.KindTheme, Name: "zen", Version: "1.0.0", Title: "Zen", Description: "Calm", Tier: packages.TierCommunity, Author: "Acme", Source: "https://example.org/zen", Screenshot: "screenshots/zen.webp"},
		packages.Entry{Kind: packages.KindTheme, Name: "aqua", Version: "v2.0.0", Tier: packages.TierFirstParty, Author: "hidden", Source: "http://example.org/aqua"},
	)
	vs := r.dir.Views(context.Background())
	if len(vs.plugins.Cards) != 1 || vs.plugins.Cards[0].Name != "zeta" {
		t.Fatalf("plugins = %+v", vs.plugins)
	}
	if vs.themes.State != render.PluginDirectoryOK || len(vs.themes.Cards) != 2 {
		t.Fatalf("themes = %+v", vs.themes)
	}
	a, z := vs.themes.Cards[0], vs.themes.Cards[1]
	if a.Name != "aqua" || a.Version != "v2.0.0" || a.TierLabel != "First-party" || a.Author != "" || a.Source != "" || a.Screenshot != "" {
		t.Errorf("aqua = %+v", a)
	}
	if z.TierLabel != "Community" || z.Author != "Acme" || z.Source != "https://example.org/zen" || z.Search != "zen zen calm acme" {
		t.Errorf("zen = %+v", z)
	}
	// The rig serves over http, so the card has no screenshot; an https registry resolves it.
	if z.Screenshot != "" {
		t.Errorf("http registry screenshot %q, want none", z.Screenshot)
	}
	idx := &packages.Index{Packages: []packages.Entry{{Kind: packages.KindTheme, Name: "zen", Screenshot: "screenshots/zen.webp"}}}
	if got := directoriesFor(idx, "https://hatamoto.dev/registry.json").themes.Cards[0].Screenshot; got != "https://hatamoto.dev/screenshots/zen.webp" {
		t.Errorf("https screenshot %q", got)
	}
	if n := r.hits.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

func TestRegistryDirectoryThemeStates(t *testing.T) {
	r := newDirRig(t)
	r.publish(time.Hour)
	if v := r.dir.Views(context.Background()).themes; v.State != render.PluginDirectoryEmpty {
		t.Fatalf("empty: %q", v.State)
	}
	r.fail()
	r.advance(2 * time.Hour) // past the last good index's expiry
	if v := r.dir.Views(context.Background()).themes; v.State != render.PluginDirectoryUnavailable {
		t.Fatalf("unavailable: %q", v.State)
	}
	off := newRegistryDirectory(func() *packages.Registry { return nil }, nil)
	if v := off.Views(context.Background()); v.themes.State != render.PluginDirectoryOff || v.plugins.State != render.PluginDirectoryOff {
		t.Fatalf("off: %+v", v)
	}
}

func TestScreenshotURL(t *testing.T) {
	for _, c := range []struct{ base, rel, want string }{
		{"https://hatamoto.dev/registry.json", "screenshots/a.webp", "https://hatamoto.dev/screenshots/a.webp"},
		{"https://hatamoto.dev/registry/index.json", "screenshots/a.webp", "https://hatamoto.dev/registry/screenshots/a.webp"},
		{"http://hatamoto.dev/registry.json", "screenshots/a.webp", ""},
		{"https://hatamoto.dev/registry.json", "", ""},
		{"https://hatamoto.dev/registry.json", "//evil.example/a.png", "https://hatamoto.dev//evil.example/a.png"},
		{"https://hatamoto.dev/registry.json", "https://evil.example/a.png", "https://hatamoto.dev/https://evil.example/a.png"},
		{"https://hatamoto.dev/registry.json", "../../a.png", "https://hatamoto.dev/a.png"},
		{"%zz", "a.png", ""},
		{"", "a.png", ""},
	} {
		if got := screenshotURL(c.base, c.rel); got != c.want {
			t.Errorf("screenshotURL(%q, %q) = %q, want %q", c.base, c.rel, got, c.want)
		}
	}
}

func TestRegistryDirectoryPermissionCard(t *testing.T) {
	r := newDirRig(t)
	r.publish(24*time.Hour,
		packages.Entry{Kind: packages.KindPlugin, Name: "tags", Version: "1.0.0", Permissions: []string{"settings", "site.scripts", "http.fetch.any", "future.perm"}},
		packages.Entry{Kind: packages.KindPlugin, Name: "plain", Version: "1.0.0"},
	)
	v := r.dir.View(context.Background())
	if len(v.Cards) != 2 {
		t.Fatalf("cards = %+v", v.Cards)
	}
	plain, tags := v.Cards[0], v.Cards[1]
	if plain.PermCount != 0 || len(plain.PermWarn) != 0 || len(plain.PermLines) != 0 {
		t.Errorf("plain = %+v", plain)
	}
	if tags.PermCount != 4 || len(tags.PermLines) != 4 {
		t.Fatalf("tags = %+v", tags)
	}
	if got := strings.Join(tags.Permissions, ","); got != "settings,site.scripts,http.fetch.any,future.perm" {
		t.Errorf("Permissions (kept for theme overrides) = %q", got)
	}
	if got := strings.Join(tags.PermWarn, "|"); got != "JavaScript on every public page.|Any web address." {
		t.Errorf("warn badges = %q", got)
	}
	if l := tags.PermLines[1]; !l.Warn || l.Name != "site.scripts" {
		t.Errorf("line = %+v", l)
	}
	if l := tags.PermLines[2]; !l.Warn || strings.HasPrefix(l.Text, "WARNING:") || !strings.HasPrefix(l.Text, "send requests") {
		t.Errorf("WARNING prefix not stripped: %+v", l)
	}
	if l := tags.PermLines[3]; l.Text != "future.perm" || l.Warn {
		t.Errorf("unknown permission line = %+v", l)
	}
}

// TestRegistryDirectoryPermLineConversion pins permtext.Line to render.PermLine's field order.
func TestRegistryDirectoryPermLineConversion(t *testing.T) {
	got := render.PermLine(permtext.Line{Name: "a", Text: "b", Warn: true})
	if want := (render.PermLine{Name: "a", Text: "b", Warn: true}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
