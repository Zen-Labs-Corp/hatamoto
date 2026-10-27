//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
)

const smtpSettingsVersion = 202610251100

func TestSMTPSettings_UpDownUp(t *testing.T) {
	ctx := context.Background()
	db, _ := scratchDB(t)
	p := provider(t, db)

	exists := func() bool {
		t.Helper()
		var reg sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('hatamoto_smtp_settings')::text`).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		return reg.Valid
	}

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !exists() {
		t.Fatal("hatamoto_smtp_settings missing after up")
	}

	ins := `INSERT INTO hatamoto_smtp_settings (host, port, security, from_address) VALUES ('smtp.example.com', 587, 'starttls', 'a@example.com')`
	if _, err := db.ExecContext(ctx, ins); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	// Single row: a second insert (id defaults to true) must conflict, and id=false is refused.
	if _, err := db.ExecContext(ctx, ins); err == nil {
		t.Error("second row accepted, want primary key conflict")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO hatamoto_smtp_settings (id, host, port, security, from_address) VALUES (false, 'h', 1, 'none', 'a@b.c')`); err == nil {
		t.Error("id=false accepted, want check violation")
	}
	var provider, region string
	if err := db.QueryRowContext(ctx, `SELECT provider, ses_region FROM hatamoto_smtp_settings WHERE id`).Scan(&provider, &region); err != nil || provider != "custom" || region != "" {
		t.Errorf("defaults = %q/%q (%v)", provider, region, err)
	}

	if _, err := p.DownTo(ctx, smtpSettingsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if exists() {
		t.Fatal("hatamoto_smtp_settings still present after down")
	}
	if _, err := p.UpTo(ctx, smtpSettingsVersion); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !exists() {
		t.Fatal("hatamoto_smtp_settings missing after re-up")
	}
}
