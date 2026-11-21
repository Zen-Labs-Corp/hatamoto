package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/testutil"
	"hatamoto.dev/core/internal/user"
)

// countingUsers is moderatorUsers that counts List calls.
type countingUsers struct {
	moderatorUsers
	lists int
}

func (r *countingUsers) List(ctx context.Context, limit, offset int) ([]*user.User, error) {
	r.lists++
	return r.moderatorUsers.List(ctx, limit, offset)
}

// recipientUsers builds n accounts, every third one an owner.
func recipientUsers(n int) []*user.User {
	var out []*user.User
	for i := range n {
		role := user.RoleEditor
		if i%3 == 0 {
			role = user.RoleOwner
		}
		out = append(out, &user.User{ID: uuid.New(), Email: fmt.Sprintf("u%d@example.test", i), Role: role})
	}
	return out
}

func TestRecipients_OwnerEmailsPagesOnceAndAsksPerUser(t *testing.T) {
	users := recipientUsers(450)
	repo := &countingUsers{moderatorUsers: moderatorUsers{all: users}}
	s := &Server{userRepo: repo, logger: quietLogger()}
	rec := &authztest.Recorder{}
	authztest.Use(t, rec)

	got, err := s.ownerEmails(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 150, len(got), "owners")
	testutil.Equal(t, 3, repo.lists, "List calls")
	testutil.Equal(t, 450, len(rec.Calls()), "one authz question per user")
	for _, c := range rec.Calls() {
		testutil.Equal(t, authz.ActionSiteReceiveOwnerMail, c.Action)
	}
}

func TestRecipients_ErrorSkipsThatUser(t *testing.T) {
	users := recipientUsers(6)
	s := &Server{userRepo: moderatorUsers{all: users}, logger: quietLogger()}
	bad := users[0].ID.String()
	authztest.Use(t, authztest.Func(func(ctx context.Context, sub authz.Subject, a authz.Action, o authz.Object) (bool, error) {
		if sub.UserID == bad {
			return false, errors.New("boom")
		}
		return authz.Default().Can(ctx, sub, a, o)
	}))
	got, err := s.ownerEmails(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, []string{users[3].Email}, got, "the other owner stays")
}

func TestRecipients_DenyAllIsEmpty(t *testing.T) {
	s := &Server{userRepo: moderatorUsers{all: recipientUsers(5)}, logger: quietLogger()}
	authztest.Use(t, authztest.DenyAll)
	emails, err := s.ownerEmails(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 0, len(emails))
	mods, err := s.commentModerators(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 0, len(mods))
	owners, err := s.commentOwners(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 0, len(owners))
}

func TestRecipients_CommentOwnersAreModeratorsThatReceiveOwnerMail(t *testing.T) {
	s := &Server{userRepo: moderatorUsers{all: recipientUsers(6)}, logger: quietLogger()}
	mods, err := s.commentModerators(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 6, len(mods))
	owners, err := s.commentOwners(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, 2, len(owners))
}
