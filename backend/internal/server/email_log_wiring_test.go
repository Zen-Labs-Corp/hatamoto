//go:build integration

package server

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/pluginhost"
)

// The server's email service leaves a log row per send, and a plugin's mail
// is labelled with the plugin id.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run EmailLog
func TestEmailLogWiring_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	t.Setenv("EMAIL_DISABLED", "")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("SMTP_HOST", sink.Host())
	t.Setenv("SMTP_PORT", strconv.Itoa(sink.Port()))
	t.Setenv("SMTP_FROM", "env@example.test")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	srv := New(cfg, db, quietLogger())
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}
	if srv.emailLog == nil || srv.emailService == nil {
		t.Fatal("the email log is not wired")
	}
	c := srv.emailService.Config()
	c.Security = email.SecurityNone
	srv.emailService.Reload(c)
	var mailer pluginhost.Mailer = srv.emailService
	if !mailer.IsConfigured() {
		t.Skip("the env sink did not configure the email service")
	}
	var before int64
	if err := db.Pool().QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM hatamoto_email_log`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_email_log WHERE id > $1`, before)
	})

	if err := mailer.SendPlugin(ctx, "acme-plugin", []string{"to@example.test"}, "Hello", "<p>hi</p>", "hi"); err != nil {
		t.Fatal(err)
	}
	rows, total, err := srv.emailLog.List(ctx, "", 50, 0)
	if err != nil || total == 0 {
		t.Fatalf("List = %d rows, %v", total, err)
	}
	r := rows[0]
	if r.ID <= before || r.Kind != "plugin" || r.Plugin != "acme-plugin" || r.Status != "sent" || r.Subject != "Hello" || r.Recipients[0] != "to@example.test" {
		t.Errorf("row = %+v", r)
	}
	if _, err := srv.emailLog.Purge(ctx); err != nil {
		t.Errorf("purge: %v", err)
	}
}
