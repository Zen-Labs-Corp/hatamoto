//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// changeRows is the number of rows in the content change log.
func changeRows(t *testing.T, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) int64 {
	t.Helper()
	var n int64
	if err := q.QueryRowContext(context.Background(), `SELECT count(*) FROM hatamoto_content_changes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func migratedEpochDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(context.Background()); err != nil {
		t.Fatalf("up: %v", err)
	}
	return db
}

func seedSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO hatamoto_entity_schemas (name, slug, schema) VALUES ('EpochPage', 'epoch-page', '{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Every kind of content write that changes rows logs exactly one row, also the
// raw UPDATE shape the scheduled-publish worker uses.
func TestContentChanges_WritesLogOneRow(t *testing.T) {
	ctx := context.Background()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)
	var entityID, menuID string

	steps := []struct {
		name string
		run  func() error
	}{
		{"insert entity", func() error {
			return db.QueryRowContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status) VALUES ($1, 'Themes', 'themes', 'draft') RETURNING id`, schemaID).Scan(&entityID)
		}},
		{"update entity", func() error {
			_, err := db.ExecContext(ctx, `UPDATE hatamoto_entities SET name = 'Themes 2' WHERE id = $1`, entityID)
			return err
		}},
		{"status change to scheduled", func() error {
			_, err := db.ExecContext(ctx, `UPDATE hatamoto_entities SET status = 'scheduled', published_at = now() + interval '1 hour' WHERE id = $1`, entityID)
			return err
		}},
		{"worker publish", func() error {
			_, err := db.ExecContext(ctx, `UPDATE hatamoto_entities SET status = 'published' WHERE status = 'scheduled' AND published_at <= now() + interval '2 hours'`)
			return err
		}},
		{"insert menu", func() error {
			return db.QueryRowContext(ctx, `INSERT INTO hatamoto_menus (name, slug) VALUES ('Main', 'main') RETURNING id`).Scan(&menuID)
		}},
		{"insert menu item", func() error {
			_, err := db.ExecContext(ctx, `INSERT INTO hatamoto_menu_items (menu_id, label, link_type, url) VALUES ($1, 'x', 'custom', '/x')`, menuID)
			return err
		}},
		{"delete menu item", func() error {
			_, err := db.ExecContext(ctx, `DELETE FROM hatamoto_menu_items WHERE menu_id = $1`, menuID)
			return err
		}},
		{"delete entity", func() error {
			_, err := db.ExecContext(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, entityID)
			return err
		}},
	}
	for _, s := range steps {
		before := changeRows(t, db)
		if err := s.run(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if after := changeRows(t, db); after != before+1 {
			t.Errorf("%s: change log %d -> %d rows, want one more", s.name, before, after)
		}
	}
}

// A statement that touches no rows must not log: the scheduled-publish worker
// runs one every minute, and each log row purges every server's page cache.
func TestContentChanges_ZeroRowStatementsDoNotLog(t *testing.T) {
	ctx := context.Background()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)
	// Rows exist, so only the WHERE clause makes the statements empty.
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug, status) VALUES ($1, 'Idle', 'idle', 'draft')`, schemaID); err != nil {
		t.Fatal(err)
	}
	var menuID string
	if err := db.QueryRowContext(ctx, `INSERT INTO hatamoto_menus (name, slug) VALUES ('Main', 'main') RETURNING id`).Scan(&menuID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_menu_items (menu_id, label, link_type, url) VALUES ($1, 'x', 'custom', '/x')`, menuID); err != nil {
		t.Fatal(err)
	}
	before := changeRows(t, db)

	for _, q := range []string{
		// The worker's promotion with nothing due.
		`UPDATE hatamoto_entities SET status = 'published' WHERE status = 'scheduled' AND published_at <= now()`,
		`UPDATE hatamoto_entities SET name = 'x' WHERE false`,
		`DELETE FROM hatamoto_entities WHERE false`,
		`INSERT INTO hatamoto_entities (schema_id, name, slug) SELECT $1, 'n', 's' WHERE false`,
		`UPDATE hatamoto_menus SET name = 'x' WHERE false`,
		`DELETE FROM hatamoto_menus WHERE false`,
		`UPDATE hatamoto_menu_items SET label = 'x' WHERE false`,
		`DELETE FROM hatamoto_menu_items WHERE false`,
	} {
		args := []any{}
		if strings.Contains(q, "$1") {
			args = append(args, schemaID)
		}
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if after := changeRows(t, db); after != before {
			t.Fatalf("%s: logged a change (%d -> %d rows) though it touched nothing", q, before, after)
		}
	}
}

