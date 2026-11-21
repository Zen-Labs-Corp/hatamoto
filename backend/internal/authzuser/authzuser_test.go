package authzuser

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authztest"
	"hatamoto.dev/core/internal/user"
)

func fakeUsers(n int) []*user.User {
	us := make([]*user.User, n)
	for i := range us {
		role := user.RoleEditor
		if i%3 == 0 {
			role = user.RoleOwner
		}
		us[i] = &user.User{ID: uuid.New(), Role: role, Email: "u" + uuid.NewString() + "@example.test"}
	}
	return us
}

func pager(us []*user.User, calls *int) ListFunc {
	return func(_ context.Context, limit, offset int) ([]*user.User, error) {
		*calls++
		if offset >= len(us) {
			return nil, nil
		}
		return us[offset:min(offset+limit, len(us))], nil
	}
}

func TestAllowedPagesAndFilters(t *testing.T) {
	us := fakeUsers(450)
	calls := 0
	got, err := Allowed(context.Background(), slog.Default(), pager(us, &calls), authz.ActionSettingsManage, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("list calls = %d, want 3", calls)
	}
	if len(got) != 150 {
		t.Fatalf("owners = %d, want 150", len(got))
	}
	for i, u := range got {
		if u.Role != user.RoleOwner || u != us[i*3] {
			t.Fatalf("row %d: %+v", i, u)
		}
	}
	if emails := Emails(got); len(emails) != 150 || emails[0] != got[0].Email {
		t.Errorf("Emails = %d", len(emails))
	}
}

func TestAllowedAsksOncePerKeptUser(t *testing.T) {
	rc := &authztest.Recorder{}
	authztest.Use(t, rc)
	us := fakeUsers(30)
	calls := 0
	keep := func(u *user.User) bool { return u.Role == user.RoleEditor }
	if _, err := Allowed(context.Background(), slog.Default(), pager(us, &calls), authz.ActionEntityExport, keep); err != nil {
		t.Fatal(err)
	}
	if n := len(rc.Calls()); n != 20 {
		t.Errorf("authz calls = %d, want 20", n)
	}
}

func TestAllowedSkipsUserOnAuthzError(t *testing.T) {
	us := fakeUsers(5)
	bad := us[2].ID.String()
	authztest.Use(t, authztest.Func(func(_ context.Context, s authz.Subject, _ authz.Action, _ authz.Object) (bool, error) {
		if s.UserID == bad {
			return false, errors.New("boom")
		}
		return true, nil
	}))
	calls := 0
	got, err := Allowed(context.Background(), slog.Default(), pager(us, &calls), authz.ActionEntityExport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d users, want 4", len(got))
	}
	for _, u := range got {
		if u.ID.String() == bad {
			t.Error("user with an authz error was kept")
		}
	}
}

func TestAllowedListErrorReturnsCollected(t *testing.T) {
	us := fakeUsers(250)
	boom := errors.New("db down")
	list := func(_ context.Context, limit, offset int) ([]*user.User, error) {
		if offset > 0 {
			return nil, boom
		}
		return us[:limit], nil
	}
	got, err := Allowed(context.Background(), slog.Default(), list, authz.ActionEntityExport, nil)
	if !errors.Is(err, boom) || len(got) != 200 {
		t.Errorf("got %d users, err %v", len(got), err)
	}
}

func TestCanUser(t *testing.T) {
	ctx := context.Background()
	owner := &user.User{ID: uuid.New(), Role: user.RoleOwner}
	editor := &user.User{ID: uuid.New(), Role: user.RoleEditor}
	if !Can(ctx, slog.Default(), owner, authz.ActionUserManage) || Can(ctx, slog.Default(), editor, authz.ActionUserManage) || Can(ctx, slog.Default(), nil, authz.ActionUserManage) {
		t.Error("default answers wrong")
	}
	authztest.Use(t, authztest.DenyAll)
	if Can(ctx, slog.Default(), owner, authz.ActionUserManage) {
		t.Error("owner allowed under DenyAll")
	}
}
