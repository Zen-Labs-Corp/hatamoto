//go:build integration

package server

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
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

// Erase announces comment.deleted for every comment that went out with
// comment.created (approved and pending), not for a draft or filter spam.
func TestEraseAnnouncesPendingButNotDraftOrFilterSpam(t *testing.T) {
	r := newCascadeRig(t)
	id := r.entityWithComments("erase-events") // approved, pending, draft
	if _, err := r.s.comments.Store(context.Background(), comment.Draft{
		EntityID: id, Email: "a@example.test", AuthorName: "Ann", BodyRaw: "hello", BodyHTML: r.res.HTML,
		BodyHash: r.res.BodyHash, SpamVerdict: "spam", Status: comment.StatusSpam, Locale: "en",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := r.s.comments.EraseByEmail(context.Background(), "a@example.test")
	if err != nil || res.Deleted != 4 {
		t.Fatalf("erase = %+v, %v; want 4 deleted", res, err)
	}
	ev := r.events()
	if len(ev) != 2 {
		t.Fatalf("comment.deleted events = %d, want 2 (approved and pending): %+v", len(ev), ev)
	}
	statuses := []string{ev[0].Status, ev[1].Status}
	slices.Sort(statuses)
	if statuses[0] != "approved" || statuses[1] != "pending" {
		t.Fatalf("event statuses = %v, want approved and pending", statuses)
	}
}
