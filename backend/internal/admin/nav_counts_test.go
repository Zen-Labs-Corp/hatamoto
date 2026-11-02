package admin

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func syncRun(f func()) { f() }

func init() { navRefreshRun = syncRun }

func TestCountBadge(t *testing.T) {
	t.Parallel()

	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	n, err := 158, error(nil)
	calls := 0
	badge := countBadge(30*time.Second, now, syncRun, func() (int, error) { calls++; return n, err })

	if got := badge(); got != "158" {
		t.Fatalf("badge %q, want 158", got)
	}
	n = 3
	clock = clock.Add(10 * time.Second)
	if got := badge(); got != "158" || calls != 1 {
		t.Errorf("within the ttl: badge %q after %d calls, want the cached 158 after 1", got, calls)
	}
	clock = clock.Add(31 * time.Second)
	if got := badge(); got != "3" || calls != 2 {
		t.Errorf("after the ttl: badge %q after %d calls, want 3 after 2", got, calls)
	}
	n = 0
	clock = clock.Add(time.Minute)
	if got := badge(); got != "" {
		t.Errorf("zero shows %q, want no badge", got)
	}
	n, err = 5, errors.New("down")
	clock = clock.Add(time.Minute)
	if got := badge(); got != "" {
		t.Errorf("an error shows %q, want no badge", got)
	}
	err = nil
	if got := badge(); got != "" || calls != 4 {
		t.Errorf("a failed count is retried only after the ttl: badge %q after %d calls", got, calls)
	}
}

// A render never waits on the count: the first call shows no badge while the
// background refresh is still running, an expired value is served stale, and
// only one refresh runs at a time.
func TestCountBadgeNeverBlocksOnCount(t *testing.T) {
	t.Parallel()

	clock := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	nowf := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	var wg sync.WaitGroup
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	badge := countBadge(30*time.Second, nowf, run, func() (int, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return int(calls.Load()) * 10, nil
	})

	done := make(chan string)
	go func() { done <- badge() }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("first render with no value showed %q, want no badge", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first render waited on the slow count")
	}
	<-started
	if got := badge(); got != "" || calls.Load() != 1 {
		t.Errorf("while a refresh runs: badge %q after %d counts, want none after 1", got, calls.Load())
	}
	release <- struct{}{}
	wg.Wait()
	if got := badge(); got != "10" {
		t.Fatalf("after the refresh: badge %q, want 10", got)
	}

	mu.Lock()
	clock = clock.Add(time.Minute)
	mu.Unlock()
	if got := badge(); got != "10" {
		t.Errorf("expired value: badge %q, want the stale 10 without waiting", got)
	}
	<-started
	release <- struct{}{}
	wg.Wait()
	if got := badge(); got != "20" {
		t.Errorf("after the background refresh: badge %q, want 20", got)
	}
}

type threePlugins struct{ PluginAdmin }

func (threePlugins) Plugins() []PluginInfo { return make([]PluginInfo, 3) }

// The Plugins count is the owner's; an editor admitted by a plugin UI sees none.
func TestPluginsCountIsOwnerOnly(t *testing.T) {
	t.Parallel()

	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Growth.Plugins = threePlugins{}
	a.wireNavCounts()
	for _, it := range a.nav.all() {
		if it.ID != pluginsID {
			continue
		}
		if got := it.Badge("owner"); got != "3" {
			t.Errorf("owner badge %q, want 3", got)
		}
		if got := it.Badge("editor"); got != "" {
			t.Errorf("editor badge %q, want none", got)
		}
	}
}

// The sidebar shows the Entities and Plugins counts, and only when there is something to count.
func TestSidebarShowsEntityAndPluginCounts(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	if !strings.Contains(body, `<span>Entities</span><span class="hm-nav-link__badge">4</span>`) {
		t.Errorf("the Entities item lacks its count of 4")
	}
	if strings.Contains(body, `<span>Analytics</span><span class="hm-nav-link__badge">`) {
		t.Error("Analytics must not carry a count")
	}
}
