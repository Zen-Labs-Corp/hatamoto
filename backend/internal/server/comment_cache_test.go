package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/render"
)

const pendingText = "PENDING-ITEM-TEXT"

// threadEngine renders the comment form placeholders plus whatever thread the
// view carries, so a test can tell a visitor-specific render from a shared one.
type threadEngine struct{ pageRenderer }

func (threadEngine) RenderPage(w io.Writer, v *render.PageView) error {
	if _, err := io.WriteString(w, `<html><body><form method="post" action="/-/comments/`+v.EntityID+`" data-hm-cform>`+
		`<input type="hidden" name="hm_ct" value="__hmcf_tok__"><textarea name="__hmcf_body__"></textarea>`+
		`<input name="__hmcf_name__"><input name="__hmcf_email__"><input name="__hmcf_hp__"></form>`); err != nil {
		return err
	}
	if t := v.Comments; t != nil {
		for _, it := range t.Items {
			if _, err := io.WriteString(w, "<p>"+string(it.Body)+"</p>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "<p>notice="+t.Notice+" error="+t.Error+"</p>"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "</body></html>")
	return err
}

// cacheRig is a bypass fixture with two entities (paths /a and /b), per-path
// resolve counts and an optional hold inside the resolver.
type cacheRig struct {
	*bypassFixture
	ids map[string]uuid.UUID
	mu  sync.Mutex
	per map[string]int
	// hold runs inside the resolver, before it returns; nil = none.
	hold func(path string)
}

func newCacheRig(t *testing.T) *cacheRig {
	t.Helper()
	r := &cacheRig{
		bypassFixture: newBypassFixture(t),
		ids:           map[string]uuid.UUID{"/a": uuid.New(), "/b": uuid.New()},
		per:           map[string]int{},
	}
	build := r.h.buildView
	r.h.engine = threadEngine{pageRenderer: r.h.engine}
	r.h.resolve = func(_ context.Context, path string, _ *http.Request) (*routeResult, pageOutcome, error) {
		r.mu.Lock()
		r.per[path]++
		r.mu.Unlock()
		r.renderFixture.mu.Lock()
		r.renderFixture.resolves++
		r.renderFixture.mu.Unlock()
		if r.hold != nil {
			r.hold(path)
		}
		p := pageResponse("Page " + path)
		p.Entity.ID = r.ids[path]
		return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
	r.h.buildView = func(ctx context.Context, req *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, req, rr)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		if c, cerr := req.Cookie("hm_c"); cerr == nil && c.Value != "" && r.own {
			v.Comments = &render.CommentThread{Items: []render.CommentItem{{ID: "p1", Body: pendingText}}}
		}
		if q.Get("cnotice") != "" || q.Get("cerror") != "" {
			v.Comments = &render.CommentThread{Notice: q.Get("cnotice"), Error: q.Get("cerror")}
		}
		return v, nil
	}
	return r
}

func (r *cacheRig) resolvesOf(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.per[path]
}

func (r *cacheRig) pageKey(path string) string {
	return cache.RenderedPageKey(r.h.engine.Version(), path)
}

// dropEntity is the unit stand-in for CacheInvalidator.InvalidateRenderedEntity:
// it deletes the keys tagged with the entity (the real one is covered by the
// integration test).
func (r *cacheRig) dropEntity(_ context.Context, id uuid.UUID) {
	m := r.cache
	m.mu.Lock()
	defer m.mu.Unlock()
	tag := cache.EntityTag(id)
	for k, tags := range m.tags {
		for _, tg := range tags {
			if tg == tag {
				delete(m.data, k)
				delete(m.tags, k)
			}
		}
	}
}

func (r *cacheRig) approve(path string) {
	o := commentCacheObserver{drop: r.dropEntity}
	o.OnApproved(context.Background(), comment.Comment{EntityID: r.ids[path], Status: comment.StatusApproved})
}

func (r *cacheRig) cached(path string) bool {
	r.cache.mu.Lock()
	defer r.cache.mu.Unlock()
	_, ok := r.cache.data[r.pageKey(path)]
	return ok
}

func TestCommentApproveInvalidatesOnlyEntity(t *testing.T) {
	r := newCacheRig(t)
	r.get("/a")
	r.get("/b")
	if !r.cached("/a") || !r.cached("/b") {
		t.Fatalf("pages not primed: %v", r.cache.sets)
	}
	if tags := r.cache.tags[r.pageKey("/a")]; len(tags) != 2 {
		t.Fatalf("page tags = %v", tags)
	}
	r.approve("/a")
	if r.cached("/a") || !r.cached("/b") {
		t.Fatal("approve must drop entity A only")
	}
	r.get("/a")
	r.get("/b")
	if r.resolvesOf("/a") != 2 || r.resolvesOf("/b") != 1 {
		t.Fatalf("resolves a=%d b=%d, want 2 and 1", r.resolvesOf("/a"), r.resolvesOf("/b"))
	}
}

func TestCommentInflightOtherPageStillStored(t *testing.T) {
	r := newCacheRig(t)
	r.get("/a")
	started, release := make(chan struct{}), make(chan struct{})
	r.hold = func(path string) {
		if path == "/b" {
			close(started)
			<-release
		}
	}
	done := make(chan int)
	go func() { done <- r.get("/b").Code }()
	<-started
	r.approve("/a") // lands while B renders
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !r.cached("/b") {
		t.Fatal("an approval on another entity rejected B's store")
	}
	if r.cached("/a") {
		t.Fatal("A was not dropped")
	}
}

func TestCommentConcurrentMissRendersOnce(t *testing.T) {
	const n = 50
	r := newCacheRig(t)
	key := r.pageKey("/a")
	var first atomic.Bool
	r.hold = func(path string) {
		if first.CompareAndSwap(false, true) {
			flightWaitFor(t, "followers to park", func() bool { return r.h.flight.waiting(key) == n-1 })
		}
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := r.get("/a"); w.Code != http.StatusOK {
				t.Errorf("status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if got := r.resolvesOf("/a"); got != 1 {
		t.Fatalf("resolves = %d, want 1", got)
	}
	if len(r.cache.sets) != 1 {
		t.Fatalf("stores = %v", r.cache.sets)
	}

	// Requests that bypass the cache never join or lead a flight and store nothing.
	r.own = true
	const m = 5
	for range m {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.get("/a", r.cookie(r.key))
		}()
	}
	wg.Wait()
	if got := r.resolvesOf("/a"); got != 1+m {
		t.Fatalf("resolves = %d, want %d (each bypass request renders alone)", got, 1+m)
	}
	if len(r.cache.sets) != 1 || r.h.flight.waiting(key) != 0 {
		t.Fatalf("bypass request stored or joined a flight: sets %v", r.cache.sets)
	}
}

func TestCommentPrivateViewsNeverShared(t *testing.T) {
	r := newCacheRig(t)
	anon := r.get("/a")
	if strings.Contains(anon.Body.String(), pendingText) {
		t.Fatal("anonymous page shows a pending item")
	}
	sets, resolves := len(r.cache.sets), r.resolvesOf("/a")

	r.own = true
	views := map[string]struct {
		path string
		mut  []func(*http.Request)
	}{
		"own":    {"/a", []func(*http.Request){r.cookie(r.key)}},
		"notice": {"/a?cnotice=check_email", nil},
		"error":  {"/a?cerror=body_empty", nil},
	}
	for name, v := range views {
		w := r.get(v.path, v.mut...)
		cc := w.Header().Get("Cache-Control")
		if !strings.Contains(cc, "private") || !strings.Contains(cc, "no-store") {
			t.Errorf("%s: Cache-Control = %q", name, cc)
		}
		if w.Header().Get("ETag") != "" || w.Code != http.StatusOK {
			t.Errorf("%s: status %d etag %q", name, w.Code, w.Header().Get("ETag"))
		}
		if name == "own" && !strings.Contains(w.Body.String(), pendingText) {
			t.Error("own view lost the pending item")
		}
		if name == "notice" && !strings.Contains(w.Body.String(), "notice=check_email") {
			t.Error("notice view lost the notice")
		}
	}
	if len(r.cache.sets) != sets {
		t.Fatalf("a private view was stored: %v", r.cache.sets)
	}

	r.own = false
	next := r.get("/a")
	if r.resolvesOf("/a") != resolves+3 {
		t.Fatalf("anonymous request after the private views re-rendered (resolves %d)", r.resolvesOf("/a"))
	}
	if strings.Contains(next.Body.String(), pendingText) || strings.Contains(next.Body.String(), "notice=") {
		t.Fatal("the cached page carries a private view")
	}
}

func TestCommentForgedCookieGetsCachedPage(t *testing.T) {
	r := newCacheRig(t)
	r.own = true
	r.get("/a")
	before := r.resolvesOf("/a")
	for name, mut := range map[string]func(*http.Request){
		"garbage":   func(q *http.Request) { q.AddCookie(&http.Cookie{Name: "hm_c", Value: "forged"}) },
		"wrong key": r.cookie([]byte("another key")),
	} {
		w := r.get("/a", mut)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), pendingText) {
			t.Fatalf("%s: status %d, shows a private item", name, w.Code)
		}
	}
	if r.resolvesOf("/a") != before || r.hasOwn.Load() != 0 {
		t.Fatalf("forged cookie reached the resolver or the lookup (resolves %d, lookups %d)", r.resolvesOf("/a"), r.hasOwn.Load())
	}
}

func TestCommentFlightFollowerGetsOwnToken(t *testing.T) {
	r := newCacheRig(t)
	key := r.pageKey("/a")
	var first atomic.Bool
	r.hold = func(string) {
		if first.CompareAndSwap(false, true) {
			flightWaitFor(t, "the follower to park", func() bool { return r.h.flight.waiting(key) == 1 })
		}
	}
	bodies := make([]string, 2)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := r.get("/a")
			if w.Code != http.StatusOK {
				t.Errorf("status %d", w.Code)
			}
			bodies[i] = w.Body.String()
		}()
	}
	wg.Wait()
	if r.resolvesOf("/a") != 1 {
		t.Fatalf("resolves = %d, want 1", r.resolvesOf("/a"))
	}
	ta, tb := slotTokRe.FindStringSubmatch(bodies[0]), slotTokRe.FindStringSubmatch(bodies[1])
	if ta == nil || tb == nil || ta[1] == tb[1] || strings.HasPrefix(ta[1], "__hmcf") {
		t.Fatalf("tokens %v %v: want two different filled tokens", ta, tb)
	}
	stored := string(r.cache.data[key])
	if !strings.Contains(stored, "__hmcf_tok__") || strings.Contains(stored, ta[1]) || strings.Contains(stored, tb[1]) {
		t.Fatal("the cache must hold the template bytes only")
	}
}

