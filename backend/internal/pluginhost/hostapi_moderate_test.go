package pluginhost

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/sdk"
)

type fakeModerator struct {
	err   error
	calls int
}

func (f *fakeModerator) MarkDead(_ context.Context, _, _ string, ids []uuid.UUID) (int, error) {
	f.calls++
	return len(ids), f.err
}

func (f *fakeModerator) Restore(ctx context.Context, p, s string, ids []uuid.UUID) (int, error) {
	return f.MarkDead(ctx, p, s, ids)
}

func (f *fakeModerator) SetRank(_ context.Context, _, _ string, items []RankItem) (int, error) {
	f.calls++
	return len(items), f.err
}

func (f *fakeModerator) SetViewer(context.Context, string, string, bool) error {
	f.calls++
	return f.err
}

type auditRec struct {
	mu    sync.Mutex
	calls []string
}

func (a *auditRec) record(_ context.Context, action, target string, _ map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, action+" "+target)
}

func newModerateRig(t *testing.T, mod CommentModerator, audit func(context.Context, string, string, map[string]any)) *capaRig {
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
	rt.Configure(Config{Services: Services{Entities: svc, CommentModerator: mod, Audit: audit}, Scheduler: &fakeScheduler{}})
	r := &capaRig{rt: rt, engine: engine, svc: svc, logs: logs}
	r.load(t, func(m *sdk.Manifest) {
		m.Permissions = append(m.Permissions, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
		m.EntitySchema = "story"
	})
	return r
}

func moderateCalls() map[string]any {
	id := uuid.NewString()
	return map[string]any{
		sdk.FnCommentsMarkDead:  map[string]any{"ids": []string{id}},
		sdk.FnCommentsRestore:   map[string]any{"ids": []string{id}},
		sdk.FnCommentsSetRank:   map[string]any{"items": []map[string]any{{"id": id, "rank": 1.5}}},
		sdk.FnCommentsSetViewer: map[string]any{"commenter_key": "k", "show_dead": true},
	}
}

func TestCommentsModerateDeniedUntilGranted(t *testing.T) {
	r := newModerateRig(t, &fakeModerator{}, nil)
	for fn, args := range moderateCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrCapabilityDenied || o.Cap != sdk.PermCommentsModerate {
			t.Errorf("%s: %+v", fn, o)
		}
	}
}

func TestCommentsModerateNotOwnedAndUnavailable(t *testing.T) {
	r := newModerateRig(t, &fakeModerator{err: ErrCommentNotOwned}, nil)
	r.grant(t, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	for fn, args := range moderateCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrNotOwned {
			t.Errorf("%s: %+v, want not_owned", fn, o)
		}
	}
	r = newModerateRig(t, nil, nil)
	r.grant(t, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	for fn, args := range moderateCalls() {
		if o := r.do(t, fn, args); o.Code != sdk.ErrUnavailable {
			t.Errorf("%s: %+v, want unavailable", fn, o)
		}
	}
}

func TestCommentsModerateRateLimitedAndAudited(t *testing.T) {
	mod, aud := &fakeModerator{}, &auditRec{}
	r := newModerateRig(t, mod, aud.record)
	r.grant(t, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	args := moderateCalls()[sdk.FnCommentsMarkDead]
	o := r.do(t, sdk.FnCommentsMarkDead, args)
	if o.Code != "" || string(o.Result) != `{"changed":1}` {
		t.Fatalf("first call: %+v %s", o, o.Result)
	}
	if len(aud.calls) != 1 || aud.calls[0] != "plugin.comment.moderate plugin:capa" {
		t.Fatalf("audit: %v", aud.calls)
	}
	for i := 2; i <= 20; i++ {
		if o := r.do(t, sdk.FnCommentsMarkDead, args); o.Code != "" {
			t.Fatalf("call %d: %+v", i, o)
		}
	}
	if o := r.do(t, sdk.FnCommentsMarkDead, args); o.Code != sdk.ErrRateLimited {
		t.Fatalf("21st call: %+v, want rate_limited", o)
	}
	if len(aud.calls) != 20 {
		t.Errorf("audit calls %d, want 20 (limited call not audited)", len(aud.calls))
	}
}

func TestCommentsModerateInvalidArgs(t *testing.T) {
	mod := &fakeModerator{}
	r := newModerateRig(t, mod, nil)
	r.grant(t, sdk.PermCommentsModerate, sdk.PermEntitiesWriteOwn)
	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = uuid.NewString()
		}
		return out
	}
	items := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"id": uuid.NewString(), "rank": 1.0}
		}
		return out
	}
	dup := uuid.NewString()
	cases := []struct {
		fn   string
		args any
	}{
		{sdk.FnCommentsMarkDead, map[string]any{}},
		{sdk.FnCommentsMarkDead, map[string]any{"ids": ids(101)}},
		{sdk.FnCommentsRestore, map[string]any{"ids": []string{dup, dup}}},
		{sdk.FnCommentsSetRank, map[string]any{}},
		{sdk.FnCommentsSetRank, map[string]any{"items": items(201)}},
		{sdk.FnCommentsSetRank, map[string]any{"items": []map[string]any{{"id": dup, "rank": 1}, {"id": dup, "rank": 2}}}},
		{sdk.FnCommentsSetViewer, map[string]any{}},
	}
	for _, c := range cases {
		if o := r.do(t, c.fn, c.args); o.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s %v: %+v, want invalid_argument", c.fn, c.args, o)
		}
	}
	// NaN cannot travel as JSON; the guard is exercised on the function directly.
	a := NewAPI(Services{CommentModerator: mod}, nil, nil, nil)
	raw := []byte(`{"items":[{"id":"` + uuid.NewString() + `","rank":1e999}]}`)
	if _, err := fnCommentsSetRank(context.Background(), a, &callEnv{plugin: "capa", manifest: sdk.Manifest{EntitySchema: "story"}}, raw); err == nil {
		t.Error("non-finite rank accepted")
	}
	if mod.calls != 0 {
		t.Errorf("moderator called %d times for invalid args", mod.calls)
	}
	if o := r.do(t, sdk.FnCommentsSetViewer, moderateCalls()[sdk.FnCommentsSetViewer]); o.Code != "" || string(o.Result) != "{}" {
		t.Errorf("set_viewer: %+v %s", o, o.Result)
	}
}
