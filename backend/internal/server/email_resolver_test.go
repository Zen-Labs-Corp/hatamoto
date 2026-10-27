package server

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/mailsettings"
)

const testSecret = "a-server-secret-for-tests"

type fakeSettings struct {
	set   mailsettings.Settings
	found bool
	err   error
}

func (f *fakeSettings) Get(context.Context) (mailsettings.Settings, bool, error) {
	return f.set, f.found, f.err
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func savedFor(t *testing.T, srv *smtptest.Server, password string) mailsettings.Settings {
	t.Helper()
	sealed, err := mailsettings.SealPassword(mailsettings.PasswordKey(testSecret), password)
	if err != nil {
		t.Fatal(err)
	}
	return mailsettings.Settings{
		Provider: mailsettings.ProviderCustom, Host: srv.Host(), Port: srv.Port(),
		Security: email.SecurityNone, Username: "saved-user", PasswordSealed: sealed,
		FromAddress: "saved@example.com", UpdatedAt: time.Unix(1700000000, 0),
	}
}

// envFor points SMTP_* at srv. Env always uses STARTTLS, so srv must be a
// StartTLS sink.
func envFor(srv *smtptest.Server) config.SMTPConfig {
	return config.SMTPConfig{Host: srv.Host(), Port: srv.Port(), Username: "env-user", Password: "env-pass",
		From: "env@example.com", FromNameFromSite: true}
}

// resolveAndSend resolves the configuration into a fresh service and sends one
// message. roots lets the env path trust the sink's self-signed certificate.
func resolveAndSend(t *testing.T, env config.SMTPConfig, store smtpSettingsReader, roots *x509.CertPool) emailResolution {
	t.Helper()
	t.Setenv("EMAIL_DISABLED", "")
	cfg, res := resolveEmailConfig(context.Background(), quietLogger(), env, store, testSecret, "Site")
	cfg.RootCAs = roots
	svc, err := email.NewService(email.Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	svc.Reload(cfg)
	if err := svc.Send(context.Background(), []string{"to@example.com"}, "subject", "<p>x</p>", "x"); err != nil {
		t.Fatalf("send: %v", err)
	}
	return res
}

func TestSavedWinsOverEnvAndClearRestoresEnv(t *testing.T) {
	envSrv := smtptest.Start(t, smtptest.Options{Mode: smtptest.StartTLS, User: "env-user", Pass: "env-pass"})
	savedSrv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain, User: "saved-user", Pass: "saved-pass"})
	store := &fakeSettings{set: savedFor(t, savedSrv, "saved-pass"), found: true}

	res := resolveAndSend(t, envFor(envSrv), store, envSrv.RootCAs())
	if res.Source != emailSourceSaved || res.PasswordLost {
		t.Fatalf("resolution = %+v, want saved", res)
	}
	if len(savedSrv.Messages()) != 1 || len(envSrv.Messages()) != 0 {
		t.Fatalf("saved got %d, env got %d; want 1 and 0", len(savedSrv.Messages()), len(envSrv.Messages()))
	}
	if !res.SavedAt.Equal(store.set.UpdatedAt) {
		t.Fatalf("SavedAt = %v", res.SavedAt)
	}

	// "Use the server's settings": the row is gone, the next send reaches env.
	store.found = false
	res = resolveAndSend(t, envFor(envSrv), store, envSrv.RootCAs())
	if res.Source != emailSourceServer {
		t.Fatalf("resolution = %+v, want server", res)
	}
	if len(envSrv.Messages()) != 1 || len(savedSrv.Messages()) != 1 {
		t.Fatalf("env got %d, saved got %d; want 1 and 1", len(envSrv.Messages()), len(savedSrv.Messages()))
	}
}

func TestUnreadablePasswordFallsBackToEnv(t *testing.T) {
	envSrv := smtptest.Start(t, smtptest.Options{Mode: smtptest.StartTLS, User: "env-user", Pass: "env-pass"})
	savedSrv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain, User: "saved-user", Pass: "saved-pass"})
	saved := savedFor(t, savedSrv, "saved-pass")
	// Sealed under another secret, as after a SECRET rotation.
	other, err := mailsettings.SealPassword(mailsettings.PasswordKey("rotated-secret"), "saved-pass")
	if err != nil {
		t.Fatal(err)
	}
	saved.PasswordSealed = other

	res := resolveAndSend(t, envFor(envSrv), &fakeSettings{set: saved, found: true}, envSrv.RootCAs())
	if res.Source != emailSourceServer || !res.PasswordLost {
		t.Fatalf("resolution = %+v, want server + PasswordLost", res)
	}
	if len(envSrv.Messages()) != 1 || len(savedSrv.Messages()) != 0 {
		t.Fatalf("env got %d, saved got %d; want 1 and 0", len(envSrv.Messages()), len(savedSrv.Messages()))
	}
}

