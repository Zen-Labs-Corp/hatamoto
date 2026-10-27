package analytics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

type addCall struct {
	day  time.Time
	kind string
	key  string
	n    int64
}

type fakeRepo struct{ calls []addCall }

func (f *fakeRepo) Add(_ context.Context, day time.Time, kind, key string, n int64) error {
	f.calls = append(f.calls, addCall{day, kind, key, n})
	return nil
}

func newTestService(repo Repository) *Service {
	s := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return time.Date(2026, 10, 15, 23, 59, 0, 0, time.FixedZone("x", 10*3600)) }
	return s
}

func TestServiceRollsUpByUTCDay(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	svc.RecordAIReferral("chatgpt")
	svc.RecordPageViews(3)
	if len(repo.calls) != 0 {
		t.Fatalf("writes before flush: %+v", repo.calls)
	}
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sort.Slice(repo.calls, func(i, j int) bool { return repo.calls[i].kind < repo.calls[j].kind })
	wantDay := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	want := []addCall{
		{wantDay, KindAIReferral, "chatgpt", 1},
		{wantDay, KindPageView, "", 3},
	}
	if len(repo.calls) != len(want) {
		t.Fatalf("calls = %+v", repo.calls)
	}
	for i, w := range want {
		if repo.calls[i] != w {
			t.Errorf("call %d = %+v, want %+v", i, repo.calls[i], w)
		}
	}
}

func track(t *testing.T, svc *Service, ua, body string) int {
	t.Helper()
	h := NewHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/track", strings.NewReader(body))
	req.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	h.Track(rec, req)
	return rec.Code
}

func flushed(t *testing.T, svc *Service, repo *fakeRepo) []addCall {
	t.Helper()
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repo.calls
}

func TestTrackCountsOnePageViewPerRequest(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	code := track(t, svc, "Mozilla/5.0",
		`{"events":[{"type":"page_view"},{"type":"page_view"},{"type":"page_view"}]}`)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	calls := flushed(t, svc, repo)
	if len(calls) != 1 || calls[0].kind != KindPageView || calls[0].n != 1 {
		t.Fatalf("calls = %+v (a request must count at most one view)", calls)
	}
}

func TestTrackBuffersAndMergesBeforeFlush(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	for i := 0; i < 5; i++ {
		track(t, svc, "Mozilla/5.0", `{"events":[{"type":"page_view"}]}`)
	}
	if len(repo.calls) != 0 {
		t.Fatalf("track wrote to the repo synchronously: %+v", repo.calls)
	}
	calls := flushed(t, svc, repo)
	if len(calls) != 1 || calls[0].n != 5 {
		t.Fatalf("calls = %+v, want one merged write of 5", calls)
	}
}

func TestFlushRetriesAfterFailure(t *testing.T) {
	repo := &failingRepo{fail: true}
	svc := newTestService(repo)
	svc.RecordPageViews(2)
	if err := svc.Flush(context.Background()); err == nil {
		t.Fatal("want error")
	}
	repo.fail = false
	svc.RecordPageViews(1)
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.calls) != 1 || repo.calls[0].n != 3 {
		t.Fatalf("calls = %+v, want the failed 2 carried into a write of 3", repo.calls)
	}
}

func TestCloseFlushesPending(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	svc.Start()
	svc.RecordPageViews(4)
	svc.Close(context.Background())
	if len(repo.calls) != 1 || repo.calls[0].n != 4 {
		t.Fatalf("calls = %+v", repo.calls)
	}
}

func TestTrackIgnoresNonPageViewBatchAndCrawlers(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo)
	track(t, svc, "Mozilla/5.0", `{"events":[{"type":"scroll_depth"}]}`)
	track(t, svc, "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)", `{"events":[{"type":"page_view"}]}`)
	if calls := flushed(t, svc, repo); len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestTrackRejectsBadJSON(t *testing.T) {
	if code := track(t, newTestService(&fakeRepo{}), "Mozilla/5.0", `{`); code != http.StatusBadRequest {
		t.Fatalf("status = %d", code)
	}
}

type failingRepo struct {
	fakeRepo
	fail bool
}

func (f *failingRepo) Add(ctx context.Context, day time.Time, kind, key string, n int64) error {
	if f.fail {
		return errors.New("db down")
	}
	return f.fakeRepo.Add(ctx, day, kind, key, n)
}
