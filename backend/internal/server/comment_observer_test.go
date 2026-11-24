package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
)

type observerRig struct {
	o      commentCacheObserver
	drops  []uuid.UUID
	alls   int
	forgot []uuid.UUID
}

func newObserverRig() *observerRig {
	r := &observerRig{}
	r.o = commentCacheObserver{
		drop:   func(_ context.Context, id uuid.UUID) { r.drops = append(r.drops, id) },
		all:    func(context.Context) { r.alls++ },
		forget: func(id uuid.UUID) { r.forgot = append(r.forgot, id) },
	}
	return r
}

func TestCommentObserverDropsOnlyThePublicThreadChanges(t *testing.T) {
	r := newObserverRig()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	person := uuid.New()
	approved := comment.Comment{EntityID: a, Status: comment.StatusApproved, CommenterID: &person}
	pending := comment.Comment{EntityID: a, Status: comment.StatusPending}

	r.o.OnApproved(ctx, approved)
	if len(r.drops) != 1 || r.drops[0] != a || r.alls != 0 {
		t.Fatalf("OnApproved: drops %v, all %d", r.drops, r.alls)
	}
	if len(r.forgot) != 1 || r.forgot[0] != person {
		t.Fatalf("the commenter's own-items answer was not forgotten: %v", r.forgot)
	}

	r.drops = nil
	r.o.OnCreated(ctx, pending)
	r.o.OnConfirmed(ctx, pending)
	r.o.OnUpdated(ctx, pending, pending)
	if len(r.drops) != 0 {
		t.Fatalf("pending-only changes dropped pages: %v", r.drops)
	}

	r.o.OnUpdated(ctx, approved, pending) // an unapproval
	r.o.OnUpdated(ctx, pending, approved)
	r.o.OnUpdated(ctx, approved, approved) // an edit of a public comment
	if len(r.drops) != 3 {
		t.Fatalf("OnUpdated drops = %d, want 3", len(r.drops))
	}

	r.drops = nil
	// Never public (pending or spam from the start): nothing visible changed.
	r.o.OnSpam(ctx, comment.Comment{EntityID: b})
	r.o.OnDeleted(ctx, comment.Comment{EntityID: b})
	if len(r.drops) != 0 {
		t.Fatalf("a never-public comment dropped pages: %v", r.drops)
	}
	was := time.Now()
	r.o.OnSpam(ctx, comment.Comment{EntityID: b, ApprovedAt: &was})
	r.o.OnDeleted(ctx, comment.Comment{EntityID: b, ApprovedAt: &was})
	if len(r.drops) != 2 || r.drops[0] != b || r.alls != 0 {
		t.Fatalf("OnSpam/OnDeleted: drops %v, all %d", r.drops, r.alls)
	}
}

func TestCommentObserverSettingsChangeDropsEverything(t *testing.T) {
	r := newObserverRig()
	r.o.OnSettingsChanged(context.Background(), comment.Settings{Enabled: true})
	if r.alls != 1 || len(r.drops) != 0 {
		t.Fatalf("all %d drops %v", r.alls, r.drops)
	}
	r.o.OnSettingsChanged(context.Background(), comment.Settings{})
	if r.alls != 2 {
		t.Fatalf("all %d", r.alls)
	}
}

func TestCommentObserverModeChange(t *testing.T) {
	r := newObserverRig()
	id := uuid.New()
	r.o.OnModeChanged(context.Background(), id)
	if len(r.drops) != 1 || r.drops[0] != id || r.alls != 0 {
		t.Fatalf("entity mode: drops %v, all %d", r.drops, r.alls)
	}
	r.o.OnModeChanged(context.Background(), uuid.Nil) // a type default
	if len(r.drops) != 1 || r.alls != 1 {
		t.Fatalf("type mode: drops %v, all %d", r.drops, r.alls)
	}
}

func TestCommentObserverDropIsNilSafeWithoutACache(t *testing.T) {
	(&Server{}).dropRenderedEntity(context.Background(), uuid.New())
}

// A confirmed comment shows at once: the confirm path forgets the commenter's
// cached "has own items" answer.
func TestCommentConfirm_ForgetsTheOwnItemsAnswer(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	person := uuid.New()
	f.confirmed.CommenterID = person

	hasOwn := false
	b := newCommentBypass(
		func(string, time.Time) (identity.DeviceClaims, bool) {
			return identity.DeviceClaims{CommenterID: person}, true
		},
		func(context.Context, uuid.UUID) (bool, error) { return hasOwn, nil }, nil)
	r := httptest.NewRequest(http.MethodGet, "/post", nil)
	r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "v"})
	if b.applies(r) {
		t.Fatal("no own items yet")
	}
	hasOwn = true
	if b.applies(r) {
		t.Fatal("the 30 s cache should still say none")
	}

	req := confirmPost(map[string]string{"Sec-Fetch-Site": "same-origin"})
	req.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	(&Server{commentBypass: b}).commentConfirmHandler(f, lookup)(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if !b.applies(r) {
		t.Fatal("the confirm did not refresh the own-items answer")
	}
}

func TestCommentCacheObserverDeadAndBatch(t *testing.T) {
	r := newObserverRig()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	approved := comment.Comment{EntityID: a, Status: comment.StatusApproved}
	dead := comment.Comment{EntityID: a, Status: comment.StatusDead}

	r.o.OnUpdated(ctx, approved, dead) // hiding a body drops
	r.o.OnUpdated(ctx, dead, approved)
	if len(r.drops) != 2 {
		t.Fatalf("dead transitions dropped %d, want 2", len(r.drops))
	}

	r.drops = nil
	bctx := comment.WithBatch(ctx)
	r.o.OnUpdated(bctx, approved, dead)
	r.o.OnDeleted(bctx, comment.Comment{EntityID: a, Status: comment.StatusApproved, ApprovedAt: ptrTime(time.Now())})
	if len(r.drops) != 0 {
		t.Fatalf("per-comment batch events dropped: %v", r.drops)
	}
	r.o.OnBatch(ctx, []uuid.UUID{a, b})
	if len(r.drops) != 2 || r.drops[0] != a || r.drops[1] != b {
		t.Fatalf("OnBatch drops = %v, want one per entity", r.drops)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
