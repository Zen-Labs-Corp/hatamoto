package redirect

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeStore is an in-memory Store; unused methods are no-ops.
type fakeStore struct {
	mu    sync.Mutex
	rows  []*Redirect
	loads int
	hits  map[uuid.UUID]int
}

func newFakeStore(rows ...*Redirect) *fakeStore {
	for _, r := range rows {
		if r.ID == uuid.Nil {
			r.ID = uuid.New()
		}
		if r.StatusCode == 0 {
			r.StatusCode = 301
		}
		r.IsActive = true
	}
	return &fakeStore{rows: rows, hits: map[uuid.UUID]int{}}
}

func (f *fakeStore) GetActiveRedirects(context.Context) ([]*Redirect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	return append([]*Redirect(nil), f.rows...), nil
}
func (f *fakeStore) AddRedirectHits(_ context.Context, id uuid.UUID, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[id] += n
	return nil
}
func (f *fakeStore) CreateRedirect(_ context.Context, in *CreateRedirectInput) (*Redirect, error) {
	r := &Redirect{ID: uuid.New(), SourcePath: in.SourcePath, TargetURL: in.TargetURL, StatusCode: in.StatusCode, IsActive: true}
	f.mu.Lock()
	f.rows = append(f.rows, r)
	f.mu.Unlock()
	return r, nil
}
func (f *fakeStore) GetRedirectByID(_ context.Context, id uuid.UUID) (*Redirect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.ID == id {
			c := *r
			return &c, nil
		}
	}
	return nil, ErrRedirectNotFound
}
func (f *fakeStore) UpdateRedirect(ctx context.Context, id uuid.UUID, in *UpdateRedirectInput) (*Redirect, error) {
	return f.GetRedirectByID(ctx, id)
}
func (f *fakeStore) Upsert404Entry(context.Context, string, string, string) error { return nil }
func (f *fakeStore) List404Entries(context.Context, NotFoundListParams) ([]*NotFoundEntry, int, error) {
	return nil, 0, nil
}
func (f *fakeStore) ListRecent404Entries(context.Context, time.Time, int) ([]*NotFoundEntry, error) {
	return nil, nil
}
func (f *fakeStore) Ignore404Entry(context.Context, uuid.UUID) error               { return nil }
func (f *fakeStore) Delete404Entry(context.Context, uuid.UUID) error               { return nil }
func (f *fakeStore) Link404ToRedirect(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeStore) DeleteRedirect(context.Context, uuid.UUID) error               { return nil }
func (f *fakeStore) ListRedirects(context.Context, RedirectListParams) ([]*Redirect, int, error) {
	return nil, 0, nil
}

func newTestService(t *testing.T, store Store, logs *bytes.Buffer) *Service {
	t.Helper()
	var w = &bytes.Buffer{}
	if logs != nil {
		w = logs
	}
	s := NewServiceWithStore(store, slog.New(slog.NewTextHandler(w, nil)))
	t.Cleanup(s.Close)
	return s
}

func TestCacheSplitsExactAndPatterns(t *testing.T) {
	store := newFakeStore(
		&Redirect{SourcePath: "/docs/old", TargetURL: "/exact"},
		&Redirect{SourcePath: "/docs/**", TargetURL: "/broad/$1"},
		&Redirect{SourcePath: "/docs/*/intro", TargetURL: "/narrow/$1"},
	)
	s := newTestService(t, store, nil)
	ctx := context.Background()

	if rd := s.CheckRedirect(ctx, "/docs/old"); rd == nil || rd.TargetURL != "/exact" {
		t.Fatalf("exact lookup: %+v", rd)
	}
	if rd := s.CheckRedirect(ctx, "/docs/a/b"); rd != nil {
		t.Fatalf("exact lookup must not see wildcard rows: %+v", rd)
	}
	// Callers try exact first; a path in both resolves to the exact row.
	if rd := s.CheckRedirectPattern(ctx, "/docs/old"); rd == nil || rd.TargetURL != "/broad/old" {
		t.Fatalf("pattern lookup: %+v", rd)
	}
	// More literal segments wins.
	if rd := s.CheckRedirectPattern(ctx, "/docs/x/intro"); rd == nil || rd.TargetURL != "/narrow/x" {
		t.Fatalf("specificity: %+v", rd)
	}
	if rd := s.CheckRedirectPattern(ctx, "/other"); rd != nil {
		t.Fatalf("unexpected match: %+v", rd)
	}
}