func TestSavedWithoutPasswordIsUsed(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	saved := savedFor(t, sink, "")
	saved.PasswordSealed, saved.Username = nil, ""

	cfg, res := resolveEmailConfig(context.Background(), quietLogger(), config.SMTPConfig{},
		&fakeSettings{set: saved, found: true}, testSecret, "Site")
	if res.Source != emailSourceSaved || res.PasswordLost || cfg.SMTPPassword != "" || !cfg.AllowLocalhost {
		t.Fatalf("cfg=%+v res=%+v", cfg, res)
	}
}

// ISC-29: no env host and a saved row: the service is configured from the row.
func TestSavedRowWorksWithEnvHostUnset(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain, User: "saved-user", Pass: "p"})
	store := &fakeSettings{set: savedFor(t, sink, "p"), found: true}

	cfg, res := resolveEmailConfig(context.Background(), quietLogger(), config.SMTPConfig{}, store, testSecret, "Site")
	svc, err := email.NewService(envEmailConfig(config.SMTPConfig{}), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if svc.IsConfigured() {
		t.Fatal("env-only service must start unconfigured")
	}
	svc.Reload(cfg)
	if res.Source != emailSourceSaved || !svc.IsConfigured() {
		t.Fatalf("res=%+v configured=%v", res, svc.IsConfigured())
	}
}

func TestEnvSourceNamesAndStoreError(t *testing.T) {
	for name, tc := range map[string]struct {
		env  config.SMTPConfig
		want emailSource
	}{
		"real host":     {config.SMTPConfig{Host: "smtp.example.com", Port: 587}, emailSourceServer},
		"empty host":    {config.SMTPConfig{}, emailSourceNone},
		"localhost env": {config.SMTPConfig{Host: "localhost", Port: 1025}, emailSourceNone},
	} {
		t.Run(name, func(t *testing.T) {
			_, res := resolveEmailConfig(context.Background(), quietLogger(), tc.env, &fakeSettings{}, testSecret, "Site")
			if res.Source != tc.want {
				t.Fatalf("source = %q, want %q", res.Source, tc.want)
			}
		})
	}

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg, res := resolveEmailConfig(context.Background(), logger,
		config.SMTPConfig{Host: "smtp.example.com", Port: 587}, &fakeSettings{err: errors.New("db down")}, testSecret, "Site")
	if res.Source != emailSourceServer || cfg.SMTPHost != "smtp.example.com" {
		t.Fatalf("store error must fall back to env: cfg=%+v res=%+v", cfg, res)
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("expected a warning, got %q", buf.String())
	}
}

// A saved blank From name follows the site name for all mail, as env does.
func TestSavedBlankFromNameFollowsSiteName(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	saved := savedFor(t, sink, "")
	saved.PasswordSealed, saved.Username, saved.FromName = nil, "", ""

	cfg, _ := resolveEmailConfig(context.Background(), quietLogger(), config.SMTPConfig{},
		&fakeSettings{set: saved, found: true}, testSecret, "Example Site")
	if cfg.FromName != "Example Site" || !cfg.FromNameFromSite {
		t.Fatalf("FromName = %q (fromSite %v), want the site name", cfg.FromName, cfg.FromNameFromSite)
	}
	svc, err := email.NewService(cfg, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if svc.FromName() != "Example Site" {
		t.Fatalf("service FromName() = %q", svc.FromName())
	}

	saved.FromName = "Custom"
	cfg, _ = resolveEmailConfig(context.Background(), quietLogger(), config.SMTPConfig{},
		&fakeSettings{set: saved, found: true}, testSecret, "Example Site")
	if cfg.FromName != "Custom" || cfg.FromNameFromSite {
		t.Fatalf("explicit name changed: %q", cfg.FromName)
	}
}

// An unknown saved security value makes the row unusable; it must not be
// sent as plain text.
func TestSavedUnknownSecurityFallsBackToEnv(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	for _, sec := range []email.Security{"bogus", ""} {
		saved := savedFor(t, sink, "")
		saved.PasswordSealed, saved.Username, saved.Security = nil, "", sec
		env := config.SMTPConfig{Host: "smtp.example.com", Port: 587, From: "env@example.com"}

		cfg, res := resolveEmailConfig(context.Background(), quietLogger(), env,
			&fakeSettings{set: saved, found: true}, testSecret, "Site")
		if res.Source != emailSourceServer || cfg.SMTPHost != "smtp.example.com" || cfg.Security != email.SecurityStarttls {
			t.Fatalf("security %q: cfg=%+v res=%+v, want env", sec, cfg, res)
		}
		if !res.SavedAt.Equal(saved.UpdatedAt) {
			t.Fatalf("SavedAt = %v; the convergence tick would re-apply every 30s", res.SavedAt)
		}
	}
}
