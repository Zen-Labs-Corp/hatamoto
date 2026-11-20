package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/user"
)

func TestCommentNotifyObserverRoutes(t *testing.T) {
	var mod, rep int
	o := commentNotifyObserver{
		moderation: func(context.Context, comment.Comment) { mod++ },
		reply:      func(context.Context, comment.Comment) { rep++ },
	}
	ctx := context.Background()
	with := func(s comment.Status) comment.Comment { return comment.Comment{Status: s} }
	parent := uuid.New()

	o.OnCreated(ctx, with(comment.StatusDraft))
	o.OnConfirmed(ctx, with(comment.StatusApproved))
	o.OnUpdated(ctx, with(comment.StatusPending), with(comment.StatusPending))
	o.OnApproved(ctx, with(comment.StatusApproved))
	if mod != 0 || rep != 0 {
		t.Fatalf("nothing should queue: moderation %d, reply %d", mod, rep)
	}

	o.OnCreated(ctx, with(comment.StatusPending))
	o.OnConfirmed(ctx, with(comment.StatusPending))
	o.OnUpdated(ctx, with(comment.StatusApproved), with(comment.StatusPending))
	if mod != 3 {
		t.Errorf("moderation = %d, want 3", mod)
	}
	o.OnApproved(ctx, comment.Comment{Status: comment.StatusApproved, ParentID: &parent})
	if rep != 1 {
		t.Errorf("reply = %d, want 1", rep)
	}
}

type moderatorUsers struct {
	user.Repository
	all []*user.User
}

func (r moderatorUsers) List(_ context.Context, limit, offset int) ([]*user.User, error) {
	if offset >= len(r.all) {
		return nil, nil
	}
	return r.all[offset:min(offset+limit, len(r.all))], nil
}

func TestCommentModerators(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	users := []*user.User{
		{ID: uuid.New(), Email: "owner@example.test", Role: user.RoleOwner},
		{ID: uuid.New(), Email: "editor@example.test", Role: user.RoleEditor},
		{ID: uuid.New(), Email: "ended@example.test", Role: user.RoleEditor, AccessEndsAt: &past},
		{ID: uuid.New(), Email: "later@example.test", Role: user.RoleEditor, AccessEndsAt: &future},
		{ID: uuid.New(), Email: "", Role: user.RoleOwner},
	}
	s := &Server{userRepo: moderatorUsers{all: users}}
	got, err := s.commentModerators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Email != "owner@example.test" || got[1].Email != "editor@example.test" || got[2].Email != "later@example.test" {
		t.Errorf("moderators = %v", got)
	}

	var many []*user.User
	for i := range 450 {
		many = append(many, &user.User{ID: uuid.New(), Email: fmt.Sprintf("u%d@example.test", i), Role: user.RoleEditor})
	}
	s = &Server{userRepo: moderatorUsers{all: many}}
	if got, err := s.commentModerators(context.Background()); err != nil || len(got) != 450 {
		t.Errorf("paged moderators = %d, err %v; want 450", len(got), err)
	}
}

func TestSetupCommentNotifyNeedsItsParts(t *testing.T) {
	if (&Server{}).setupCommentNotify() {
		t.Error("setupCommentNotify scheduled without comments, mail, users or a scheduler")
	}
	// Queueing is not gated on the scheduler, only scheduling is.
	if (&Server{comments: &comment.Service{}, userRepo: moderatorUsers{}}).setupCommentNotify() {
		t.Error("setupCommentNotify scheduled without mail or a scheduler")
	}
}
