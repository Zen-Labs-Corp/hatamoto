//go:build integration

package pluginhost

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// poolerPluginDB is a PluginDB whose core pool goes through a transaction
// pooler while its plugin pools follow the direct database, as configured
// with DATABASE_DIRECT_URL. direct is the core pool straight on Postgres.
func poolerPluginDB(t *testing.T) (db *PluginDB, direct *pgxpool.Pool, dbID string) {
	t.Helper()
	direct, _ = scratchDBMode(t, modeCreateRole)
	dc := direct.Config().ConnConfig
	directDSN := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", dc.User, dc.Password, dc.Host, dc.Port, dc.Database)
	pooledCfg, err := pgxpool.ParseConfig(pgbouncertest.Start(t, directDSN, 2))
	if err != nil {
		t.Fatal(err)
	}
	pooledCfg.MaxConns = 4
	pooled, err := pgxpool.NewWithConfig(context.Background(), pooledCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pooled.Close)

	db = NewPluginDB(pooled, quiet())
	db.SetBaseConfig(func() *pgx.ConnConfig { return dc.Copy() })
	t.Cleanup(db.Close)
	dbID, err = instanceID(context.Background(), direct)
	if err != nil {
		t.Fatal(err)
	}
	return db, direct, dbID
}

// A plugin with a database role reads and writes while the core pool sits
// behind a transaction pooler, and runs as its own role (its login),
// not as the core application role.
func TestPooler_PluginReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	db, _, dbID := poolerPluginDB(t)

	if err := db.Provision(ctx, "pooled"); err != nil {
		t.Fatalf("provision through the pooler: %v", err)
	}
	migs, err := ReadMigrations(fsOf(map[string]string{
		"migrations/001_init.sql": "-- +goose Up\nCREATE TABLE notes (id int PRIMARY KEY, v text);\n-- +goose Down\nDROP TABLE notes;\n",
	}), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := db.Migrate(ctx, "pooled", migs); err != nil || n != 1 {
		t.Fatalf("plugin migration: %d %v", n, err)
	}
	if n, err := db.Exec(ctx, "pooled", `INSERT INTO notes VALUES (1, 'a'), (2, 'b')`); err != nil || n != 2 {
		t.Fatalf("insert = %d, %v", n, err)
	}
	res, err := db.Query(ctx, "pooled", `SELECT count(*), current_user FROM notes GROUP BY 2`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("select: %v %+v", err, res)
	}
	if got, want := fmt.Sprint(res.Rows[0][0]), "2"; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(res.Rows[0][1]), roleNameFor(dbID, "pooled"); got != want {
		t.Errorf("current_user = %s, want the plugin role %s", got, want)
	}
}

// Plugin pools ignore the pooler: their server connection is on the direct
// endpoint's port, not the pooler's.
func TestPooler_PluginPoolIgnoresPoolerWhenOverridden(t *testing.T) {
	ctx := context.Background()
	db, direct, _ := poolerPluginDB(t)
	if err := db.Provision(ctx, "direct"); err != nil {
		t.Fatal(err)
	}
	res, err := db.Query(ctx, "direct", `SELECT inet_server_port()`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("inet_server_port: %v %+v", err, res)
	}
	if got, want := fmt.Sprint(res.Rows[0][0]), fmt.Sprint(direct.Config().ConnConfig.Port); got != want {
		t.Fatalf("plugin connection on port %s, want the direct port %s", got, want)
	}
	pp, err := db.pluginPool(ctx, "direct")
	if err != nil {
		t.Fatal(err)
	}
	if pp.Config().ConnConfig.Port != direct.Config().ConnConfig.Port {
		t.Fatalf("plugin pool port %d, want %d", pp.Config().ConnConfig.Port, direct.Config().ConnConfig.Port)
	}
}
