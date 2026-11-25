package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

type fakeIdentity struct {
	err   error
	calls int
}

func (f *fakeIdentity) Ensure(_ context.Context, plugin, account string) (string, error) {
	f.calls++
	return plugin + ":" + account, f.err
}

func (f *fakeIdentity) Revoke(_ context.Context, _, account string) (bool, error) {
	f.calls++
	return account == "u1", f.err
}

func newIdentityRig(t *testing.T, ident CommentIdentity) *capaRig {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	svc := entity.NewService(&memSchemas{}, &memEntities{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rt.Configure(Config{Services: Services{Entities: svc, EntityWrites: svc, CommentIdentity: ident}, Scheduler: &fakeScheduler{}})
	r := &capaRig{rt: rt, engine: engine, svc: svc, logs: logs}
	r.load(t, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
		m.EntitySchema = "story"
	})
	return r
}

func identityCalls() map[string]any {
	a := map[string]any{"account": "u1"}
	return map[string]any{sdk.FnCommentsIdentityEnsure: a, sdk.FnCommentsIdentityRevoke: a}
}

func TestCommentsIdentityPassThrough(t *testing.T) {
	f := &fakeIdentity{}
	r := newIdentityRig(t, f)
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
	for i := 0; i < 2; i++ {
		if o := r.do(t, sdk.FnCommentsIdentityEnsure, map[string]any{"account": "u1"}); o.Code != "" || string(o.Result) != `{"commenter_key":"capa:u1"}` {
			t.Fatalf("ensure %d: %+v %s", i, o, o.Result)
		}
	}
	if o := r.do(t, sdk.FnCommentsIdentityRevoke, map[string]any{"account": "u1"}); o.Code != "" || string(o.Result) != `{"revoked":true}` {
		t.Fatalf("revoke: %+v %s", o, o.Result)
	}
	if o := r.do(t, sdk.FnCommentsIdentityRevoke, map[string]any{"account": "u2"}); string(o.Result) != `{"revoked":false}` {
		t.Fatalf("revoke unknown: %+v %s", o, o.Result)
	}
}

func TestCommentsIdentityDeniedUntilGranted(t *testing.T) {
	f := &fakeIdentity{}
	r := newIdentityRig(t, f)
	for fn, args := range identityCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrCapabilityDenied || o.Cap != sdk.PermCommentsIdentity {
			t.Errorf("%s: %+v", fn, o)
		}
	}
	if f.calls != 0 {
		t.Errorf("service called %d times without a grant", f.calls)
	}
}

func TestCommentsIdentityInvalidAccount(t *testing.T) {
	f := &fakeIdentity{}
	r := newIdentityRig(t, f)
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
	for fn := range identityCalls() {
		for _, acct := range []string{"", strings.Repeat("a", 129)} {
			if o := r.do(t, fn, map[string]any{"account": acct}); o.Code != sdk.ErrInvalidArgument {
				t.Errorf("%s %d bytes: %+v, want invalid_argument", fn, len(acct), o)
			}
		}
	}
	if f.calls != 0 {
		t.Errorf("service called %d times for invalid args", f.calls)
	}
}

func TestCommentsIdentityUnavailable(t *testing.T) {
	r := newIdentityRig(t, nil)
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
	for fn, args := range identityCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrUnavailable {
			t.Errorf("nil service %s: %+v, want unavailable", fn, o)
		}
	}
	r = newIdentityRig(t, &fakeIdentity{err: ErrCommentIdentityUnavailable})
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
	for fn, args := range identityCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrUnavailable {
			t.Errorf("service error %s: %+v, want unavailable", fn, o)
		}
	}
}

func TestCommentsIdentityRateLimitSharedWithWrites(t *testing.T) {
	r := newIdentityRig(t, &fakeIdentity{})
	r.grant(t, sdk.PermCommentsRead, sdk.PermCommentsIdentity, sdk.PermEntitiesWriteOwn)
	args := map[string]any{"account": "u1"}
	for i := 1; i <= 20; i++ {
		if o := r.do(t, sdk.FnCommentsIdentityEnsure, args); o.Code != "" {
			t.Fatalf("call %d: %+v", i, o)
		}
	}
	if o := r.do(t, sdk.FnCommentsIdentityEnsure, args); o.Code != sdk.ErrRateLimited {
		t.Fatalf("21st call: %+v, want rate_limited", o)
	}
	if o := r.do(t, sdk.FnEntitiesDelete, map[string]any{"id": "00000000-0000-0000-0000-000000000001"}); o.Code != sdk.ErrRateLimited {
		t.Fatalf("entities write after the burst: %+v, want rate_limited", o)
	}
}
