package updater

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/release"
)

// statusSink is a Querier with no stored row that records every status write.
type statusSink struct {
	mu    sync.Mutex
	saved []Status
}

func (s *statusSink) QueryRow(context.Context, string, ...any) pgx.Row { return noRow{} }

func (s *statusSink) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Decision: args[2].(string), Reason: args[3].(string), Target: args[4].(string), LastResult: args[1].(string)}
	s.saved = append(s.saved, st)
	return pgconn.CommandTag{}, nil
}

func (s *statusSink) last(t *testing.T) Status {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		t.Fatal("no status was stored")
	}
	return s.saved[len(s.saved)-1]
}

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

func storedBox(t *testing.T) (*runBox, *statusSink) {
	b := newRunBox(t, "v1.0.0")
	sink := &statusSink{}
	b.u.env.Store = NewStore(sink)
	return b, sink
}

func TestCheckOnlyStoresTheDecisionAndInstallsNothing(t *testing.T) {
	b, sink := storedBox(t)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{CheckOnly: true})
	if err != nil || st.Decision != KindApplying || strings.Contains(st.Reason, "dry run") {
		t.Fatalf("%+v %v", st, err)
	}
	if b.hits("hatamoto-") != 0 || len(b.applied)+len(b.staged) != 0 {
		t.Fatal("a check installed something")
	}
	got := sink.last(t)
	if got.Decision != KindScheduled || got.Target != "v1.0.1" || got.LastResult != ResultOK {
		t.Fatalf("stored %+v: a due release is stored as scheduled, not as an install in progress", got)
	}
}

func TestCheckOnlyNeverStoresARollbackThatDidNotHappen(t *testing.T) {
	sink := &statusSink{}
	b := yankedOnBootstrapHistory(t, "")
	b.u.env.Store = NewStore(sink)
	st, err := b.u.Run(t.Context(), RunOpts{CheckOnly: true})
	if err != nil || st.Decision != KindRolledBackYank || b.rolledBack != 0 {
		t.Fatalf("%+v %v rolledBack=%d", st, err, b.rolledBack)
	}
	if got := sink.last(t); got.Decision != KindRollbackPending || !strings.Contains(got.Reason, "not switching") {
		t.Fatalf("stored %+v", got)
	}
}

func TestPlainDryRunStillStoresNothing(t *testing.T) {
	b, sink := storedBox(t)
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(sink.saved) != 0 {
		t.Fatalf("a dry run stored %+v", sink.saved)
	}
}

func TestManagedApplyStoresEveryOutcome(t *testing.T) {
	cases := map[string]struct {
		setup func(*runBox)
		want  string
		err   bool
	}{
		"switched": {func(b *runBox) { b.supervisedNow = true }, KindApplying, false},
		"blocked": {func(b *runBox) {
			b.u.env.Plugins = fakePlugins{{Kind: "plugin", Name: "hello", Range: "^0.9"}}
		}, KindBlocked, false},
		"import running": {func(b *runBox) { b.imports.running = true }, KindBusy, false},
		"download error": {func(b *runBox) { delete(b.files, "/releases/v1.0.1/hatamoto-"+testPlatform) }, KindFailed, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b, sink := storedBox(t)
			b.offer("v1.0.1", func(r *release.Record) { r.SDK = "0.10.0" })
			b.publish([]release.Entry{open100("v1.0.1")}, nil)
			c.setup(b)
			_, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true})
			if (err != nil) != c.err {
				t.Fatalf("err=%v", err)
			}
			got := sink.last(t)
			if got.Decision != c.want || got.Target != "v1.0.1" {
				t.Fatalf("stored %+v, want decision %s for v1.0.1", got, c.want)
			}
			if c.err && !strings.Contains(got.Reason, "download") {
				t.Fatalf("the error must reach the stored reason: %+v", got)
			}
		})
	}
}

func TestUnmanagedApplyStoresNothing(t *testing.T) {
	b, sink := storedBox(t)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1"}); err != nil {
		t.Fatal(err)
	}
	if len(sink.saved) != 0 {
		t.Fatalf("the CLI route stored %+v", sink.saved)
	}
}

func TestManagedApplyWaitsForARunningCheck(t *testing.T) {
	b, sink := storedBox(t)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	b.u.mu.Lock()
	go func() {
		time.Sleep(400 * time.Millisecond)
		b.u.mu.Unlock()
	}()
	st, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true})
	if err != nil || st.Decision != KindApplying {
		t.Fatalf("%+v %v: a managed apply must wait, not report busy", st, err)
	}
	if sink.last(t).Decision != KindApplying {
		t.Fatal("outcome not stored")
	}
}
