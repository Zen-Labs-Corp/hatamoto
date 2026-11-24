package pluginhost

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

type commentRig struct {
	rt     *Runtime
	engine *hooks.Engine
	logs   *lockedBuf
}

func newCommentRig(t *testing.T) *commentRig {
	t.Helper()
	logs := &lockedBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	engine := hooks.New(log)
	rt, err := NewRuntime(log, engine, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(context.Background()) })
	return &commentRig{rt: rt, engine: engine, logs: logs}
}

// load loads the commentcheck plugin in the given mode; with grant it also
// holds comments.read.
func (r *commentRig) load(t *testing.T, mode string, grant bool) {
	t.Helper()
	m := sdk.Manifest{
		Name: "commentcheck", Version: "0.1.0", SDK: "^0.25",
		Permissions: []string{sdk.PermCommentsRead, sdk.PermKV, sdk.PermClientKey},
		Config:      map[string]string{"mode": mode},
	}
	m.Limits.HookTimeoutsMS = map[string]uint64{sdk.CommentCheckF.Name: 200}
	if st := r.rt.Load(context.Background(), m, plugintest.Build(t, "commentcheck")); !st.Loaded {
		t.Fatalf("commentcheck not loaded: %s", st.Error)
	}
	if grant {
		r.grant(t, sdk.PermCommentsRead)
	}
}

func (r *commentRig) grant(t *testing.T, perms ...string) {
	t.Helper()
	if _, err := r.rt.Grant(context.Background(), "commentcheck", perms); err != nil {
		t.Fatal(err)
	}
}

// check runs the chain the way commentsubmit.HookChecker does.
func (r *commentRig) check(ctx context.Context) (sdk.CommentCheck, error) {
	out, err := r.engine.ApplyClosed(ctx, sdk.CommentCheckF.Name,
		sdk.CommentCheck{Body: "hello", LinkDomains: []string{"a.test"}, BodyHash: "h"})
	if err != nil {
		return sdk.CommentCheck{}, err
	}
	return out.(sdk.CommentCheck), nil
}

func TestCommentCheckAdds(t *testing.T) {
	r := newCommentRig(t)
	r.load(t, "add", true)
	got, err := r.check(context.Background())
	if err != nil || got.Score != 40 || !slices.Equal(got.Reasons, []string{"commentcheck: test-add"}) {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestCommentCheckClamps(t *testing.T) {
	cases := []struct {
		mode string
		want func(t *testing.T, got sdk.CommentCheck)
	}{
		{"lower", func(t *testing.T, got sdk.CommentCheck) {
			if got.Score != 0 {
				t.Errorf("score %d, want 0", got.Score)
			}
		}},
		{"huge", func(t *testing.T, got sdk.CommentCheck) {
			if got.Score != sdk.CommentCheckMaxScore {
				t.Errorf("score %d, want %d", got.Score, sdk.CommentCheckMaxScore)
			}
		}},
		{"many", func(t *testing.T, got sdk.CommentCheck) {
			if len(got.Reasons) != commentCheckMaxReasons {
				t.Errorf("%d reasons, want %d", len(got.Reasons), commentCheckMaxReasons)
			}
			for _, s := range got.Reasons {
				if len(s) > commentCheckMaxReason || !utf8.ValidString(s) || !strings.HasPrefix(s, "commentcheck: ") {
					t.Errorf("bad reason %q", s)
				}
			}
		}},
		{"body", func(t *testing.T, got sdk.CommentCheck) {
			if got.Score != 5 || got.Body != "hello" || got.BodyHash != "h" {
				t.Errorf("got %+v: only the score may change", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			r := newCommentRig(t)
			r.load(t, tc.mode, true)
			got, err := r.check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			tc.want(t, got)
		})
	}
}

func TestCommentCheckHolds(t *testing.T) {
	for _, mode := range []string{"trap", "sleep", "reject"} {
		t.Run(mode, func(t *testing.T) {
			r := newCommentRig(t)
			r.load(t, mode, true)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, err := r.check(ctx)
			if err == nil {
				t.Fatal("the chain must fail")
			}
			if mode == "trap" && !errors.Is(err, hooks.ErrFailClosed) {
				t.Errorf("trap: %v is not ErrFailClosed", err)
			}
			if mode == "reject" {
				var rej *sdk.Reject
				if !errors.As(err, &rej) {
					t.Errorf("reject: %v is not a *sdk.Reject", err)
				}
			}
		})
	}
	t.Run("breaker open", func(t *testing.T) {
		r := newCommentRig(t)
		r.load(t, "trap", true)
		for range 3 {
			if _, err := r.check(context.Background()); err == nil {
				t.Fatal("trap must fail")
			}
		}
		_, err := r.check(context.Background())
		if err == nil || !errors.Is(err, hooks.ErrFailClosed) {
			t.Fatalf("open breaker: %v", err)
		}
	})
}

func TestCommentCheckNotRegistered(t *testing.T) {
	r := newCommentRig(t)
	if n := r.engine.FilterCount(sdk.CommentCheckF.Name); n != 0 {
		t.Fatalf("no plugin: %d filters", n)
	}
	r.load(t, "add", false)
	if n := r.engine.FilterCount(sdk.CommentCheckF.Name); n != 0 {
		t.Fatalf("not granted: %d filters", n)
	}
	if st, _ := r.rt.status("commentcheck"); slices.Contains(st.Hooks, sdk.CommentCheckF.Name) {
		t.Fatalf("not granted but listed: %v", st.Hooks)
	}
	r.grant(t, sdk.PermCommentsRead)
	if n := r.engine.FilterCount(sdk.CommentCheckF.Name); n != 1 {
		t.Fatalf("granted: %d filters", n)
	}
	r.grant(t)
	if n := r.engine.FilterCount(sdk.CommentCheckF.Name); n != 0 {
		t.Fatalf("revoked: %d filters", n)
	}
}

func TestCommentCheckPayloadKeys(t *testing.T) {
	r := newCommentRig(t)
	r.load(t, "keys", true)
	got, err := r.check(context.Background())
	if err != nil || !slices.Equal(got.Reasons, []string{"commentcheck: body,body_hash,link_domains"}) {
		t.Fatalf("got %+v %v", got, err)
	}
	for _, bad := range []string{"email", "ip", "net", "user_agent", "author", "entity"} {
		if strings.Contains(strings.Join(got.Reasons, " "), bad) {
			t.Errorf("payload leaks %q", bad)
		}
	}
}

func TestCommentEventsNeedCommentsRead(t *testing.T) {
	r := newCommentRig(t)
	r.load(t, "empty", false)
	ev := sdk.CommentEvent{ID: "c1", EntityID: "e1", AuthorName: "A", BodyHTML: "<p>x</p>", Status: "approved"}

	r.engine.Fire(context.Background(), sdk.CommentApproved.Name, ev)
	r.engine.Wait()
	if strings.Contains(r.logs.String(), "plugin action failed") {
		t.Fatalf("comment.approved reached a plugin without comments.read:\n%s", r.logs.String())
	}

	// With comments.read the action runs; its kv write is denied (kv not
	// granted), which shows the delivery without a database.
	r.grant(t, sdk.PermCommentsRead)
	r.engine.Fire(context.Background(), sdk.CommentApproved.Name, ev)
	r.engine.Wait()
	if !strings.Contains(r.logs.String(), "capability_denied") {
		t.Fatalf("comment.approved was not delivered:\n%s", r.logs.String())
	}
}
