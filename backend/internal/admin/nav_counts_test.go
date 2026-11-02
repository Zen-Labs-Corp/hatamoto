package admin

import (
	"context"
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
// A panicking count is recovered (a bare goroutine would otherwise end the process), shows no badge,
// and a later refresh after the ttl still runs.
func TestCountBadgePanicIsRecovered(t *testing.T) {
	t.Parallel()

	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	panics := true
	calls := 0
	badge := countBadge(30*time.Second, now, syncRun, func() (int, error) {
		calls++
		if panics {
			panic("boom")
		}
		return 7, nil
	})

	if got := badge(); got != "" || calls != 1 {
		t.Fatalf("after a panic: badge %q, %d calls, want no badge after 1", got, calls)
	}
	if badge(); calls != 1 {
		t.Errorf("a panic must stamp the time: %d calls within the ttl, want 1", calls)
	}
	panics = false
	clock = clock.Add(31 * time.Second)
	if got := badge(); got != "7" || calls != 2 {
		t.Errorf("after the ttl: badge %q after %d calls, want 7 after 2 (inflight must be cleared)", got, calls)
	}
}

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

// The Entities marker is the draft count, worded for the eye and for screen readers.
func TestSidebarShowsDraftCount(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	want := `<span>Entities</span><span class="hm-nav-link__badge" title="1 draft to review"><span aria-hidden="true">1</span><span class="hm-visually-hidden">1 draft to review</span></span>`
	body := c.get(t, "owner-token", "/admin/entities", "")
	if !strings.Contains(body, want) {
		t.Errorf("the Entities item lacks its labelled draft count of 1")
	}
	if strings.Contains(body, `<span>Analytics</span><span class="hm-nav-link__badge"`) {
		t.Error("Analytics must not carry a marker")
	}

	schemas, err := c.ents.ListSchemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, list := c.a.entityCounts(context.Background(), schemas, nil).byStatus["draft"], mustDrafts(t, c.a); got != list {
		t.Errorf("nav drafts %d, list Draft count %d", list, got)
	}
}

func mustDrafts(t *testing.T, a *Admin) int {
	t.Helper()
	n, err := a.entityDrafts()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// No drafts, no marker.
func TestSidebarHidesMarkerWithoutDrafts(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	delete(c.ents.ents, fid(12))
	body := c.get(t, "owner-token", "/admin/entities", "")
	if strings.Contains(body, `<span>Entities</span><span class="hm-nav-link__badge"`) {
		t.Error("the Entities item shows a marker at 0 drafts")
	}
}

func TestCountLabel(t *testing.T) {
	t.Parallel()

	if got := countLabel("1", "draft to review", "drafts to review"); got != "1 draft to review" {
		t.Errorf("singular: %q", got)
	}
	if got := countLabel("3", "draft to review", "drafts to review"); got != "3 drafts to review" {
		t.Errorf("plural: %q", got)
	}
}

// The Plugins marker says what it counts.
func TestPluginsMarkerIsLabelled(t *testing.T) {
	t.Parallel()

	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Growth.Plugins = threePlugins{}
	a.wireNavCounts()
	for _, it := range a.nav.all() {
		if it.ID == pluginsID {
			if got := it.BadgeLabel("owner", "3"); got != "3 plugins installed" {
				t.Errorf("label %q", got)
			}
		}
	}
}
