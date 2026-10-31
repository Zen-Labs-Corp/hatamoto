package agents

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const gptUA = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.1; +https://openai.com/gptbot"

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeStore struct {
	mu      sync.Mutex
	hits    []Hit
	batches chan []Hit
	block   chan struct{} // when non-nil, InsertHits waits on it
}

func newFakeStore() *fakeStore { return &fakeStore{batches: make(chan []Hit, 64)} }

func (f *fakeStore) InsertHits(ctx context.Context, hits []Hit) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.hits = append(f.hits, hits...)
	f.mu.Unlock()
	f.batches <- hits
	return nil
}

func (f *fakeStore) all() []Hit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Hit(nil), f.hits...)
}

func waitBatch(t *testing.T, f *fakeStore) []Hit {
	t.Helper()
	select {
	case b := <-f.batches:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a flush")
		return nil
	}
}

func TestRecordDropsWhenFull(t *testing.T) {
	st := newFakeStore()
	st.block = make(chan struct{})
	w := NewWriter(st, Default(), nil, discard)
	t.Cleanup(func() { close(st.block); w.Close() })

	// Fill: one batch (100) is stuck inside the blocked store, the queue holds queueSize more.
	const total = flushBatch + queueSize + 50
	start := time.Now()
	for range total + 200 {
		w.Record(RawHit{UA: gptUA, Path: "/x", Source: SourceBackend})
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Record blocked: %v", elapsed)
	}
	if got := w.Stats().Dropped; got == 0 {
		t.Fatalf("expected drops, got %d", got)
	}
	// And a single Record on a full queue returns promptly, reporting the refusal.
	done := make(chan struct{})
	go func() {
		if w.Record(RawHit{UA: gptUA, Path: "/y", Source: SourceBackend}) {
			t.Error("Record on a full queue must report false")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Record blocked on a full queue")
	}
}

func TestRecordAfterCloseNoPanic(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 500 {
				w.Record(RawHit{UA: gptUA, Path: "/x", Source: SourceBackend})
			}
		})
	}
	w.Close()
	wg.Wait()
	w.Close() // idempotent
	w.Record(RawHit{UA: gptUA, Path: "/after", Source: SourceBackend})
	for _, h := range st.all() {
		if h.Path == "/after" {
			t.Fatal("hit recorded after Close")
		}
	}
}

func TestCloseFlushesBuffered(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	for range 7 {
		w.Record(RawHit{UA: gptUA, Path: "/x", Source: SourceBackend})
	}
	w.Close()
	if n := len(st.all()); n != 7 {
		t.Fatalf("stored %d, want 7", n)
	}
	if s := w.Stats(); s.Written != 7 {
		t.Fatalf("Written = %d, want 7", s.Written)
	}
}

func TestUnknownUAIgnored(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	w.Record(RawHit{UA: "Mozilla/5.0 Firefox/120.0", Path: "/", Source: SourceBackend})
	w.Record(RawHit{UA: "some-crawler/1.0", Path: "/", Source: SourceBackend}) // hint-only, not a known agent
	w.Close()
	if n := len(st.all()); n != 0 {
		t.Fatalf("stored %d hits for unknown UAs", n)
	}
}

func TestFlushBatches(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	t.Cleanup(w.Close)
	for range flushBatch {
		w.Record(RawHit{UA: gptUA, Path: "/p", Source: SourceBackend})
	}
	if b := waitBatch(t, st); len(b) != flushBatch {
		t.Fatalf("batch = %d, want %d", len(b), flushBatch)
	}
}