func TestCacheSkipsInvalidWildcardRowsAndLogsOnce(t *testing.T) {
	bad := &Redirect{ID: uuid.New(), SourcePath: "/industries/**/old", TargetURL: "/secret-target"}
	loop := &Redirect{ID: uuid.New(), SourcePath: "/docs/**", TargetURL: "/docs/archive"}
	catchAll := &Redirect{ID: uuid.New(), SourcePath: "/**", TargetURL: "/maintenance"}
	external := &Redirect{ID: uuid.New(), SourcePath: "/**", TargetURL: "https://example.com/"}
	good := &Redirect{ID: uuid.New(), SourcePath: "/ok/*", TargetURL: "/fine/$1"}
	store := newFakeStore(bad, loop, catchAll, good)
	var logs bytes.Buffer
	s := newTestService(t, store, &logs)
	ctx := context.Background()

	if rd := s.CheckRedirectPattern(ctx, "/industries/a/old"); rd != nil {
		t.Fatalf("malformed row must be skipped, got %+v", rd)
	}
	if rd := s.CheckRedirectPattern(ctx, "/docs/x"); rd != nil {
		t.Fatalf("self-looping row must be skipped, got %+v", rd)
	}
	if rd := s.CheckRedirectPattern(ctx, "/anything"); rd != nil {
		t.Fatalf("catch-all must be skipped, got %+v", rd)
	}
	if rd := s.CheckRedirectPattern(ctx, "/ok/z"); rd == nil || rd.TargetURL != "/fine/z" {
		t.Fatalf("valid row lost: %+v", rd)
	}

	s.invalidateCache() // reload: already-reported ids stay quiet
	s.CheckRedirectPattern(ctx, "/ok/z")
	out := logs.String()
	for _, r := range []*Redirect{bad, loop, catchAll} {
		if n := strings.Count(out, "id="+r.ID.String()); n != 1 {
			t.Errorf("id %s logged %d times, want 1", r.ID, n)
		}
	}
	if strings.Contains(out, "secret-target") || strings.Contains(out, "/industries") {
		t.Errorf("full row leaked into logs: %s", out)
	}
	if !strings.Contains(out, "count=3") {
		t.Errorf("skipped count missing: %s", out)
	}

	// An absolute external catch-all is allowed.
	s2 := newTestService(t, newFakeStore(external), nil)
	if rd := s2.CheckRedirectPattern(ctx, "/x/y"); rd == nil || rd.TargetURL != "https://example.com/" {
		t.Fatalf("external catch-all: %+v", rd)
	}
}