// modeEngine renders the comment form only while the entity is open, like the
// real theme does from the resolved mode.
type modeEngine struct {
	threadEngine
	closed *atomic.Bool
}

func (e modeEngine) RenderPage(w io.Writer, v *render.PageView) error {
	if e.closed.Load() {
		_, err := io.WriteString(w, "<html><body><p>Comments are closed.</p></body></html>")
		return err
	}
	return e.threadEngine.RenderPage(w, v)
}

// Closing an entity's comments in admin drops its cached page, so the next
// visitor no longer gets the comment form.
func TestCommentCloseEntityStopsCachedForm(t *testing.T) {
	r := newCacheRig(t)
	var closed atomic.Bool
	r.h.engine = modeEngine{threadEngine: r.h.engine.(threadEngine), closed: &closed}
	if w := r.get("/a"); !strings.Contains(w.Body.String(), "data-hm-cform") {
		t.Fatalf("open page has no form: %s", w.Body.String())
	}
	r.get("/b")
	if !r.cached("/a") || !r.cached("/b") {
		t.Fatal("pages not primed")
	}

	closed.Store(true)
	if w := r.get("/a"); !strings.Contains(w.Body.String(), "data-hm-cform") {
		t.Fatal("rig: the cached page should still carry the form before the drop")
	}
	o := commentCacheObserver{drop: r.dropEntity, all: func(context.Context) { t.Fatal("entity change dropped everything") }}
	o.OnModeChanged(context.Background(), r.ids["/a"])
	if r.cached("/a") || !r.cached("/b") {
		t.Fatal("closing entity A must drop its pages only")
	}
	w := r.get("/a")
	if body := w.Body.String(); strings.Contains(body, "data-hm-cform") || strings.Contains(body, "__hmcf_") {
		t.Fatalf("closed entity still serves the comment form: %s", body)
	}
	if r.resolvesOf("/a") != 2 {
		t.Fatalf("resolves a=%d, want 2", r.resolvesOf("/a"))
	}
}
