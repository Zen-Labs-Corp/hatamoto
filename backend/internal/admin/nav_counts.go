package admin

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/form"
)

// navCountTTL is how long a sidebar count is reused: NavItem.Badge runs on
// every render, and a count that is a few seconds old is fine.
const navCountTTL = 30 * time.Second

// navRefreshRun starts a background count refresh. Tests swap it for a
// synchronous runner so a first render already shows the count.
var navRefreshRun = func(f func()) { go f() }

// countBadge returns a NavItem.Badge showing count's value. A render never
// waits on count: it gets the cached value, possibly stale, and once the ttl
// has passed one background refresh (guarded against overlap) replaces it. The
// first render before any value exists shows no badge. Zero and errors give ""
// (no badge), and a failed count is not retried before the ttl passes, so a
// down database is not hit per render.
func countBadge(ttl time.Duration, now func() time.Time, run func(func()), count func() (int, error)) func() string {
	var (
		mu       sync.Mutex
		at       time.Time
		text     string
		inflight bool
	)
	refresh := func() {
		text2 := ""
		// The refresh runs in a bare goroutine, where a panic would end the process: log it and
		// treat it as a failed count (no badge, retried after the ttl).
		defer func() {
			if r := recover(); r != nil {
				slog.Error("admin: nav count panicked", "panic", r)
				mu.Lock()
				at, text, inflight = now(), "", false
				mu.Unlock()
			}
		}()
		if n, err := count(); err == nil && n > 0 {
			text2 = strconv.Itoa(n)
		}
		mu.Lock()
		at, text, inflight = now(), text2, false
		mu.Unlock()
	}
	return func() string {
		mu.Lock()
		start := (at.IsZero() || now().Sub(at) >= ttl) && !inflight
		if start {
			inflight = true
		}
		mu.Unlock()
		if start {
			run(refresh)
		}
		mu.Lock()
		defer mu.Unlock()
		return text
	}
}

// countLabel words a count: "1 draft to review", "3 drafts to review".
func countLabel(n, one, many string) string {
	if n == "1" {
		return "1 " + one
	}
	return n + " " + many
}

// wireNavCounts gives the sidebar's Entities, Forms, Comments and Plugins items their markers:
// the drafts waiting for review, the new form submissions, the comments waiting
// for moderation and the installed plugins. The Plugins count is the
// owner's: an editor admitted by a plugin UI does not see the installed total.
func (a *Admin) wireNavCounts() {
	entities := countBadge(navCountTTL, time.Now, navRefreshRun, a.entityDrafts)
	plugins := countBadge(navCountTTL, time.Now, navRefreshRun, a.pluginTotal)
	a.nav.update("entities", func(it *NavItem) {
		it.Badge = func(viewer) string { return entities() }
		it.BadgeLabel = func(_ viewer, b string) string { return countLabel(b, "draft to review", "drafts to review") }
	})
	forms := countBadge(navCountTTL, time.Now, navRefreshRun, a.newSubmissions)
	a.nav.update("forms", func(it *NavItem) {
		it.Badge = func(viewer) string { return forms() }
		it.BadgeLabel = func(_ viewer, b string) string { return countLabel(b, "new submission", "new submissions") }
	})
	if a.deps.Comments != nil {
		a.wireCommentsNav()
	}
	if a.deps.Review != nil {
		a.wireReviewNav()
	}
	a.nav.update(pluginsID, func(it *NavItem) {
		it.Badge = func(v viewer) string {
			if !v.Can(authz.ActionPluginManage) {
				return ""
			}
			return plugins()
		}
		it.BadgeLabel = func(_ viewer, b string) string { return countLabel(b, "plugin installed", "plugins installed") }
	})
}

// entityDrafts counts the drafts the Entities list shows (reuse schemas
// excluded), as the list's own Draft count does.
func (a *Admin) entityDrafts() (int, error) {
	c := a.deps.Content
	if c.Entities == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	schemas, err := c.Entities.ListSchemas(ctx)
	if err != nil {
		return 0, err
	}
	return a.entityCounts(ctx, schemas, nil).byStatus["draft"], nil
}

// newSubmissions counts the submissions still marked new across all forms
// (spam is a separate status and not counted), as the Forms list's own New
// column does. Every role that sees Forms sees the count.
func (a *Admin) newSubmissions() (int, error) {
	fs := a.deps.Growth.Forms
	if fs == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	forms, err := fs.ListForms(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range forms {
		_, total, err := fs.ListSubmissions(ctx, f.ID, &form.ListSubmissionsParams{Status: form.SubmissionStatusNew, Limit: 1})
		if err != nil {
			return 0, err
		}
		n += total
	}
	return n, nil
}

// pluginTotal counts the installed plugins.
func (a *Admin) pluginTotal() (int, error) {
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return 0, nil
	}
	return len(pa.Plugins()), nil
}

