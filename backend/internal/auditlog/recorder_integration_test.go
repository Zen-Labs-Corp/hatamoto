//go:build integration

package auditlog_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

type failSink struct{}

func (failSink) Record(context.Context, pgx.Tx, auditstate.Event) error { return errors.New("refused") }

func count(t *testing.T, p *pgxpool.Pool, target string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1`, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecorder_PoolWritesAllColumns(t *testing.T) {
	p := pool(t)
	target := "t-" + uuid.NewString()
	uid, kid := uuid.NewString(), uuid.NewString()
	err := auditlog.Recorder{Pool: p}.Record(context.Background(), nil, auditstate.Event{
		ActorID: uid, Actor: "a@example.com", ActorKind: "user", KeyID: kid, Action: "test.pool", Target: target,
		Detail: map[string]any{"token": "s3cret", "k": "v"}, IP: "203.0.113.9", UserAgent: "ua", RequestID: "rid", Result: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	var actorID, keyID *uuid.UUID
	var actor, kind, ip, ua, rid, result, detail string
	if err := p.QueryRow(context.Background(), `SELECT actor_id, actor, actor_kind, actor_key_id, ip, user_agent, request_id, result, detail::text
		FROM hatamoto_admin_audit WHERE target = $1`, target).Scan(&actorID, &actor, &kind, &keyID, &ip, &ua, &rid, &result, &detail); err != nil {
		t.Fatal(err)
	}
	if actorID == nil || actorID.String() != uid || keyID == nil || keyID.String() != kid || actor != "a@example.com" ||
		kind != "user" || ip != "203.0.113.9" || ua != "ua" || rid != "rid" || result != "denied" {
		t.Fatalf("row = %v %q %q %v %q %q %q %q", actorID, actor, kind, keyID, ip, ua, rid, result)
	}
	if want := `{"k": "v", "token": "[redacted]"}`; detail != want {
		t.Fatalf("detail = %s, want %s", detail, want)
	}
}

func TestRecorder_TxFailedInsertKeepsTxUsable(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	err = auditlog.Recorder{}.Record(ctx, tx, auditstate.Event{Actor: "x\x00", Action: "test.nul", Target: "t-nul"})
	if !errors.Is(err, auditlog.ErrTable) || auditlog.MustFail(err) {
		t.Fatalf("err = %v, want ErrTable", err)
	}
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("tx unusable after a failed insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRecorder_SinkErrorIsErrSink(t *testing.T) {
	p := pool(t)
	auditstate.Reset()
	t.Cleanup(auditstate.Reset)
	auditstate.Register(failSink{})
	target := "t-" + uuid.NewString()
	err := auditlog.Recorder{Pool: p}.Record(context.Background(), nil, auditstate.Event{Actor: "cli", Action: "test.sink", Target: target})
	if !errors.Is(err, auditlog.ErrSink) || !auditlog.MustFail(err) {
		t.Fatalf("err = %v, want ErrSink", err)
	}
	if n := count(t, p, target); n != 1 {
		t.Fatalf("table rows = %d, want 1 (sink is called in addition to the table)", n)
	}
}