func TestHitFields(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	var observed atomic.Int32
	w.SetObserver(func(h []Hit) { observed.Add(int32(len(h))) })
	w.Record(RawHit{UA: gptUA, IP: "::ffff:20.171.207.1", Method: "GET", Path: "/sitemap.xml?x=1", Status: 404, Bytes: 12, DurationMS: 3, Source: SourceBackend})
	w.Record(RawHit{UA: gptUA, IP: "not-an-ip", Path: "/llms.txt", Source: SourceBackend}) // no status written => 200
	w.Close()
	hits := st.all()
	if len(hits) != 2 {
		t.Fatalf("got %d hits", len(hits))
	}
	h := hits[0]
	if h.AgentID != "gptbot" || h.Operator != "OpenAI" || h.Purpose != PurposeTraining || h.Verification != VerificationPending {
		t.Errorf("classification wrong: %+v", h)
	}
	if h.IP == nil || h.IP.String() != "20.171.207.1" {
		t.Errorf("ip = %v, want unmapped 20.171.207.1", h.IP)
	}
	if h.Path != "/sitemap.xml" || h.Status != 404 || h.Bytes != 12 {
		t.Errorf("fields wrong: %+v", h)
	}
	if hits[1].IP != nil || hits[1].Status != 200 {
		t.Errorf("bad ip must be nil and default status 200: %+v", hits[1])
	}
	if observed.Load() != 2 {
		t.Errorf("observer saw %d hits, want 2", observed.Load())
	}
}

func TestResolverOnlyForMiddleware(t *testing.T) {
	st := newFakeStore()
	id := uuid.New()
	var calls atomic.Int32
	resolve := func(ctx context.Context, path string) (*uuid.UUID, int) {
		calls.Add(1)
		return &id, 200
	}
	w := NewWriter(st, Default(), resolve, discard)
	w.Record(RawHit{UA: gptUA, Path: "/page", Source: SourceMiddleware})                    // resolved
	w.Record(RawHit{UA: gptUA, Path: "/robots.txt", Status: 200, Source: SourceMiddleware}) // explicit status: skipped
	w.Record(RawHit{UA: gptUA, Path: "/sitemap.xml", Source: SourceBackend})                // backend: skipped
	w.Record(RawHit{UA: gptUA, Path: "/post/example-slug", Status: 200, Source: SourceLog}) // served page: entity only
	w.Close()
	if calls.Load() != 2 {
		t.Fatalf("resolver called %d times, want 2", calls.Load())
	}
	byPath := map[string]Hit{}
	for _, h := range st.all() {
		byPath[h.Path] = h
	}
	if p := byPath["/page"]; p.EntityID == nil || *p.EntityID != id || p.Status != 200 {
		t.Errorf("page hit not resolved: %+v", p)
	}
	if r := byPath["/robots.txt"]; r.EntityID != nil || r.Status != 200 {
		t.Errorf("robots hit wrong: %+v", r)
	}
}

// An explicit status below 400 keeps its status and still gets the entity; an
// explicit error status never does, and the resolver's status never overrides.
func TestExplicitStatusGetsEntityKeepsStatus(t *testing.T) {
	st := newFakeStore()
	id := uuid.New()
	var paths []string
	var mu sync.Mutex
	resolve := func(ctx context.Context, path string) (*uuid.UUID, int) {
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		return &id, 404 // deliberately different: must not replace the explicit status
	}
	w := NewWriter(st, Default(), resolve, discard)
	w.Record(RawHit{UA: gptUA, Path: "/post/example-slug", Status: 200, Source: SourceBackend})
	w.Record(RawHit{UA: gptUA, Path: "/fr/post/example-slug", Status: 301, Source: SourceLog})
	w.Record(RawHit{UA: gptUA, Path: "/post/broken", Status: 500, Source: SourceBackend})
	for _, f := range []string{"/robots.txt", "/llms.txt", "/sitemap.xml", "/sitemap-posts.xml"} {
		w.Record(RawHit{UA: gptUA, Path: f, Status: 200, Source: SourceBackend})
	}
	w.Close()
	if len(paths) != 2 {
		t.Fatalf("resolver paths = %v, want the two served pages only", paths)
	}
	for _, h := range st.all() {
		switch h.Path {
		case "/post/example-slug":
			if h.EntityID == nil || *h.EntityID != id || h.Status != 200 {
				t.Errorf("served page: %+v", h)
			}
		case "/fr/post/example-slug":
			if h.EntityID == nil || h.Status != 301 {
				t.Errorf("redirected page: %+v", h)
			}
		default:
			if h.EntityID != nil {
				t.Errorf("%s must have no entity: %+v", h.Path, h)
			}
		}
	}
}

