package admin

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hatamoto.dev/core/internal/form"
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
		if got := it.Badge(roleViewer("owner")); got != "3" {
			t.Errorf("owner badge %q, want 3", got)
		}
		if got := it.Badge(roleViewer("editor")); got != "" {
			t.Errorf("editor badge %q, want none", got)
		}
	}
}

// The Entities marker is the draft count, worded for the eye and for screen readers.
func TestSidebarShowsDraftCount(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	want := `<span>Content</span><span class="hm-nav-link__badge" title="1 draft to review"><span aria-hidden="true">1</span><span class="hm-visually-hidden">1 draft to review</span></span>`
	body := c.get(t, "owner-token", "/admin/entities", "")
	if !strings.Contains(body, want) {
		t.Errorf("the Content item lacks its labelled draft count of 1")
	}
	if strings.Contains(body, `<span>Analytics</span><span class="hm-nav-link__badge"`) {
		t.Error("Analytics must not carry a marker")
	}

	// The marker and the list's own Draft segment come from the same page and must agree.
	nav := regexp.MustCompile(`<span>Content</span><span class="hm-nav-link__badge"[^>]*><span aria-hidden="true">(\d+)</span>`).FindStringSubmatch(body)
	seg := regexp.MustCompile(`id="entities-seg-draft-n">(\d+)</span>`).FindStringSubmatch(body)
	if nav == nil || seg == nil {
		t.Fatalf("nav marker %v, Draft segment %v: both must render", nav, seg)
	}
	if nav[1] != seg[1] {
		t.Errorf("nav drafts %s, list Draft count %s", nav[1], seg[1])
	}
}

