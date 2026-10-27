package updater

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/release"
)

type fakeBackups struct {
	note  string
	err   error
	calls []string
}

func (f *fakeBackups) PreUpdate(_ context.Context, target string) (string, error) {
	f.calls = append(f.calls, target)
	return f.note, f.err
}

const pgDumpErr = "pg_dump: error: query failed: ERROR:  permission denied for table x"

func backupBox(t *testing.T, fb *fakeBackups) (*runBox, *statusSink) {
	t.Helper()
	b, sink := storedBox(t)
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	if fb != nil {
		b.u.env.Backups = fb
	}
	return b, sink
}

func TestFailedBackupBlocksTheUpdateAndKeepsTheTarget(t *testing.T) {
	for name, o := range map[string]RunOpts{
		"scheduled": {},
		"managed":   {Apply: "v1.0.1", Managed: true},
		"cli apply": {Apply: "v1.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			fb := &fakeBackups{err: errors.New(pgDumpErr)}
			b, sink := backupBox(t, fb)
			st, err := b.u.Run(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			if len(b.applied)+len(b.staged) != 0 {
				t.Fatalf("switched despite the failed backup: applied=%d staged=%d", len(b.applied), len(b.staged))
			}
			if st.Decision != KindBackupFailed || st.Target != "v1.0.1" || st.LastResult != ResultError ||
				!strings.Contains(st.Reason, "pg_dump: error:") {
				t.Fatalf("status %+v", st)
			}
			if o.Apply == "" || o.Managed {
				if got := sink.last(t); got.Decision != KindBackupFailed || got.Target != "v1.0.1" {
					t.Fatalf("stored %+v", got)
				}
			}
		})
	}
}

func TestSkipBackupSkipsOnlyThatRun(t *testing.T) {
	fb := &fakeBackups{note: "backup x"}
	b, _ := backupBox(t, fb)
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true, SkipBackup: true}); err != nil {
		t.Fatal(err)
	}
	if len(fb.calls) != 0 || len(b.applied) != 1 {
		t.Fatalf("calls=%v applied=%d", fb.calls, len(b.applied))
	}
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true}); err != nil {
		t.Fatal(err)
	}
	if len(fb.calls) != 1 {
		t.Fatalf("second run did not back up: %v", fb.calls)
	}
}

func TestEnvBackupZeroDisablesTheBackup(t *testing.T) {
	t.Setenv(EnvBackup, "0")
	fb := &fakeBackups{err: errors.New(pgDumpErr)}
	b, _ := backupBox(t, fb)
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true}); err != nil {
		t.Fatal(err)
	}
	if len(fb.calls) != 0 || len(b.applied) != 1 {
		t.Fatalf("calls=%v applied=%d", fb.calls, len(b.applied))
	}
}

func TestBackupRunsForTheCLIApplyAndNoteIsReported(t *testing.T) {
	fb := &fakeBackups{note: "backup 20260101"}
	b, _ := backupBox(t, fb)
	st, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1"})
	if err != nil || len(fb.calls) != 1 || fb.calls[0] != "v1.0.1" || !strings.Contains(st.Reason, "backup 20260101") {
		t.Fatalf("err=%v calls=%v st=%+v", err, fb.calls, st)
	}
}

func TestNilBackupsBlocksUnlessSkipped(t *testing.T) {
	b, _ := backupBox(t, nil)
	b.u.env.Backups = nil
	st, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true})
	if err != nil || len(b.applied) != 0 || st.Decision != KindBackupFailed || !strings.Contains(st.Reason, "no backup engine") {
		t.Fatalf("err=%v applied=%d st=%+v", err, len(b.applied), st)
	}
	if _, err := b.u.Run(t.Context(), RunOpts{Apply: "v1.0.1", Managed: true, SkipBackup: true}); err != nil || len(b.applied) != 1 {
		t.Fatalf("skipped run: err=%v applied=%d", err, len(b.applied))
	}
}