func TestCreateAndUpdateRejectUnsafeRules(t *testing.T) {
	existing := &Redirect{SourcePath: "/docs/**", TargetURL: "/elsewhere"}
	s := newTestService(t, newFakeStore(existing), nil)
	ctx := context.Background()

	for _, c := range []struct{ src, dst string }{
		{"/a/**/b", "/x"},
		{"/**", "/maintenance"},
		{"/*", "/maintenance"},
		{"/docs/**", "/docs/archive"},
		{"/docs/*", "/docs/$1"},
		{"/*/foo", "/foo"},
	} {
		_, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: c.src, TargetURL: c.dst, StatusCode: 301})
		if !errors.Is(err, ErrInvalidPattern) && !errors.Is(err, ErrUnsafePattern) {
			t.Errorf("create %s -> %s: err = %v", c.src, c.dst, err)
		}
	}
	for _, c := range []struct{ src, dst string }{
		{"/**", "https://example.com/"},
		{"/docs/*", "/archive/$1"},
		{"/docs/*", "/guides/$1"},
	} {
		if _, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: c.src, TargetURL: c.dst, StatusCode: 301}); err != nil {
			t.Errorf("create %s -> %s: %v", c.src, c.dst, err)
		}
	}

	// Updating only the target is checked against the stored source.
	bad := "/docs/archive"
	if _, err := s.UpdateRedirect(ctx, existing.ID, &UpdateRedirectInput{TargetURL: &bad}); !errors.Is(err, ErrUnsafePattern) {
		t.Errorf("update target: err = %v", err)
	}
	src := "/a/**/b"
	if _, err := s.UpdateRedirect(ctx, existing.ID, &UpdateRedirectInput{SourcePath: &src}); !errors.Is(err, ErrInvalidPattern) {
		t.Errorf("update source: err = %v", err)
	}
}

func TestPeekMirrorsLookupOrderWithoutCounting(t *testing.T) {
	exact := &Redirect{SourcePath: "/old", TargetURL: "/new"}
	slashed := &Redirect{SourcePath: "/with-slash/", TargetURL: "/n2", StatusCode: 302}
	store := newFakeStore(exact, slashed,
		&Redirect{SourcePath: "/docs/**", TargetURL: "/broad/$1"})
	s := newTestService(t, store, nil)
	ctx := context.Background()

	if rd := s.Peek(ctx, "/old"); rd == nil || rd.TargetURL != "/new" {
		t.Fatalf("exact: %+v", rd)
	}
	if rd := s.Peek(ctx, "/old/"); rd == nil || rd.TargetURL != "/new" {
		t.Fatalf("alt (slash added): %+v", rd)
	}
	if rd := s.Peek(ctx, "/with-slash"); rd == nil || rd.StatusCode != 302 {
		t.Fatalf("alt (slash removed): %+v", rd)
	}
	if rd := s.Peek(ctx, "/docs/a/b"); rd == nil || rd.TargetURL != "/broad/a/b" {
		t.Fatalf("pattern: %+v", rd)
	}
	if rd := s.Peek(ctx, "/nothing"); rd != nil {
		t.Fatalf("unexpected: %+v", rd)
	}
	time.Sleep(50 * time.Millisecond) // let any (buggy) async increment land
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.hits) != 0 {
		t.Fatalf("Peek incremented hit counters: %v", store.hits)
	}
}

func TestSameHostOnlyRefusesExternalTargets(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		target string
		ok     bool
	}{
		{"/new", true},
		{"https://evil.example/login", false},
		{"http://evil.example", false},
		{"//evil.example", false},
		{`/\evil.example`, false},
	} {
		s := NewServiceWithStore(newFakeStore(), slog.Default())
		s.SameHostOnly = true
		_, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: "/login", TargetURL: tc.target, StatusCode: 301})
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrExternalTarget)) {
			t.Errorf("create %q: err=%v", tc.target, err)
		}
	}
	// Outside the demo an absolute target stays allowed.
	s := NewServiceWithStore(newFakeStore(), slog.Default())
	if _, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: "/a", TargetURL: "https://example.org/", StatusCode: 301}); err != nil {
		t.Errorf("demo off: %v", err)
	}
	// An update that only changes the target is checked too.
	st := newFakeStore(&Redirect{SourcePath: "/b", TargetURL: "/c"})
	s = NewServiceWithStore(st, slog.Default())
	s.SameHostOnly = true
	ext := "https://evil.example/"
	if _, err := s.UpdateRedirect(ctx, st.rows[0].ID, &UpdateRedirectInput{TargetURL: &ext}); !errors.Is(err, ErrExternalTarget) {
		t.Errorf("update: %v", err)
	}
}
