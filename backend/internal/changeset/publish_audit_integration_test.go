//go:build integration

package changeset

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/auditstate"
)

type refusingSink struct{}

func (refusingSink) Record(context.Context, pgx.Tx, audit.Event) error { return errors.New("refused") }

// auditTxViaRecord mirrors the server wiring: only a registered sink's error
// fails the publish.
func auditTxViaRecord(actor string) func(context.Context, pgx.Tx, string, string, map[string]any) error {
	return func(ctx context.Context, tx pgx.Tx, action, target string, detail map[string]any) error {
		err := audit.Record(ctx, tx, audit.Event{Actor: actor, ActorKind: audit.KindSystem, Action: action, Target: target, Detail: detail})
		if errors.Is(err, audit.ErrSink) {
			return err
		}
		return nil
	}
}

func (f *fixture) auditRows(cs int64) int {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM hatamoto_admin_audit WHERE action = 'changeset.publish' AND target = $1`, changesetTargetForTest(cs))
}

func changesetTargetForTest(cs int64) string { return fmt.Sprintf("changeset:%d", cs) }

func TestPublishRegisteredFailingSinkRollsBack(t *testing.T) {
	f := newFixture(t)
	f.eng.AuditTx = auditTxViaRecord("system")
	auditstate.Register(refusingSink{})
	t.Cleanup(auditstate.Reset)

	cs := f.begin("")
	f.stage(cs, f.entityItem(f.tag+"-a", f.tag+"-a", "x"))
	if _, err := f.eng.Publish(f.ctx, cs); !errors.Is(err, audit.ErrSink) {
		t.Fatalf("Publish err = %v, want ErrSink", err)
	}
	if n := f.entities(); n != 0 {
		t.Fatalf("entities = %d, want 0 (rolled back)", n)
	}
	if s := f.state(cs); s != string(StateStaged) {
		t.Fatalf("state = %q, want staged", s)
	}
	if n := f.auditRows(cs); n != 0 {
		t.Fatalf("audit rows = %d, want 0", n)
	}
}

func TestPublishDefaultSinkFailureDoesNotBreakPublish(t *testing.T) {
	f := newFixture(t)
	f.eng.AuditTx = auditTxViaRecord("x\x00") // NUL makes the default insert fail
	auditstate.Reset()

	cs := f.begin("")
	f.stage(cs, f.entityItem(f.tag+"-b", f.tag+"-b", "x"))
	f.publish(cs)
	if n := f.entities(); n != 1 {
		t.Fatalf("entities = %d, want 1", n)
	}
	if s := f.state(cs); s != string(StatePublished) {
		t.Fatalf("state = %q, want published", s)
	}
}

func TestPublishWritesOneAuditRowInTx(t *testing.T) {
	f := newFixture(t)
	f.eng.AuditTx = auditTxViaRecord("system")
	auditstate.Reset()

	cs := f.begin("")
	f.stage(cs, f.entityItem(f.tag+"-c", f.tag+"-c", "x"))
	f.publish(cs)
	if n := f.auditRows(cs); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_admin_audit WHERE action = 'changeset.publish' AND target = $1`, changesetTargetForTest(cs))
	})
}
