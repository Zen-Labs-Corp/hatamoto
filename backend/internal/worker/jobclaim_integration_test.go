//go:build integration

package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/database"
)

func claimTestID(t *testing.T) string {
	t.Helper()
	id := "test:claim:" + uuid.NewString()
	db := openStatusDB(t)
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_job_claims WHERE job_id = $1`, id)
	})
	return id
}

func TestClaimStoreOneWinner_Integration(t *testing.T) {
	ctx := context.Background()
	id := claimTestID(t)
	// Eight servers over two pools: the same holder may reclaim, so each
	// contender needs its own name.
	dbs := []*database.DB{openStatusDB(t), openStatusDB(t)}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		s := NewClaimStore(dbs[i%2], fmt.Sprintf("server-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			won, err := s.Claim(ctx, id, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if won {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners = %d, want 1", wins.Load())
	}
}

func TestClaimStoreSameHolderReclaims_Integration(t *testing.T) {
	ctx := context.Background()
	id := claimTestID(t)
	a := NewClaimStore(openStatusDB(t), "a")
	b := NewClaimStore(openStatusDB(t), "b")
	for i := range 2 {
		if won, err := a.Claim(ctx, id, time.Minute); err != nil || !won {
			t.Fatalf("a claim %d = %v, %v", i, won, err)
		}
	}
	if won, err := b.Claim(ctx, id, time.Minute); err != nil || won {
		t.Fatalf("b claim = %v, %v, want false", won, err)
	}
}

func TestClaimStoreLeaseExpires_Integration(t *testing.T) {
	ctx := context.Background()
	id := claimTestID(t)
	a := NewClaimStore(openStatusDB(t), "a")
	b := NewClaimStore(openStatusDB(t), "b")
	if won, err := a.Claim(ctx, id, 100*time.Millisecond); err != nil || !won {
		t.Fatalf("a claim = %v, %v", won, err)
	}
	time.Sleep(300 * time.Millisecond)
	if won, err := b.Claim(ctx, id, time.Minute); err != nil || !won {
		t.Fatalf("b claim after expiry = %v, %v, want true", won, err)
	}
}
