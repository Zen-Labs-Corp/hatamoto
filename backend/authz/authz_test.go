package authz_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/authz"
)

// fakeAuthorizer answers from allow, keyed by action + "/" + object ID.
type fakeAuthorizer struct {
	allow map[string]bool
	err   error
}

var _ authz.Authorizer = fakeAuthorizer{}

func (f fakeAuthorizer) Can(_ context.Context, _ authz.Subject, a authz.Action, o authz.Object) (bool, error) {
	return f.allow[string(a)+"/"+o.ID], f.err
}

type ptrAuthorizer struct{}

func (*ptrAuthorizer) Can(context.Context, authz.Subject, authz.Action, authz.Object) (bool, error) {
	return true, nil
}

func reset(t *testing.T) {
	t.Helper()
	authz.ResetForTest()
	t.Cleanup(authz.ResetForTest)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	fn()
}

var (
	owner  = authz.Subject{UserID: "u1", Role: "owner"}
	editor = authz.Subject{UserID: "u2", Role: "editor"}
)

func TestCanDefaultWhenNothingRegistered(t *testing.T) {
	reset(t)
	ctx := context.Background()
	if ok, err := authz.Can(ctx, editor, authz.ActionEntityUpdate, authz.Object{}); !ok || err != nil {
		t.Errorf("editor entity.update = (%v, %v)", ok, err)
	}
	if ok, err := authz.Can(ctx, editor, authz.ActionSettingsManage, authz.Object{}); ok || err != nil {
		t.Errorf("editor settings.manage = (%v, %v)", ok, err)
	}
}

func TestRegisteredNarrowsAndWidens(t *testing.T) {
	reset(t)
	ctx := context.Background()
	authz.Register(fakeAuthorizer{allow: map[string]bool{
		"entity.update/y":  true,
		"settings.manage/": true,
	}})
	// Narrow: the default would allow the owner on both objects.
	if ok, _ := authz.Can(ctx, owner, authz.ActionEntityUpdate, authz.Object{ID: "x"}); ok {
		t.Error("owner on x should be denied by the fake")
	}
	if ok, _ := authz.Can(ctx, owner, authz.ActionEntityUpdate, authz.Object{ID: "y"}); !ok {
		t.Error("owner on y should be allowed by the fake")
	}
	// Widen: the default denies an editor settings.manage.
	if ok, _ := authz.Can(ctx, editor, authz.ActionSettingsManage, authz.Object{}); !ok {
		t.Error("editor settings.manage should be allowed by the fake")
	}
}

func TestRegisteredReceivesCustomAction(t *testing.T) {
	reset(t)
	authz.Register(fakeAuthorizer{allow: map[string]bool{"module.thing/1": true}})
	ok, err := authz.Can(context.Background(), editor, "module.thing", authz.Object{ID: "1"})
	if !ok || err != nil {
		t.Errorf("custom action = (%v, %v)", ok, err)
	}
}

func TestRegisteredErrorFailsClosed(t *testing.T) {
	reset(t)
	boom := errors.New("boom")
	authz.Register(fakeAuthorizer{allow: map[string]bool{"entity.read/": true}, err: boom})
	ok, err := authz.Can(context.Background(), owner, authz.ActionEntityRead, authz.Object{})
	if ok || !errors.Is(err, boom) {
		t.Errorf("got (%v, %v), want false and the fake's error", ok, err)
	}
}

type delegating struct{}

func (delegating) Can(ctx context.Context, s authz.Subject, a authz.Action, o authz.Object) (bool, error) {
	if a == authz.ActionEntityPublish && o.Schema == "page" {
		return false, nil
	}
	return authz.Default().Can(ctx, s, a, o)
}

func TestRegisteredDelegatesToDefault(t *testing.T) {
	reset(t)
	authz.Register(delegating{})
	ctx := context.Background()
	if ok, _ := authz.Can(ctx, owner, authz.ActionEntityPublish, authz.Object{Schema: "page"}); ok {
		t.Error("publishing a page should be narrowed")
	}
	if ok, _ := authz.Can(ctx, editor, authz.ActionEntityPublish, authz.Object{Schema: "post"}); !ok {
		t.Error("other schemas should follow the default")
	}
}

func TestRegisterPanics(t *testing.T) {
	reset(t)
	mustPanic(t, func() { authz.Register(nil) })
	mustPanic(t, func() { authz.Register((*ptrAuthorizer)(nil)) })
	authz.Register(fakeAuthorizer{})
	mustPanic(t, func() { authz.Register(fakeAuthorizer{}) })
}

func TestRegisterAfterFreezePanics(t *testing.T) {
	reset(t)
	authz.FreezeForTest()
	mustPanic(t, func() { authz.Register(fakeAuthorizer{}) })
}
