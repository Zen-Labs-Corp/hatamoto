//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
)

const userIdentitiesVersion = 202611211400

func TestUserIdentities_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if regclass(t, db, "hatamoto_user_identities") == nil {
		t.Fatal("hatamoto_user_identities missing after up")
	}
	if _, err := p.DownTo(ctx, userIdentitiesVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if regclass(t, db, "hatamoto_user_identities") != nil {
		t.Error("hatamoto_user_identities still exists after down")
	}
	if regclass(t, db, "hatamoto_users") == nil {
		t.Error("hatamoto_users gone after down of the identities migration")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if regclass(t, db, "hatamoto_user_identities") == nil {
		t.Error("hatamoto_user_identities missing after second up")
	}
}

func TestUserIdentities_UniqueAndCascade(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	if _, err := provider(t, db).Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const userA = "66666666-6666-6666-6666-666666666666"
	const userB = "77777777-7777-7777-7777-777777777777"
	must(`INSERT INTO hatamoto_users (id, email, role) VALUES ($1, 'a@example.test', 'editor'), ($2, 'b@example.test', 'editor')`, userA, userB)
	ins := `INSERT INTO hatamoto_user_identities (user_id, issuer, subject) VALUES ($1, $2, $3)`
	must(ins, userA, "https://idp.example.test", "sub-1")

	err := exec(ins, userB, "https://idp.example.test", "sub-1")
	if err == nil || !strings.Contains(err.Error(), "hatamoto_user_identities_issuer_subject_key") {
		t.Errorf("duplicate (issuer, subject) error = %v, want unique violation", err)
	}
	must(ins, userB, "https://other.example.test", "sub-1")

	count := func() (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hatamoto_user_identities`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	must(`DELETE FROM hatamoto_users`)
	if n := count(); n != 0 {
		t.Errorf("after user delete: rows = %d, want 0", n)
	}
}
