//go:build integration

package webhook

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mkSub(t *testing.T, svc *Service, events ...string) Subscription {
	t.Helper()
	s := validSub()
	s.Events = events
	created, _, err := svc.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func exec(t *testing.T, svc *Service, sql string, args ...any) {
	t.Helper()
	if _, err := svc.repo.db.Pool().Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, svc *Service, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := svc.repo.db.Pool().QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnqueueFansOutToMatchingActiveSubscriptions(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	match1 := mkSub(t, svc, EventEntityCreated)
	match2 := mkSub(t, svc, EventEntityCreated, EventEntityDeleted)
	mkSub(t, svc, EventEntityDeleted)
	paused := mkSub(t, svc, EventEntityCreated)
	if err := svc.SetActive(ctx, paused.ID, false); err != nil {
		t.Fatal(err)
	}
	n, err := svc.repo.Enqueue(ctx, EventEntityCreated, map[string]string{"k": "v"}, time.Now())
	if err != nil || n != 2 {
		t.Fatalf("Enqueue = %d, %v; want 2", n, err)
	}
	seen := map[string]bool{}
	rows, err := svc.repo.ListDeliveries(ctx, "", 0, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	for _, r := range rows {
		seen[r.SubscriptionID] = true
		if r.Status != "pending" || r.MaxAttempts != 4 || r.Event != EventEntityCreated {
			t.Fatalf("row = %+v", r)
		}
		var env Envelope
		var payload string
		if err := svc.repo.db.Pool().QueryRow(ctx, `SELECT payload FROM hatamoto_webhook_deliveries WHERE id=$1`, r.ID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil || env.ID != r.ID {
			t.Fatalf("envelope id %q != delivery id %q (%v)", env.ID, r.ID, err)
		}
	}
	if !seen[match1.ID] || !seen[match2.ID] {
		t.Fatalf("seen = %v", seen)
	}
}

func TestEnqueueSkipsSubscriptionAtPendingCap(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	full := mkSub(t, svc, EventEntityCreated)
	other := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload)
		SELECT gen_random_uuid()::text, $1, 'entity.created', '{}' FROM generate_series(1, $2)`, full.ID, MaxPending)
	n, err := svc.repo.Enqueue(ctx, EventEntityCreated, nil, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("Enqueue = %d, %v; want 1", n, err)
	}
	if c := count(t, svc, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE subscription_id=$1`, other.ID); c != 1 {
		t.Fatalf("other sub has %d rows", c)
	}
	if c := count(t, svc, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE subscription_id=$1`, full.ID); c != MaxPending {
		t.Fatalf("full sub grew to %d", c)
	}
}

func TestConcurrentClaimNeverReturnsOneRowTwice(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload)
		SELECT gen_random_uuid()::text, $1, 'entity.created', '{}' FROM generate_series(1, 30)`, sub.ID)
	runStart, _ := svc.repo.DBNow(ctx)
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				d, err := svc.repo.ClaimOne(ctx, runStart, nil)
				if err != nil {
					t.Error(err)
					return
				}
				if d == nil {
					return
				}
				mu.Lock()
				got[d.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(got) != 30 {
		t.Fatalf("claimed %d distinct rows, want 30", len(got))
	}
	for id, n := range got {
		if n != 1 {
			t.Fatalf("row %s claimed %d times", id, n)
		}
	}
}

func TestClaimHonoursLeaseSkipAndDueTime(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	a, b := mkSub(t, svc, EventEntityCreated), mkSub(t, svc, EventEntityCreated)
	for _, s := range []Subscription{a, b} {
		if err := svc.repo.InsertParked(ctx, uuid.NewString(), s.ID, EventEntityCreated, "{}", 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	runStart, _ := svc.repo.DBNow(ctx)
	d, err := svc.repo.ClaimOne(ctx, runStart, []string{a.ID})
	if err != nil || d == nil || d.SubscriptionID != b.ID {
		t.Fatalf("claim with skip = %+v, %v", d, err)
	}
	if d2, _ := svc.repo.ClaimOne(ctx, runStart, []string{a.ID}); d2 != nil {
		t.Fatal("a leased row was claimed again")
	}
	if err := svc.repo.InsertParked(ctx, uuid.NewString(), a.ID, EventEntityCreated, "{}", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	// a's first row is claimable, the parked one is not
	if d, _ := svc.repo.ClaimOne(ctx, runStart, nil); d == nil || d.SubscriptionID != a.ID {
		t.Fatalf("want a's due row, got %+v", d)
	}
	if d, _ := svc.repo.ClaimOne(ctx, runStart, nil); d != nil {
		t.Fatalf("parked row claimed: %+v", d)
	}
}

func TestSettleDeadCountsAnAttemptAndFailsAtMax(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, attempts, max_attempts, leased_until)
		VALUES ('d1', $1, 'e', '{}', 0, 3, now() - interval '1 second'),
		       ('d2', $1, 'e', '{}', 2, 3, now() - interval '1 second'),
		       ('d3', $1, 'e', '{}', 0, 3, now() + interval '1 minute')`, sub.ID)
	n, err := svc.repo.SettleDead(ctx)
	if err != nil || n != 2 {
		t.Fatalf("SettleDead = %d, %v", n, err)
	}
	status := func(id string) (string, int) {
		var s string
		var a int
		if err := svc.repo.db.Pool().QueryRow(ctx, `SELECT status, attempts FROM hatamoto_webhook_deliveries WHERE id=$1`, id).Scan(&s, &a); err != nil {
			t.Fatal(err)
		}
		return s, a
	}
	if s, a := status("d1"); s != "pending" || a != 1 {
		t.Fatalf("d1 = %s/%d", s, a)
	}
	if s, a := status("d2"); s != "failed" || a != 3 {
		t.Fatalf("d2 = %s/%d", s, a)
	}
	if s, a := status("d3"); s != "pending" || a != 0 {
		t.Fatalf("d3 (live lease) = %s/%d", s, a)
	}
}

func TestMarkOutcomesAndRetryFailed(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, max_attempts)
		VALUES ('ok', $1, 'e', '{}', 4), ('retry', $1, 'e', '{}', 4), ('dead', $1, 'e', '{}', 1), ('gone', $1, 'e', '{}', 4)`, sub.ID)
	code := 500
	if err := svc.repo.MarkDelivered(ctx, "ok", 200, "fine", 12*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.MarkRetry(ctx, "retry", &code, "oops", "receiver answered HTTP 500", time.Millisecond, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.MarkExhausted(ctx, "dead", nil, "", "dial failed", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.MarkFailed(ctx, "gone", "subscription removed or paused"); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	by := map[string]DeliveryRow{}
	for _, r := range rows {
		by[r.ID] = r
	}
	if r := by["ok"]; r.Status != "delivered" || r.Attempts != 1 || r.DeliveredAt == nil || *r.ResponseStatus != 200 {
		t.Fatalf("ok = %+v", r)
	}
	if r := by["retry"]; r.Status != "pending" || r.Attempts != 1 || !r.NextAttemptAt.After(time.Now().Add(time.Minute)) || *r.ResponseStatus != 500 {
		t.Fatalf("retry = %+v", r)
	}
	if r := by["dead"]; r.Status != "failed" || r.Attempts != 1 || r.ResponseStatus != nil {
		t.Fatalf("dead = %+v", r)
	}
	if r := by["gone"]; r.Status != "failed" || r.Attempts != 0 || r.Error != "subscription removed or paused" {
		t.Fatalf("gone = %+v", r)
	}
	// RetryFailed requeues failed rows only
	for _, id := range []string{"dead", "ok", "retry"} {
		if err := svc.repo.RetryFailed(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ = svc.repo.ListDeliveries(ctx, sub.ID, 10, 0)
	for _, r := range rows {
		by[r.ID] = r
	}
	if r := by["dead"]; r.Status != "pending" || r.MaxAttempts != 2 {
		t.Fatalf("dead after retry = %+v", r)
	}
	if r := by["ok"]; r.Status != "delivered" {
		t.Fatalf("delivered row was requeued: %+v", r)
	}
	if r := by["retry"]; r.Status != "pending" || r.MaxAttempts != 4 {
		t.Fatalf("pending row was touched: %+v", r)
	}
}

func TestListDeliveriesLimits(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload)
		SELECT gen_random_uuid()::text, $1, 'e', '{}' FROM generate_series(1, 260)`, sub.ID)
	if rows, _ := svc.repo.ListDeliveries(ctx, "", 0, 0); len(rows) != 50 {
		t.Fatalf("default limit gave %d", len(rows))
	}
	if rows, _ := svc.repo.ListDeliveries(ctx, "", 1000, 0); len(rows) != 200 {
		t.Fatalf("max limit gave %d", len(rows))
	}
	if rows, _ := svc.repo.ListDeliveries(ctx, "", 200, 250); len(rows) != 10 {
		t.Fatalf("offset gave %d", len(rows))
	}
}

func TestPruneAgeRulesAndPerSubscriptionCap(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	a, b := mkSub(t, svc, EventEntityCreated), mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status, created_at) VALUES
		('del-old', $1, 'e', '{}', 'delivered', now() - interval '31 days'),
		('del-new', $1, 'e', '{}', 'delivered', now() - interval '29 days'),
		('fail-mid', $1, 'e', '{}', 'failed', now() - interval '60 days'),
		('fail-old', $1, 'e', '{}', 'failed', now() - interval '91 days'),
		('pend-mid', $1, 'e', '{}', 'pending', now() - interval '60 days'),
		('pend-old', $1, 'e', '{}', 'pending', now() - interval '91 days')`, a.ID)
	// 1,005 finished rows for b (recent), plus a pending one that the cap must not count
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status, created_at)
		SELECT 'b' || g, $1, 'e', '{}', 'delivered', now() - make_interval(secs => g) FROM generate_series(1, 1005) g`, b.ID)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status, created_at)
		VALUES ('b-pending', $1, 'e', '{}', 'pending', now() - interval '1 day')`, b.ID)
	if err := svc.repo.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	left := func(id string) bool {
		return count(t, svc, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE id=$1`, id) == 1
	}
	for id, want := range map[string]bool{"del-old": false, "del-new": true, "fail-mid": true, "fail-old": false, "pend-mid": true, "pend-old": false, "b-pending": true, "b1": true, "b1000": true, "b1001": false, "b1005": false} {
		if left(id) != want {
			t.Errorf("row %s present = %v, want %v", id, !want, want)
		}
	}
	if c := count(t, svc, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE subscription_id=$1 AND status<>'pending'`, b.ID); c != LogPerSubscription {
		t.Fatalf("b keeps %d finished rows, want %d", c, LogPerSubscription)
	}
}

func TestGetDelivery(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	if _, err := svc.repo.Enqueue(ctx, EventEntityCreated, map[string]string{"k": "v"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.repo.ListDeliveries(ctx, sub.ID, 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	got, err := svc.repo.GetDelivery(ctx, rows[0].ID)
	if err != nil || got.SubscriptionID != sub.ID || got.Status != "pending" || got.Event != EventEntityCreated {
		t.Fatalf("GetDelivery = %+v, %v", got, err)
	}
	if n, err := svc.repo.CountDeliveries(ctx, sub.ID); err != nil || n != 1 {
		t.Errorf("CountDeliveries = %d, %v", n, err)
	}
	if n, err := svc.repo.CountDeliveries(ctx, "other"); err != nil || n != 0 {
		t.Errorf("CountDeliveries(other) = %d, %v", n, err)
	}
	if _, err := svc.repo.GetDelivery(ctx, "missing"); err != ErrNotFound {
		t.Errorf("missing delivery: %v", err)
	}
}

func TestServiceRetryDeliveryOnlyRequeuesFailedRows(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status, attempts, max_attempts)
		VALUES ('bad', $1, 'e', '{}', 'failed', 4, 4), ('good', $1, 'e', '{}', 'delivered', 1, 4), ('wait', $1, 'e', '{}', 'pending', 0, 4)`, sub.ID)
	if ok, err := svc.RetryDelivery(ctx, "bad"); err != nil || !ok {
		t.Fatalf("retry failed row = %v, %v", ok, err)
	}
	if r, _ := svc.repo.GetDelivery(ctx, "bad"); r.Status != "pending" || r.MaxAttempts != 5 || r.Attempts != 4 {
		t.Fatalf("bad after retry = %+v", r)
	}
	for _, id := range []string{"good", "wait"} {
		if ok, err := svc.RetryDelivery(ctx, id); err != nil || ok {
			t.Errorf("retry %s = %v, %v; want false, nil", id, ok, err)
		}
	}
	if r, _ := svc.repo.GetDelivery(ctx, "good"); r.Status != "delivered" || r.MaxAttempts != 4 {
		t.Errorf("delivered row touched: %+v", r)
	}
	if _, err := svc.RetryDelivery(ctx, "nope"); err != ErrNotFound {
		t.Errorf("unknown id: %v", err)
	}
}

func TestCountFailedSinceCountsOnlyRecentFailures(t *testing.T) {
	svc, _ := testService(t)
	sub := mkSub(t, svc, EventEntityCreated)
	exec(t, svc, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, status, created_at) VALUES
		('f-new', $1, 'e', '{}', 'failed', now() - interval '1 day'),
		('f-new2', $1, 'e', '{}', 'failed', now() - interval '6 days'),
		('f-old', $1, 'e', '{}', 'failed', now() - interval '8 days'),
		('ok-new', $1, 'e', '{}', 'delivered', now() - interval '1 day'),
		('wait-new', $1, 'e', '{}', 'pending', now() - interval '1 day')`, sub.ID)
	n, err := svc.CountFailedSince(context.Background(), time.Now().AddDate(0, 0, -7))
	if err != nil || n != 2 {
		t.Fatalf("CountFailedSince = %d, %v; want 2", n, err)
	}
}