func TestIsFilePath(t *testing.T) {
	for p, want := range map[string]bool{
		"/robots.txt": true, "/llms.txt": true, "/sitemap.xml": true, "/sitemap-posts.xml": true,
		"/api/v1/public/sitemap.xml": true, "/post/example-slug": false, "/": false, "/sitemap": false,
	} {
		if got := IsFilePath(p); got != want {
			t.Errorf("IsFilePath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCachedResolver(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls int
	fn := func(ctx context.Context, path string) (*uuid.UUID, int) { calls++; return nil, 404 }
	r := newCachedResolver(fn, time.Minute, 2, func() time.Time { return now })
	ctx := context.Background()

	r(ctx, "/a")
	r(ctx, "/a")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls)
	}
	now = now.Add(2 * time.Minute)
	r(ctx, "/a")
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after TTL", calls)
	}
	r(ctx, "/b")
	r(ctx, "/c") // cache full (2): cleared, still bounded
	r(ctx, "/c")
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

// A miss is remembered only briefly, so a page published right after a crawler
// asked for it is attributed on the next hit; an entity result lasts the ttl.
func TestCachedResolverNilExpiresEarly(t *testing.T) {
	now := time.Unix(1000, 0)
	id := uuid.New()
	var calls int
	fn := func(ctx context.Context, path string) (*uuid.UUID, int) {
		calls++
		if path == "/hit" {
			return &id, 200
		}
		return nil, 404
	}
	r := newCachedResolver(fn, time.Minute, 10, func() time.Time { return now })
	ctx := context.Background()
	r(ctx, "/miss")
	r(ctx, "/hit")
	now = now.Add(nilTTL + time.Second)
	r(ctx, "/miss")
	r(ctx, "/hit")
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (miss refetched, entity still cached)", calls)
	}
	now = now.Add(time.Minute)
	r(ctx, "/hit")
	if calls != 4 {
		t.Fatalf("calls = %d, want 4 after ttl", calls)
	}
}

func TestBuildSQL(t *testing.T) {
	id := uuid.New()
	hits := []Hit{
		{At: time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC), AgentID: "gptbot", Path: "/a", Status: 200, Bytes: 5, EntityID: &id, Purpose: PurposeTraining},
		{At: time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC), AgentID: "gptbot", Path: "/a", Status: 200, Bytes: 7, Purpose: PurposeTraining},
	}
	q, args := buildHitsInsert(hits)
	if len(args) != 2*hitColumns || !strings.Contains(q, "$28") {
		t.Errorf("hits insert placeholders wrong: %d args", len(args))
	}
	agg := aggregateDaily(hits)
	if len(agg) != 1 {
		t.Fatalf("aggregate keys = %d, want 1", len(agg))
	}
	for _, a := range agg {
		if a.hits != 2 || a.bytes != 12 || a.entityID == nil {
			t.Errorf("aggregate wrong: %+v", a)
		}
	}
	q, args = buildDailyUpsert(agg)
	if !strings.Contains(q, "ON CONFLICT (day, agent_id, path, status)") || len(args) != dailyColumns {
		t.Errorf("daily upsert wrong: %q (%d args)", q, len(args))
	}
	if strings.Contains(strings.ToLower(q), " ip") {
		t.Error("rollup must never carry an IP")
	}
}

// poisonStore rejects any batch containing the path "/bad", like Postgres
// rejecting one malformed row of a multi-row insert.
type poisonStore struct {
	mu   sync.Mutex
	hits []Hit
}

func (p *poisonStore) InsertHits(_ context.Context, hits []Hit) error {
	for _, h := range hits {
		if h.Path == "/bad" {
			return &pgconn.PgError{Code: "22021"}
		}
	}
	p.mu.Lock()
	p.hits = append(p.hits, hits...)
	p.mu.Unlock()
	return nil
}