// No drafts, no marker.
func TestSidebarHidesMarkerWithoutDrafts(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	delete(c.ents.ents, fid(12))
	body := c.get(t, "owner-token", "/admin/entities", "")
	if strings.Contains(body, `<span>Content</span><span class="hm-nav-link__badge"`) {
		t.Error("the Content item shows a marker at 0 drafts")
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
	found := false
	for _, it := range a.nav.all() {
		if it.ID == pluginsID {
			found = true
			if got := it.BadgeLabel(roleViewer("owner"), "3"); got != "3 plugins installed" {
				t.Errorf("label %q", got)
			}
		}
	}
	if !found {
		t.Fatal("no Plugins nav item")
	}
}

// The Forms marker counts new submissions only (spam excluded), is worded for
// screen readers, hidden at zero, and the same for every role.
func TestFormsMarkerCountsNewSubmissions(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	fm := ff.addForm("Contact")
	other := ff.addForm("Quote")
	now := time.Now()
	for range 2 {
		ff.addSub(fm, form.SubmissionStatusNew, now, nil)
	}
	ff.addSub(other, form.SubmissionStatusNew, now, nil)
	ff.addSub(other, form.SubmissionStatusSpam, now, nil)

	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Growth.Forms = ff
	a.wireNavCounts()
	var item NavItem
	for _, it := range a.nav.all() {
		if it.ID == "forms" {
			item = it
		}
	}
	for _, role := range []string{"owner", "editor"} {
		if got := item.Badge(roleViewer(role)); got != "3" {
			t.Errorf("%s: badge %q, want 3", role, got)
		}
	}
	if got := item.BadgeLabel(roleViewer("owner"), "3"); got != "3 new submissions" {
		t.Errorf("label %q", got)
	}
	if got := item.BadgeLabel(roleViewer("owner"), "1"); got != "1 new submission" {
		t.Errorf("label %q", got)
	}
}

func TestNewSubmissionsZeroAndNilStore(t *testing.T) {
	t.Parallel()

	a := &Admin{}
	if n, err := a.newSubmissions(); n != 0 || err != nil {
		t.Errorf("nil store: %d, %v", n, err)
	}
	ff := newFakeForms()
	ff.addSub(ff.addForm("Contact"), form.SubmissionStatusSpam, time.Now(), nil)
	a.deps.Growth.Forms = ff
	if n, err := a.newSubmissions(); n != 0 || err != nil {
		t.Errorf("spam only: %d, %v", n, err)
	}
	if got := countBadge(time.Minute, time.Now, syncRun, a.newSubmissions)(); got != "" {
		t.Errorf("zero shows %q, want no badge", got)
	}
}

func commentsNavItem(t *testing.T, svc CommentsService) NavItem {
	t.Helper()
	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Comments = svc
	a.wireNavCounts()
	for _, it := range a.nav.all() {
		if it.ID == "comments" {
			return it
		}
	}
	t.Fatal("no comments nav item")
	return NavItem{}
}

func TestCommentsBadge(t *testing.T) {
	t.Parallel()

	it := commentsNavItem(t, &fakeComments{counts: CommentCounts{Pending: 3}})
	if !it.Visible(roleViewer("editor")) || it.Badge(roleViewer("editor")) != "3" {
		t.Errorf("pending 3: visible %v, badge %q", it.Visible(roleViewer("editor")), it.Badge(roleViewer("editor")))
	}
	if got := it.BadgeLabel(roleViewer("editor"), "3"); got != "3 comments to moderate" {
		t.Errorf("label %q", got)
	}
	if got := it.BadgeLabel(roleViewer("editor"), "1"); got != "1 comment to moderate" {
		t.Errorf("label %q", got)
	}

	if it := commentsNavItem(t, &fakeComments{}); it.Visible(roleViewer("owner")) {
		t.Error("off and empty: the item must be hidden")
	}
	it = commentsNavItem(t, &fakeComments{settings: CommentSettings{Enabled: true}})
	if !it.Visible(roleViewer("owner")) || it.Badge(roleViewer("owner")) != "" {
		t.Errorf("enabled, none pending: visible %v, badge %q", it.Visible(roleViewer("owner")), it.Badge(roleViewer("owner")))
	}
	it = commentsNavItem(t, &fakeComments{counts: CommentCounts{Trash: 2}})
	if !it.Visible(roleViewer("owner")) {
		t.Error("off with existing comments: the item must show")
	}
}

func TestCommentsNavHiddenWithoutService(t *testing.T) {
	t.Parallel()

	a := &Admin{nav: defaultNav("/admin")}
	a.wireNavCounts()
	for _, it := range a.nav.For(roleViewer("owner"), "/admin") {
		if it.Label == "Comments" {
			t.Error("Comments shows without a service")
		}
	}
}

// Turning comments on must show the item at once, not after the cached count expires.
func TestCommentsNavRefreshAfterSwitch(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{counts: CommentCounts{Pending: 3}}
	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Comments = svc
	a.wireNavCounts()
	item := func() NavItem {
		for _, it := range a.nav.all() {
			if it.ID == "comments" {
				return it
			}
		}
		t.Fatal("no comments nav item")
		return NavItem{}
	}
	svc.mu.Lock()
	svc.counts = CommentCounts{}
	svc.mu.Unlock()
	if item().Visible(roleViewer("owner")) {
		t.Fatal("off and empty: the item must be hidden")
	}
	svc.mu.Lock()
	svc.settings.Enabled = true
	svc.counts = CommentCounts{Pending: 3}
	svc.mu.Unlock()
	a.refreshCommentsNav()
	if it := item(); !it.Visible(roleViewer("owner")) || it.Badge(roleViewer("owner")) != "3" {
		t.Errorf("after the switch: visible %v, badge %q", it.Visible(roleViewer("owner")), it.Badge(roleViewer("owner")))
	}
}

func reviewNavItem(t *testing.T, rv ReviewFlow) (*Admin, NavItem) {
	t.Helper()
	a := &Admin{nav: defaultNav("/admin")}
	a.deps.Review = rv
	a.wireNavCounts()
	return a, a.reviewItem(t)
}

func (a *Admin) reviewItem(t *testing.T) NavItem {
	t.Helper()
	for _, it := range a.nav.all() {
		if it.ID == "review" {
			return it
		}
	}
	t.Fatal("no review nav item")
	return NavItem{}
}

func TestReviewNavItem(t *testing.T) {
	t.Parallel()

	roles := map[string]bool{"owner": true, "editor": true, "reviewer": true, "author": false, "contributor": false, "read-only": false}
	waiting := []PendingEdit{{ID: 1}, {ID: 2}}

	t.Run("off and empty: hidden for every role", func(t *testing.T) {
		_, it := reviewNavItem(t, &fakeReviewFlow{})
		for role := range roles {
			if it.Visible(roleViewer(role)) {
				t.Errorf("%s sees the item with review off and nothing waiting", role)
			}
		}
	})
	t.Run("on: visible to deciders only, with the count", func(t *testing.T) {
		_, it := reviewNavItem(t, &fakeReviewFlow{on: true, queue: waiting})
		for role, want := range roles {
			if got := it.Visible(roleViewer(role)); got != want {
				t.Errorf("%s: visible = %v, want %v", role, got, want)
			}
		}
		if got := it.Badge(roleViewer("editor")); got != "2" {
			t.Errorf("badge = %q, want 2", got)
		}
		if got := it.Badge(roleViewer("author")); got != "" {
			t.Errorf("an author sees the badge %q", got)
		}
		if got := it.BadgeLabel(roleViewer("editor"), "2"); got != "2 changes waiting for review" {
			t.Errorf("label %q", got)
		}
		if got := it.BadgeLabel(roleViewer("editor"), "1"); got != "1 change waiting for review" {
			t.Errorf("label %q", got)
		}
	})
	t.Run("off with a change still waiting: shown", func(t *testing.T) {
		_, it := reviewNavItem(t, &fakeReviewFlow{queue: waiting[:1]})
		if !it.Visible(roleViewer("owner")) {
			t.Error("a change waits but the item is hidden")
		}
	})
	t.Run("turning review on shows it at once", func(t *testing.T) {
		rv := &fakeReviewFlow{}
		a, it := reviewNavItem(t, rv)
		if it.Visible(roleViewer("owner")) {
			t.Fatal("hidden before the switch")
		}
		rv.on = true
		a.refreshReviewNav()
		if !a.reviewItem(t).Visible(roleViewer("owner")) {
			t.Error("still hidden after the switch")
		}
	})
	t.Run("no review flow: no item", func(t *testing.T) {
		a := &Admin{nav: defaultNav("/admin")}
		a.wireNavCounts()
		for _, it := range a.nav.For(roleViewer("owner"), "/admin") {
			if it.Label == "Review" {
				t.Error("Review shows without a flow")
			}
		}
	})
}
