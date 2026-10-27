//go:build integration

package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testDelivererOn(svc *Service, o Options) *Deliverer {
	o.AllowPrivate = true
	return NewDeliverer(svc.repo, svc.log, o)
}

func subOn(t *testing.T, svc *Service, url string, mut func(*Subscription)) Subscription {
	t.Helper()
	s := validSub()
	s.URL, s.AllowHTTP = url, true
	s.RetryDelaySeconds = 0
	if mut != nil {
		mut(&s)
	}
	created, _, err := svc.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestRetriesUntilDelivered(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("nope"))
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, nil)
	if n, err := svc.repo.Enqueue(ctx, EventEntityCreated, map[string]string{"a": "b"}, time.Now()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	d := testDelivererOn(svc, Options{})
	// retry delay 0: each Run makes one attempt (rows are only due at run start)
	for i := 0; i < 3; i++ {
		if err := d.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if len(rows) != 1 || rows[0].Status != "delivered" || rows[0].Attempts != 3 || hits.Load() != 3 {
		t.Fatalf("rows = %+v, hits = %d", rows, hits.Load())
	}
}

func TestRetriesUseBackoffAndExhaust(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, func(s *Subscription) { s.MaxRetries, s.RetryDelaySeconds = 1, 60 })
	svc.repo.Enqueue(ctx, EventEntityCreated, nil, time.Now())
	d := testDelivererOn(svc, Options{})
	if err := d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if rows[0].Status != "pending" || rows[0].Attempts != 1 || rows[0].Error != "receiver answered HTTP 503" ||
		time.Until(rows[0].NextAttemptAt) < 100*time.Second { // Backoff(1, 60s) = 2m
		t.Fatalf("after 1st: %+v", rows[0])
	}
	exec(t, svc, `UPDATE hatamoto_webhook_deliveries SET next_attempt_at = now()`)
	if err := d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if rows[0].Status != "failed" || rows[0].Attempts != 2 {
		t.Fatalf("after 2nd: %+v", rows[0])
	}
}

func TestPausedSubscriptionFailsQueuedRowsWithoutAnAttempt(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, nil)
	svc.repo.Enqueue(ctx, EventEntityCreated, nil, time.Now())
	if err := svc.SetActive(ctx, sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := testDelivererOn(svc, Options{}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if rows[0].Status != "failed" || rows[0].Attempts != 0 || rows[0].Error != "subscription removed or paused" || hits.Load() != 0 {
		t.Fatalf("row = %+v hits = %d", rows[0], hits.Load())
	}
}

func TestHungReceiverDoesNotTrapOtherSubscriptions(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer hung.Close()
	defer close(release)
	var okHits atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { okHits.Add(1) }))
	defer good.Close()
	hungSub := subOn(t, svc, hung.URL, func(s *Subscription) { s.TimeoutSeconds = 1 })
	subOn(t, svc, good.URL, nil)
	for i := 0; i < 3; i++ { // three events: the hung sub gets 3 rows, but only the first is tried
		svc.repo.Enqueue(ctx, EventEntityCreated, nil, time.Now())
	}
	start := time.Now()
	if err := testDelivererOn(svc, Options{RunBudget: 20 * time.Second}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("run took %s: the hung receiver was retried within the run", took)
	}
	if okHits.Load() != 3 {
		t.Fatalf("good receiver got %d deliveries, want 3", okHits.Load())
	}
	var tried int
	for _, r := range func() []DeliveryRow { r, _ := svc.repo.ListDeliveries(ctx, hungSub.ID, 10, 0); return r }() {
		tried += r.Attempts
	}
	if tried != 1 {
		t.Fatalf("hung sub attempts = %d, want 1", tried)
	}
}

func TestDeadClaimCountsAsAnAttemptInRun(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, func(s *Subscription) { s.MaxRetries = 0 }) // max_attempts 1
	svc.repo.Enqueue(ctx, EventEntityCreated, nil, time.Now())
	runStart, _ := svc.repo.DBNow(ctx)
	if d, err := svc.repo.ClaimOne(ctx, runStart, nil); err != nil || d == nil {
		t.Fatal(d, err) // the claimer "dies" here
	}
	exec(t, svc, `UPDATE hatamoto_webhook_deliveries SET leased_until = now() - interval '1 second'`)
	if err := testDelivererOn(svc, Options{}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if rows[0].Status != "failed" || rows[0].Attempts != 1 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestSendTestRecordsAParkedDelivery(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	var mu sync.Mutex
	var event string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		event = r.Header.Get("X-Webhook-Event")
		mu.Unlock()
	}))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, func(s *Subscription) { s.Events = []string{EventEntityDeleted} })
	d := testDelivererOn(svc, Options{})
	id, err := d.SendTest(ctx, sub.ID)
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	mu.Lock()
	defer mu.Unlock()
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if event != EventTest || len(rows) != 1 || rows[0].ID != id || rows[0].Status != "delivered" || rows[0].MaxAttempts != 1 {
		t.Fatalf("event = %q rows = %+v", event, rows)
	}
	if _, err := d.SendTest(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("unknown sub: %v", err)
	}
}

func TestSendTestFailureIsFinal(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	sub := subOn(t, svc, srv.URL, nil)
	id, err := testDelivererOn(svc, Options{}).SendTest(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	if rows[0].ID != id || rows[0].Status != "failed" || rows[0].Attempts != 1 {
		t.Fatalf("row = %+v", rows[0])
	}
}
