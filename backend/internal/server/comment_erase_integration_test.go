//go:build integration

package server

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// eraseRig adds an uncoalesced cache observer to the cascade rig and records its drops.
func eraseRig(t *testing.T) (*cascadeRig, func() []uuid.UUID, func()) {
	r := newCascadeRig(t)
	var mu sync.Mutex
	var drops []uuid.UUID
	r.s.comments.Register(commentCacheObserver{
		drop: func(_ context.Context, id uuid.UUID) {
			mu.Lock()
			defer mu.Unlock()
			drops = append(drops, id)
		},
		all: func(context.Context) {},
	})
	get := func() []uuid.UUID {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(drops)
	}
	reset := func() {
		mu.Lock()
		defer mu.Unlock()
		drops = nil
	}
	return r, get, reset
}

// Erasing a commenter deletes their comments in a batch; the cached pages of
// their entities must still be dropped (one OnBatch after the announcements).
func TestEraseDropsCachedPagesOfDeletedComments(t *testing.T) {
	r, drops, reset := eraseRig(t)
	id := r.entityWithComments("erase-cache")
	reset()
	res, err := r.s.comments.EraseByEmail(context.Background(), "a@example.test")
	if err != nil || res.Deleted != 3 {
		t.Fatalf("erase = %+v, %v; want 3 deleted", res, err)
	}
	if got := drops(); len(got) != 1 || got[0] != id {
		t.Fatalf("page drops = %v, want one for the entity %s", got, id)
	}
}
