package pgbouncertest

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ScratchDB creates a private copy of the database in directDSN (the DSN's
// database is the template, so it must be idle) over a direct connection and
// returns the DSN of the copy. The copy is dropped when the test ends. DDL
// stays off the pooler on purpose. It skips the test, before touching the
// database, when no pgbouncer binary is available.
//
// A template must be idle. When it is not, ScratchDB fails, unless
// HATAMOTO_POOLER_TERMINATE=1 allows it to cut the other sessions on the
// template (the integration gate sets it: its databases are private).
func ScratchDB(t testing.TB, directDSN string) string {
	t.Helper()
	binary(t)
	u, err := url.Parse(directDSN)
	if err != nil {
		t.Fatal(err)
	}
	template := strings.TrimPrefix(u.Path, "/")
	name := "hm_pool_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, directDSN)
	if err != nil {
		t.Fatal(err)
	}
	create := `CREATE DATABASE ` + name + ` TEMPLATE ` + template
	_, err = admin.Exec(ctx, create)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "55006" && os.Getenv("HATAMOTO_POOLER_TERMINATE") == "1" {
		// Connections left by earlier tests (a pooler keeps its server
		// connections open) are cut.
		_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()`, template)
		_, err = admin.Exec(ctx, create)
	}
	if err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DROP DATABASE `+name+` WITH (FORCE)`)
		_ = admin.Close(ctx)
	})
	u.Path = "/" + name
	return u.String()
}
