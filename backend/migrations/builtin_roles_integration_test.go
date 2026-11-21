//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const builtinRolesVersion = 202611231000

var newRoles = []string{"author", "contributor", "reviewer", "read-only"}

func insertRoleUser(db *sql.DB, role string) error {
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO hatamoto_users (id, email, role) VALUES ($1, $2, $3)`, uuid.New(), role+"-"+uuid.NewString()[:6]+"@example.com", role)
	return err
}

func insertRoleInvite(db *sql.DB, role string) error {
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO hatamoto_user_tokens (kind, token_hash, email, role, expires_at)
		 VALUES ('invite', $1, $2, $3, now() + interval '1 day')`,
		[]byte(uuid.NewString()), role+"-"+uuid.NewString()[:6]+"@example.com", role)
	return err
}

func isCheckViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23514"
}

// roleChecks counts the CHECK constraints on a table whose definition restricts role.
func roleChecks(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT conname FROM pg_constraint
		  WHERE conrelid = $1::regclass AND contype = 'c' AND conname <> 'chk_user_tokens_shape' AND pg_get_constraintdef(oid) LIKE '%role%'
		  ORDER BY conname`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

func TestBuiltinRoles_KeepsOldRowsAndAcceptsNew(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, builtinRolesVersion-1); err != nil {
		t.Fatalf("up to previous: %v", err)
	}
	if got := roleChecks(t, db, "hatamoto_user_tokens"); len(got) != 1 || got[0] != "hatamoto_user_tokens_role_check" {
		t.Fatalf("role checks on tokens before = %v, want the default-named one", got)
	}
	for _, r := range []string{"owner", "editor"} {
		if err := insertRoleUser(db, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := insertRoleInvite(db, "editor"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var u, k string
		if err := db.QueryRowContext(ctx, `SELECT coalesce(string_agg(id::text||role||email, ',' ORDER BY id), '') FROM hatamoto_users`).Scan(&u); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT coalesce(string_agg(id::text||role||email, ',' ORDER BY id), '') FROM hatamoto_user_tokens`).Scan(&k); err != nil {
			t.Fatal(err)
		}
		return u + "|" + k
	}
	before := snapshot()
	if _, err := p.UpTo(ctx, builtinRolesVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	if after := snapshot(); after != before {
		t.Errorf("old rows changed:\n before %s\n after  %s", before, after)
	}
	for _, r := range newRoles {
		if err := insertRoleUser(db, r); err != nil {
			t.Errorf("user %s: %v", r, err)
		}
		if err := insertRoleInvite(db, r); err != nil {
			t.Errorf("invite %s: %v", r, err)
		}
	}
	if err := insertRoleUser(db, "viewer"); !isCheckViolation(err) {
		t.Errorf("viewer user error = %v, want 23514", err)
	}
	if err := insertRoleInvite(db, "viewer"); !isCheckViolation(err) {
		t.Errorf("viewer invite error = %v, want 23514", err)
	}
	if got := roleChecks(t, db, "hatamoto_user_tokens"); len(got) != 1 || got[0] != "chk_user_tokens_role" {
		t.Errorf("role checks on tokens = %v, want exactly chk_user_tokens_role", got)
	}
	if got := roleChecks(t, db, "hatamoto_users"); len(got) != 1 || got[0] != "chk_users_role" {
		t.Errorf("role checks on users = %v, want exactly chk_users_role", got)
	}
}

func TestBuiltinRoles_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.UpTo(ctx, builtinRolesVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := insertRoleUser(db, "read-only"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, builtinRolesVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_users WHERE role = 'read-only'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("read-only rows after down = %d (%v), want 1", n, err)
	}
	if err := insertRoleUser(db, "reviewer"); !isCheckViolation(err) {
		t.Errorf("reviewer after down error = %v, want 23514", err)
	}
	if _, err := p.UpTo(ctx, builtinRolesVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := insertRoleUser(db, "reviewer"); err != nil {
		t.Errorf("reviewer after second up: %v", err)
	}
}