// Many statements in one transaction log one row, so a bulk import neither
// bloats the log nor makes the poller see many changes.
func TestContentChanges_OneRowPerTransaction(t *testing.T) {
	ctx := context.Background()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)
	before := changeRows(t, db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"a", "b", "c"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, $2, $2)`, schemaID, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE hatamoto_entities SET name = 'z'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := changeRows(t, db); after != before+1 {
		t.Errorf("change log %d -> %d rows for one transaction, want +1", before, after)
	}
}

// Content writers must not queue behind each other on a shared row: a second
// transaction writing while the first is still open neither waits nor
// deadlocks.
func TestContentChanges_ConcurrentWritersDoNotBlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)
	a, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Rollback()
	if _, err := a.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'A', 'a')`, schemaID); err != nil {
		t.Fatal(err)
	}
	// B starts, writes and commits while A is open: it must not wait for A.
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'B', 'b')`, schemaID); err != nil {
		t.Fatalf("second writer blocked or failed while the first tx is open: %v", err)
	}
	if err := a.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := changeRows(t, db); n != 2 {
		t.Errorf("change log has %d rows, want 2 (one per transaction)", n)
	}
}

func TestContentChanges_RollbackLogsNothing(t *testing.T) {
	ctx := context.Background()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)
	before := changeRows(t, db)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'Gone', 'gone')`, schemaID); err != nil {
		t.Fatal(err)
	}
	if in := changeRows(t, tx); in != before+1 {
		t.Fatalf("change log inside the tx has %d rows, want %d", in, before+1)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := changeRows(t, db); after != before {
		t.Errorf("change log after rollback has %d rows, want %d", after, before)
	}
}

// A writer role with rights on the content tables but none on the log table
// (the shape of a restricted CLI/worker login) still logs: the function is
// SECURITY DEFINER.
func TestContentChanges_LogsForRoleWithoutLogPrivilege(t *testing.T) {
	ctx := context.Background()
	db := migratedEpochDB(t)
	schemaID := seedSchema(t, db)

	role := "hm_epoch_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	const pw = "epoch-test-pw"
	if _, err := db.ExecContext(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD '`+pw+`'`); err != nil {
		t.Skipf("cannot create role: %v", err)
	}
	var dbName string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DROP OWNED BY `+role)
		_, _ = db.ExecContext(ctx, `DROP ROLE IF EXISTS `+role)
	})
	for _, q := range []string{
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON hatamoto_entities TO ` + role,
		`GRANT SELECT ON hatamoto_entity_schemas TO ` + role,
		`GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO ` + role,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	// Reuse the scratch DB's connection details with the restricted login.
	var dsn string
	if err := db.QueryRowContext(ctx, `SELECT inet_server_port()::text`).Scan(&dsn); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(role, pw), Host: "127.0.0.1:" + dsn, Path: "/" + dbName, RawQuery: "sslmode=disable"}
	w, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.ExecContext(ctx, `SELECT count(*) FROM hatamoto_content_changes`); err == nil {
		t.Fatal("test premise broken: the writer role can read the log table directly")
	}
	before := changeRows(t, db)
	if _, err := w.ExecContext(ctx, `INSERT INTO hatamoto_entities (schema_id, name, slug) VALUES ($1, 'CLI', 'cli')`, schemaID); err != nil {
		t.Fatalf("restricted write: %v", err)
	}
	if after := changeRows(t, db); after != before+1 {
		t.Errorf("change log %d -> %d rows after a restricted-role write, want +1", before, after)
	}
}
