package pgbouncertest

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ScratchDB creates a private copy of the database in directDSN (the DSN's
// database is the template, so it must be idle) over a direct connection and
// returns the DSN of the copy. The copy is dropped when the test ends. DDL
// stays off the pooler on purpose.
func ScratchDB(t testing.TB, directDSN string) string {
	t.Helper()
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
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE `+template); err != nil {
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