func TestFlushIsolatesPoisonRow(t *testing.T) {
	st := &poisonStore{}
	w := NewWriter(st, Default(), nil, discard)
	for i := range 7 {
		p := "/ok"
		if i == 3 {
			p = "/bad"
		}
		w.Record(RawHit{UA: gptUA, Path: p, Source: SourceBackend})
	}
	w.Close()
	if len(st.hits) != 6 {
		t.Fatalf("stored %d rows, want 6", len(st.hits))
	}
	if s := w.Stats(); s.Written != 6 || s.Failed != 1 {
		t.Fatalf("stats written=%d failed=%d, want 6/1", s.Written, s.Failed)
	}
}

type errStore struct {
	calls atomic.Int32
	err   error
}

func (e *errStore) InsertHits(context.Context, []Hit) error {
	e.calls.Add(1)
	return e.err
}

func TestFlushDoesNotSplitOnInfraError(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, &pgconn.PgError{Code: "53300"}, &pgconn.PgError{Code: "08006"}} {
		st := &errStore{err: err}
		w := NewWriter(st, Default(), nil, discard)
		for range 7 {
			w.Record(RawHit{UA: gptUA, Path: "/ok", Source: SourceBackend})
		}
		w.Close()
		if n := st.calls.Load(); n != 1 {
			t.Fatalf("%v: InsertHits called %d times, want 1", err, n)
		}
		if s := w.Stats(); s.Failed != 7 || s.Written != 0 {
			t.Fatalf("%v: written=%d failed=%d, want 0/7", err, s.Written, s.Failed)
		}
	}
}

// hangStore blocks until its context ends, like a hung database.
type hangStore struct{ calls atomic.Int32 }

func (h *hangStore) InsertHits(ctx context.Context, _ []Hit) error {
	h.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func TestCloseHonoursDrainDeadline(t *testing.T) {
	st := &hangStore{}
	w := NewWriter(st, Default(), nil, discard)
	w.drainTimeout = 100 * time.Millisecond
	for range 7 {
		w.Record(RawHit{UA: gptUA, Path: "/x", Source: SourceBackend})
	}
	start := time.Now()
	w.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Close took %v with a 100ms drain deadline", d)
	}
	if n := st.calls.Load(); n != 1 {
		t.Fatalf("InsertHits called %d times, want 1", n)
	}
}

func TestRedirectHintOnlyFillsResolverMiss(t *testing.T) {
	st := newFakeStore()
	id := uuid.New()
	resolve := func(ctx context.Context, path string) (*uuid.UUID, int) {
		switch path {
		case "/served":
			return &id, 200
		case "/admin-redirected":
			return nil, 302
		}
		return nil, 404
	}
	w := NewWriter(st, Default(), resolve, discard)
	for _, p := range []string{"/served", "/admin-redirected", "/blog/gone"} {
		w.Record(RawHit{UA: gptUA, Path: p, RedirectHint: 301, Source: SourceMiddleware})
	}
	w.Close()
	got := map[string]int{}
	for _, h := range st.all() {
		got[h.Path] = h.Status
	}
	want := map[string]int{"/served": 200, "/admin-redirected": 302, "/blog/gone": 301}
	for p, s := range want {
		if got[p] != s {
			t.Errorf("%s status = %d, want %d", p, got[p], s)
		}
	}
}

func TestRedirectHintWithoutResolver(t *testing.T) {
	st := newFakeStore()
	w := NewWriter(st, Default(), nil, discard)
	w.Record(RawHit{UA: gptUA, Path: "/blog/x", RedirectHint: 301, Source: SourceMiddleware})
	w.Close()
	if h := st.all(); len(h) != 1 || h[0].Status != 301 {
		t.Fatalf("hits = %+v", h)
	}
}

// The daily upsert must emit rows in (day, agent_id, path, status) order, the
// order verify locks them in, regardless of map iteration order.
func TestBuildDailyUpsertSortedKeyOrder(t *testing.T) {
	d1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	want := []dailyKey{
		{d1, "claudebot", "/a", 200}, {d1, "gptbot", "/", 200}, {d1, "gptbot", "/", 404},
		{d1, "gptbot", "/robots.txt", 200}, {d1, "gptbot", "/z", 0}, {d2, "claudebot", "/", 200},
	}
	for round := range 30 { // map order is random; many rounds make a miss near-certain
		rows := map[dailyKey]*dailyAgg{}
		for _, k := range want {
			rows[k] = &dailyAgg{operator: "o", purpose: "training", hits: 1}
		}
		_, args := buildDailyUpsert(rows)
		for i, k := range want {
			b := i * dailyColumns
			got := dailyKey{args[b].(time.Time), args[b+1].(string), args[b+2].(string), args[b+3].(int)}
			if got != k {
				t.Fatalf("round %d row %d = %+v, want %+v", round, i, got, k)
			}
		}
	}
}

