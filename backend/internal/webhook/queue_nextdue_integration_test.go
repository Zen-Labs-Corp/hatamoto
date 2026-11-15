//go:build integration

package webhook

import (
	"context"
	"testing"
	"time"
)

func TestNextDue(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, ok := svc.repo.NextDue(ctx); ok {
		t.Fatal("an empty queue has nothing due")
	}

	sub := mkSub(t, svc, EventEntityCreated)
	if _, err := svc.repo.Enqueue(ctx, EventEntityCreated, map[string]string{"k": "v"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	exec(t, svc, `UPDATE hatamoto_webhook_deliveries SET next_attempt_at = $1`, later)
	got, ok := svc.repo.NextDue(ctx)
	if !ok || !got.Equal(later) {
		t.Fatalf("NextDue = %v, %v; want %v", got, ok, later)
	}

	// A leased delivery is due when its lease ends.
	lease := time.Now().Add(time.Hour).Truncate(time.Second)
	exec(t, svc, `UPDATE hatamoto_webhook_deliveries SET leased_until = $1 WHERE subscription_id = $2`, lease, sub.ID)
	if got, ok := svc.repo.NextDue(ctx); !ok || !got.Equal(lease) {
		t.Fatalf("leased NextDue = %v, %v; want %v", got, ok, lease)
	}

	// Settled deliveries do not count.
	exec(t, svc, `UPDATE hatamoto_webhook_deliveries SET status = 'failed'`)
	if _, ok := svc.repo.NextDue(ctx); ok {
		t.Fatal("a failed delivery is not due")
	}
}
