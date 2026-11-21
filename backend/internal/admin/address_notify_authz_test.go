package admin

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/user"
)

// listManager answers List from a fixed slice, then with err past its end.
type listManager struct {
	UserManager
	users []*user.User
	err   error
}

func (m *listManager) List(_ context.Context, limit, offset int) ([]*user.User, error) {
	if offset >= len(m.users) {
		return nil, m.err
	}
	return m.users[offset:min(offset+limit, len(m.users))], nil
}

func mailUser(role user.Role, addr string) *user.User {
	return &user.User{ID: uuid.New(), Role: role, Email: addr}
}

func TestOwnerEmailsAskAuthz(t *testing.T) {
	users := []*user.User{
		mailUser(user.RoleOwner, "a@example.test"),
		mailUser(user.RoleEditor, "e@example.test"),
		mailUser(user.RoleOwner, "b@example.test"),
	}
	a := &Admin{}
	a.deps.Manager = &listManager{users: users}

	got, err := a.ownerEmails(context.Background())
	if err != nil || !slices.Equal(got, []string{"a@example.test", "b@example.test"}) {
		t.Fatalf("got %v, %v", got, err)
	}

	authztest.Use(t, authztest.DenyAll)
	got, err = a.ownerEmails(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("deny-all: got %v, %v", got, err)
	}
}

func TestOwnerEmailsListError(t *testing.T) {
	boom := errors.New("boom")
	var users []*user.User
	for range 200 { // one full page, so the next list call is made and fails
		users = append(users, mailUser(user.RoleOwner, "o@example.test"))
	}
	a := &Admin{}
	a.deps.Manager = &listManager{users: users, err: boom}
	got, err := a.ownerEmails(context.Background())
	if !errors.Is(err, boom) || len(got) != 200 {
		t.Fatalf("got %d addresses, %v", len(got), err)
	}
}