// retryStore fails the first call with a deadlock, then succeeds.
type retryStore struct {
	calls atomic.Int32
	errs  []error
}

func (r *retryStore) InsertHits(context.Context, []Hit) error {
	n := int(r.calls.Add(1))
	if n <= len(r.errs) {
		return r.errs[n-1]
	}
	return nil
}

func TestFlushRetriesOnceOnDeadlock(t *testing.T) {
	st := &retryStore{errs: []error{&pgconn.PgError{Code: "40P01"}}}
	w := NewWriter(st, Default(), nil, discard)
	for range 7 {
		w.Record(RawHit{UA: gptUA, Path: "/ok", Source: SourceBackend})
	}
	w.Close()
	if n := st.calls.Load(); n != 2 {
		t.Fatalf("InsertHits called %d times, want 2", n)
	}
	if s := w.Stats(); s.Written != 7 || s.Failed != 0 {
		t.Fatalf("written=%d failed=%d, want 7/0 (counted once)", s.Written, s.Failed)
	}

	// A second consecutive rollback is not retried again: the batch fails once.
	st = &retryStore{errs: []error{&pgconn.PgError{Code: "40001"}, &pgconn.PgError{Code: "40P01"}}}
	w = NewWriter(st, Default(), nil, discard)
	for range 7 {
		w.Record(RawHit{UA: gptUA, Path: "/ok", Source: SourceBackend})
	}
	w.Close()
	if n := st.calls.Load(); n != 2 {
		t.Fatalf("persistent rollback: %d calls, want 2", n)
	}
	if s := w.Stats(); s.Failed != 7 || s.Written != 0 {
		t.Fatalf("persistent rollback: written=%d failed=%d, want 0/7", s.Written, s.Failed)
	}
}

// Log-source hits carry their own status (e.g. a 503 for a page): the writer
// must store it verbatim, never call the resolver and leave entity_id NULL.
// Sources coexist: dedup is by capture policy (plan decision 19), not storage,
// so the same path from different sources is kept as separate rows.
func TestLogSourceKeepsStatusSkipsResolver(t *testing.T) {
	st := newFakeStore()
	var calls atomic.Int32
	id := uuid.New()
	resolve := func(context.Context, string) (*uuid.UUID, int) {
		calls.Add(1)
		return &id, 200
	}
	w := NewWriter(st, Default(), resolve, discard)
	w.Record(RawHit{UA: gptUA, Path: "/pricing", Status: 503, Bytes: 7, DurationMS: 1500, Source: SourceLog})
	w.Record(RawHit{UA: gptUA, Path: "/media/a.png", Status: 404, Source: SourceLog})
	w.Record(RawHit{UA: gptUA, Path: "/media/a.png", Source: SourceMiddleware}) // same path, other source
	w.Close()
	if calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1 (middleware hit only)", calls.Load())
	}
	var logHits int
	for _, h := range st.all() {
		if h.Source != SourceLog {
			continue
		}
		logHits++
		if h.EntityID != nil {
			t.Errorf("log hit %s must have no entity: %+v", h.Path, h)
		}
		if h.Path == "/pricing" && (h.Status != 503 || h.Bytes != 7 || h.DurationMS != 1500) {
			t.Errorf("log hit fields lost: %+v", h)
		}
		if h.Path == "/media/a.png" && h.Status != 404 {
			t.Errorf("log media status: %+v", h)
		}
	}
	if logHits != 2 || len(st.all()) != 3 {
		t.Fatalf("want 2 log + 3 total hits, got %d / %d", logHits, len(st.all()))
	}
}
