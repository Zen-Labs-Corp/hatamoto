//go:build integration

package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

type publishAuditRefusingSink struct{}

func (publishAuditRefusingSink) Record(context.Context, pgx.Tx, auditstate.Event) error {
	return errors.New("refused")
}

type publishAuditFixture struct {
	t      *testing.T
	ctx    context.Context
	db     *database.DB
	logger *slog.Logger
	ids    []uuid.UUID
	tag    string
}

// newPublishAuditFixture seeds two due scheduled entities.
func newPublishAuditFixture(t *testing.T) *publishAuditFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	f := &publishAuditFixture{t: t, ctx: ctx, db: db, tag: uuid.NewString()[:8],
		logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var schemaID uuid.UUID
	if err := db.Pool().QueryRow(ctx,
		`INSERT INTO hatamoto_entity_schemas (name, slug, schema) VALUES ($1, $2, '{}') RETURNING id`,
		"pa "+f.tag, "pa-"+f.tag).Scan(&schemaID); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		var id uuid.UUID
		if err := db.Pool().QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status, published_at)
			VALUES ($1, $2, $3, 'scheduled', now() - interval '1 minute') RETURNING id`,
			schemaID, "pa "+n, f.tag+"-"+n).Scan(&id); err != nil {
			t.Fatal(err)
		}
		f.ids = append(f.ids, id)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_admin_audit WHERE action = 'entity.publish_scheduled' AND target = ANY($1)`, f.idStrings())
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id = ANY($1)`, f.ids)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	return f
}

func (f *publishAuditFixture) idStrings() []string {
	var s []string
	for _, id := range f.ids {
		s = append(s, id.String())
	}
	return s
}

func (f *publishAuditFixture) count(q string) int {
	f.t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(f.ctx, q, f.ids).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *publishAuditFixture) status(status string) int {
	return f.count(`SELECT count(*) FROM hatamoto_entities WHERE id = ANY($1) AND status = '` + status + `'`)
}

func (f *publishAuditFixture) rows() int {
	f.t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT count(*) FROM hatamoto_admin_audit
		WHERE action = 'entity.publish_scheduled' AND actor_kind = 'system' AND target = ANY($1)`, f.idStrings()).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

type publishAuditEvents struct{ n int }

func (e *publishAuditEvents) EntityPublished(context.Context, uuid.UUID, string, string, string, int) {
	e.n++
}

func TestPublishWorkerAuditWritesOneRowPerPromotion(t *testing.T) {
	f := newPublishAuditFixture(t)
	auditstate.Reset()
	w := NewPublishWorker(f.db, f.logger)
	w.SetAuditTx(auditlog.TxHook(f.logger))
	got, err := w.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || f.status("published") != 2 || f.rows() != 2 {
		t.Fatalf("promoted=%d published=%d rows=%d, want both entities published with 2 rows", len(got), f.status("published"), f.rows())
	}
}

func TestPublishWorkerAuditRefusingSinkRollsBack(t *testing.T) {
	f := newPublishAuditFixture(t)
	auditstate.Register(publishAuditRefusingSink{})
	t.Cleanup(auditstate.Reset)
	w := NewPublishWorker(f.db, f.logger)
	ev := &publishAuditEvents{}
	w.SetEvents(ev)
	w.SetAuditTx(auditlog.TxHook(f.logger))
	if _, err := w.Run(context.Background()); !errors.Is(err, auditstate.ErrSink) {
		t.Fatalf("Run err = %v, want ErrSink", err)
	}
	if f.status("scheduled") != 2 || ev.n != 0 || f.rows() != 0 {
		t.Fatalf("scheduled=%d events=%d rows=%d, want 2/0/0", f.status("scheduled"), ev.n, f.rows())
	}
}

func TestPublishWorkerAuditWithoutHookWritesNoRows(t *testing.T) {
	f := newPublishAuditFixture(t)
	auditstate.Reset()
	if _, err := NewPublishWorker(f.db, f.logger).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.status("published") != 2 || f.rows() != 0 {
		t.Fatalf("published=%d rows=%d, want 2/0", f.status("published"), f.rows())
	}
}
