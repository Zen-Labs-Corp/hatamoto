//go:build integration

package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

func cliAuditDB(t *testing.T) (context.Context, *database.DB) {
	t.Helper()
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: scratchDSN(t), MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func TestCliAuditEntityAndRedirectWrites(t *testing.T) {
	ctx, db := cliAuditDB(t)
	logger := quietLogger()

	var out bytes.Buffer
	if err := runEntity(ctx, db, []string{"create", "--schema", "page", "--name", "Hello"}, &out, logger); err != nil {
		t.Fatal(err)
	}
	var id, actor, kind, via string
	if err := db.Pool().QueryRow(ctx, `SELECT target, actor, actor_kind, detail->>'via' FROM hatamoto_admin_audit WHERE action='entity.create'`).Scan(&id, &actor, &kind, &via); err != nil {
		t.Fatalf("entity.create row: %v", err)
	}
	if actor != "cli" || kind != "cli" || via != "cli" {
		t.Fatalf("row = %q %q %q", actor, kind, via)
	}

	if err := runEntity(ctx, db, []string{"publish", id}, &out, logger); err != nil {
		t.Fatal(err)
	}
	if err := runEntity(ctx, db, []string{"get", id}, &out, logger); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action LIKE 'entity.%'`).Scan(&n)
	if n != 2 { // create + publish; get and list write nothing
		t.Fatalf("entity rows = %d, want 2", n)
	}

	out.Reset()
	if err := runRedirect(ctx, db, []string{"create", "--from", "/old?token=x", "--to", "/new"}, &out, logger); err != nil {
		t.Fatal(err)
	}
	var rid string
	if err := db.Pool().QueryRow(ctx, `SELECT target FROM hatamoto_admin_audit WHERE action='redirect.created' AND actor_kind='cli' AND detail->>'via'='cli' AND detail::text NOT LIKE '%token%'`).Scan(&rid); err != nil {
		t.Fatalf("redirect.created row: %v", err)
	}
	if err := runRedirect(ctx, db, []string{"delete", rid}, &out, logger); err != nil {
		t.Fatal(err)
	}
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action='redirect.deleted' AND target=$1 AND actor_kind='cli'`, rid).Scan(&n)
	if n != 1 {
		t.Fatalf("redirect.deleted rows = %d", n)
	}
}

type cliAuditRefuser struct{}

func (cliAuditRefuser) Record(context.Context, pgx.Tx, auditstate.Event) error {
	return context.DeadlineExceeded
}

func TestCliAuditRefusingSinkFailsEntityCreate(t *testing.T) {
	ctx, db := cliAuditDB(t)
	auditstate.Register(cliAuditRefuser{})
	t.Cleanup(auditstate.Reset)

	var out bytes.Buffer
	err := runEntity(ctx, db, []string{"create", "--schema", "page", "--name", "Nope"}, &out, quietLogger())
	if err == nil {
		t.Fatal("create succeeded with a refusing sink")
	}
	var n int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE name='Nope'`).Scan(&n)
	if n != 0 || strings.Contains(out.String(), "Nope") {
		t.Fatalf("entities %d, out %q", n, out.String())
	}
}
