//go:build integration

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/user"
)

// Settings > Email through the whole server: Save checks the connection and
// takes effect without a restart, the password never reaches a page, and
// "Use the server's SMTP_* settings instead" hands sending back to the env.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run EmailLive
func TestEmailLiveSaveTestAndReset_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("EMAIL_DISABLED", "")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	// The env fallback points at an address nothing listens on, so a send
	// that reaches the sink can only have come from the saved settings.
	t.Setenv("SMTP_HOST", "env-smtp.invalid")
	t.Setenv("SMTP_PORT", "2525")
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
	// Start from, and leave behind, "nothing saved".
	clearRow := func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_smtp_settings`) }
	clearRow()
	t.Cleanup(func() { clearRow(); srv.applyEmail(context.Background()) })
	srv.applyEmail(ctx)

	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "owner-" + uuid.NewString()[:6] + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner)
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})

	csrf := adminToken(t, srv.router, token)
	admin := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		adminReq(req, token, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}

	const password = "sink-p4ss-SECRET-0123456789"
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain, User: "sink-user", Pass: password})
	form := func(pass string) url.Values {
		return url.Values{
			"provider": {"custom"}, "host": {sink.Host()}, "port": {strconv.Itoa(sink.Port())}, "security": {"none"},
			"username": {"sink-user"}, "password": {pass}, "from": {"hello@example.test"}, "from-name": {"Live IT"},
		}
	}
	rowCount := func() int {
		var n int
		if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_smtp_settings`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A wrong password is refused with the SMTP error and nothing is stored (A2).
	rec := admin("POST", "/admin/settings/email", form("wrong-password"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Nothing saved.") || rowCount() != 0 {
		t.Fatalf("wrong password: %d rows=%d\n%s", rec.Code, rowCount(), rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "wrong-password") {
		t.Error("the page echoes the typed password")
	}

	// The right one saves, with no restart (A1).
	rec = admin("POST", "/admin/settings/email", form(password))
	if rec.Code != http.StatusSeeOther || rowCount() != 1 {
		t.Fatalf("save: %d rows=%d\n%s", rec.Code, rowCount(), rec.Body.String())
	}
	var sealed []byte
	if err := db.Pool().QueryRow(ctx, `SELECT password_sealed FROM hatamoto_smtp_settings`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || strings.Contains(string(sealed), password) {
		t.Error("the password is not stored sealed")
	}
	page := admin("GET", "/admin/settings/email", nil).Body.String()
	for _, want := range []string{"Email is set up", "Using: settings saved here", sink.Host()} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, password) {
		t.Error("the page renders the password")
	}
	if got := srv.emailService.Config().SMTPHost; got != sink.Host() {
		t.Fatalf("live host = %q, want the sink without a restart", got)
	}

	// "Send a test email" delivers to the sink.
	before := len(sink.Messages())
	if rec := admin("POST", "/admin/settings/email/test", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("test email: %d", rec.Code)
	}
	if got := len(sink.Messages()); got != before+1 {
		t.Fatalf("sink got %d messages, want %d", got, before+1)
	}

	// Saving again with the password left blank keeps it (host and username unchanged).
	if rec := admin("POST", "/admin/settings/email", form("")); rec.Code != http.StatusSeeOther {
		t.Fatalf("blank password keep: %d\n%s", rec.Code, rec.Body.String())
	}

	// Reset clears the row and hands sending back to the server's SMTP_* (A3).
	if rec := admin("POST", "/admin/settings/email/reset", url.Values{}); rec.Code != http.StatusSeeOther || rowCount() != 0 {
		t.Fatalf("reset: %d rows=%d", rec.Code, rowCount())
	}
	if got := srv.emailService.Config().SMTPHost; got != "env-smtp.invalid" {
		t.Fatalf("live host after reset = %q, want the env host", got)
	}
	page = admin("GET", "/admin/settings/email", nil).Body.String()
	if !strings.Contains(page, "Using: the server&#39;s SMTP_* settings") || strings.Contains(page, "SMTP_* settings instead") {
		t.Error("page does not show the server's settings as the source after reset")
	}
}
