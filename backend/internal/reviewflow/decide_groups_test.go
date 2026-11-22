package reviewflow

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/reviewstate"
)

type groupFunc func(ctx context.Context, userID, role, group, entityID string) (bool, error)

func (g groupFunc) MayDecide(ctx context.Context, userID, role, group, entityID string) (bool, error) {
	return g(ctx, userID, role, group, entityID)
}

func answer(ok bool, err error) groupFunc {
	return func(context.Context, string, string, string, string) (bool, error) { return ok, err }
}

func subj(role string) authz.Subject { return authz.Subject{UserID: uuid.NewString(), Role: role} }

func groupsFixture(t *testing.T) {
	t.Helper()
	authztest.Use(t, authztest.Func(func(_ context.Context, s authz.Subject, a authz.Action, _ authz.Object) (bool, error) {
		return a == authz.ActionEntityReviewDecide && s.Role == "owner", nil
	}))
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
}

func TestMayDecideDefaultGroupNeedsOnlyTheRight(t *testing.T) {
	groupsFixture(t)
	ctx := context.Background()
	if err := mayDecide(ctx, subj("owner"), reviewstate.DefaultGroup, "e"); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if err := mayDecide(ctx, subj("contributor"), reviewstate.DefaultGroup, "e"); !errors.Is(err, ErrNotDecider) {
		t.Fatalf("contributor: %v, want ErrNotDecider", err)
	}
}

func TestMayDecideOtherGroupWithoutResolver(t *testing.T) {
	groupsFixture(t)
	if err := mayDecide(context.Background(), subj("owner"), "legal", "e"); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("err = %v, want ErrUnknownGroup", err)
	}
}

func TestMayDecideOtherGroupWithResolver(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	cases := []struct {
		name string
		role string
		res  groupFunc
		want error // nil = allowed
		also error
	}{
		{"yes without the right", "contributor", answer(true, nil), ErrNotDecider, nil},
		{"yes with the right", "owner", answer(true, nil), nil, nil},
		{"no", "owner", answer(false, nil), ErrNotDecider, nil},
		{"error", "owner", answer(false, boom), ErrNotDecider, reviewstate.ErrGroupResolver},
		{"panic", "owner", func(context.Context, string, string, string, string) (bool, error) { panic("boom") }, ErrNotDecider, reviewstate.ErrGroupResolver},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			groupsFixture(t)
			reviewstate.RegisterGroups(c.res)
			err := mayDecide(ctx, subj(c.role), "legal", "e")
			if c.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.want) || (c.also != nil && !errors.Is(err, c.also)) {
				t.Fatalf("err = %v, want %v and %v", err, c.want, c.also)
			}
		})
	}
}

func TestGroupAllows(t *testing.T) {
	f := &Flow{}
	user := authzctx.With(context.Background(), subj("owner"))
	key := authzctx.With(context.Background(), authz.Subject{UserID: "u", KeyID: "k", Role: "owner"})
	boom := errors.New("boom")
	panics := groupFunc(func(context.Context, string, string, string, string) (bool, error) { panic("boom") })

	t.Run("no resolver", func(t *testing.T) {
		groupsFixture(t)
		if !f.GroupAllows(context.Background(), "legal", "e") {
			t.Fatal("want true without a resolver")
		}
	})
	t.Run("default group and empty", func(t *testing.T) {
		groupsFixture(t)
		reviewstate.RegisterGroups(answer(false, nil))
		if !f.GroupAllows(user, reviewstate.DefaultGroup, "e") || !f.GroupAllows(user, "", "e") {
			t.Fatal("want true for the default group")
		}
	})
	for _, c := range []struct {
		name string
		ctx  context.Context
		res  groupFunc
		want bool
	}{
		{"yes", user, answer(true, nil), true},
		{"no", user, answer(false, nil), false},
		{"key", key, answer(true, nil), false},
		{"no subject", context.Background(), answer(true, nil), false},
		{"error", user, answer(true, boom), false},
		{"panic", user, panics, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			groupsFixture(t)
			reviewstate.RegisterGroups(c.res)
			if got := f.GroupAllows(c.ctx, "legal", "e"); got != c.want {
				t.Fatalf("GroupAllows = %v, want %v", got, c.want)
			}
		})
	}
}