// wireCommentsNav gives the Comments item its visibility and badge. Both are
// cached counts, so turning comments on or off would leave the item stale for
// a whole ttl: refreshCommentsNav starts them over and fills them before it
// returns.
func (a *Admin) wireCommentsNav() {
	var (
		mu      sync.Mutex
		pending func() string
		shown   func() string
	)
	build := func(prime bool) {
		var inline atomic.Bool
		inline.Store(prime)
		run := func(f func()) {
			if inline.Load() {
				f()
				return
			}
			navRefreshRun(f)
		}
		p := countBadge(navCountTTL, time.Now, run, a.pendingComments)
		sh := countBadge(navCountTTL, time.Now, run, a.commentsInUse)
		if prime {
			p()
			sh()
			inline.Store(false)
		}
		mu.Lock()
		pending, shown = p, sh
		mu.Unlock()
	}
	build(false)
	a.refreshCommentsNav = func() { build(true) }
	get := func() (func() string, func() string) {
		mu.Lock()
		defer mu.Unlock()
		return pending, shown
	}
	a.nav.update("comments", func(it *NavItem) {
		it.Visible = func(viewer) bool { _, sh := get(); return sh() != "" }
		it.Badge = func(viewer) string { p, _ := get(); return p() }
		it.BadgeLabel = func(_ viewer, b string) string { return countLabel(b, "comment to moderate", "comments to moderate") }
	})
}

// pendingComments counts the comments waiting for moderation, site-wide.
func (a *Admin) pendingComments() (int, error) {
	svc := a.deps.Comments
	if svc == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := svc.Counts(ctx)
	return c.Pending, err
}

// commentsInUse is 1 when the Comments sidebar item shows: comments are on
// for the site or any comment exists.
func (a *Admin) commentsInUse() (int, error) {
	svc := a.deps.Comments
	if svc == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := svc.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if s.Enabled {
		return 1, nil
	}
	c, err := svc.Counts(ctx)
	if err != nil {
		return 0, err
	}
	if c.Pending+c.Approved+c.Spam+c.Trash > 0 {
		return 1, nil
	}
	return 0, nil
}

// wireReviewNav gives the Review item its visibility and badge. It shows for people who may
// decide, once the review switch is on or a change is waiting, and counts the changes in the
// queue. Both are cached values, so turning review on or off starts them over:
// refreshReviewNav fills them before it returns.
func (a *Admin) wireReviewNav() {
	var (
		mu      sync.Mutex
		pending func() string
		shown   func() string
	)
	build := func(prime bool) {
		var inline atomic.Bool
		inline.Store(prime)
		run := func(f func()) {
			if inline.Load() {
				f()
				return
			}
			navRefreshRun(f)
		}
		p := countBadge(navCountTTL, time.Now, run, a.pendingReviews)
		sh := countBadge(navCountTTL, time.Now, run, a.reviewInUse)
		if prime {
			p()
			sh()
			inline.Store(false)
		}
		mu.Lock()
		pending, shown = p, sh
		mu.Unlock()
	}
	build(false)
	a.refreshReviewNav = func() { build(true) }
	get := func() (func() string, func() string) {
		mu.Lock()
		defer mu.Unlock()
		return pending, shown
	}
	a.nav.update("review", func(it *NavItem) {
		it.Visible = func(v viewer) bool {
			_, sh := get()
			return sh() != "" && v.Can(authz.ActionEntityReviewDecide)
		}
		it.Badge = func(v viewer) string {
			if !v.Can(authz.ActionEntityReviewDecide) {
				return ""
			}
			p, _ := get()
			return p()
		}
		it.BadgeLabel = func(_ viewer, b string) string {
			return countLabel(b, "change waiting for review", "changes waiting for review")
		}
	})
}

// pendingReviews counts the changes in the review queue.
func (a *Admin) pendingReviews() (int, error) {
	rv := a.deps.Review
	if rv == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return rv.Count(ctx)
}

// reviewInUse is 1 when the Review sidebar item shows: the switch is on or a change waits.
func (a *Admin) reviewInUse() (int, error) {
	rv := a.deps.Review
	if rv == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if rv.Enabled(ctx) {
		return 1, nil
	}
	n, err := rv.Count(ctx)
	if n > 0 {
		n = 1
	}
	return n, err
}
