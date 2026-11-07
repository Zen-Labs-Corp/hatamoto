package server

import (
	"fmt"
	"testing"
	"time"
)

// A full table must not turn new visitors into "over the limit": one flood of
// distinct addresses would otherwise send everyone else's submissions to Spam.
func TestIPFreeLimiter_FullTableFailsOpen(t *testing.T) {
	t.Parallel()
	var l ipFreeLimiter
	now := time.Now()
	for i := 0; i < formRateMaxTracked; i++ {
		if l.over("f", fmt.Sprintf("b%d", i), 5, now) {
			t.Fatalf("bucket %d over the limit on its first send", i)
		}
	}
	if l.over("f", "newcomer", 5, now) {
		t.Fatal("a new visitor was counted as over the limit because the table is full")
	}
	if len(l.hits) > formRateMaxTracked {
		t.Fatalf("table grew to %d", len(l.hits))
	}
	// A tracked visitor is still limited.
	for i := 0; i < 5; i++ {
		l.over("f", "b0", 5, now)
	}
	if !l.over("f", "b0", 5, now) {
		t.Fatal("a tracked visitor past the limit was let through")
	}
	// Expired entries are swept so newcomers are tracked again.
	later := now.Add(formRateWindow + time.Minute)
	l.over("f", "fresh", 1, later)
	if !l.over("f", "fresh", 1, later) {
		t.Fatal("after the sweep a new visitor is not counted")
	}
}
