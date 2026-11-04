//go:build integration

package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

func TestResetTwoFactor_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	ctx := database.WithSuperadmin(context.Background())
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	u, err := user.NewPostgresRepository(db).Create(ctx, &user.CreateUserInput{Email: "ana@example.test", Role: user.RoleOwner}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	st := twofactor.NewStore(db.Pool())
	if _, err := st.SaveTOTP(ctx, u.ID, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, [][]byte{{1}, {2}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runUserReset2FA(ctx, db, &userFlags{email: "ana@example.test"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() == "" {
		t.Error("no output")
	}
	if n, _ := st.RecoveryLeft(ctx, u.ID); n != 0 {
		t.Errorf("recovery codes left: %d", n)
	}
	if r, _ := st.GetTOTP(ctx, u.ID); r != nil {
		t.Error("totp left")
	}
	var audits int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_admin_audit WHERE action='user.2fa.reset' AND actor='cli'`).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("audit rows %d %v", audits, err)
	}
	if err := runUserReset2FA(ctx, db, &userFlags{email: "nobody@example.test"}, &out); err == nil {
		t.Error("unknown user accepted")
	}
}
