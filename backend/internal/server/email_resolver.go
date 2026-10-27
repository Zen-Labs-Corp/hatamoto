package server

import (
	"context"
	"log/slog"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/mailsettings"
)

// emailSource names where the live email configuration comes from.
type emailSource string

const (
	// emailSourceSaved: settings an owner saved in Settings > Email.
	emailSourceSaved emailSource = "saved"
	// emailSourceServer: the server's SMTP_* environment.
	emailSourceServer emailSource = "server"
	// emailSourceNone: nothing usable is configured.
	emailSourceNone emailSource = "none"
)

// emailResolution describes how the live email configuration was chosen.
type emailResolution struct {
	Source emailSource
	// PasswordLost: a saved row exists but its password cannot be opened
	// (the server SECRET changed), so the environment was used instead.
	PasswordLost bool
	// SavedAt is the saved row's updated_at; zero when there is no row or it
	// could not be read. The replica convergence check compares it.
	SavedAt time.Time
	// ReadFailed: the saved row could not be read, so the environment was
	// resolved as a stand-in. applyEmail keeps the live configuration instead
	// once one has been applied.
	ReadFailed bool
}

// smtpSettingsReader is the part of mailsettings.Store the resolver needs.
type smtpSettingsReader interface {
	Get(ctx context.Context) (mailsettings.Settings, bool, error)
}

// smtpSettingsStore is the saved-settings store the server and the admin
// adapter use; *mailsettings.Store satisfies it.
type smtpSettingsStore interface {
	smtpSettingsReader
	Save(ctx context.Context, set mailsettings.Settings) error
	Clear(ctx context.Context) error
}

// envEmailConfig is the configuration the server's SMTP_* environment gives.
func envEmailConfig(env config.SMTPConfig) email.Config {
	return email.Config{
		SMTPHost:         env.Host,
		SMTPPort:         env.Port,
		SMTPUsername:     env.Username,
		SMTPPassword:     env.Password,
		FromAddress:      env.From,
		FromName:         env.FromName,
		FromNameFromSite: env.FromNameFromSite,
		Security:         email.SecurityStarttls,
	}
}

// resolveEmailConfig picks the email configuration: saved admin settings
// replace the environment as a whole; the environment is the fallback when
// nothing usable is saved (no row, a read error, or a password the current
// secret cannot open).
func resolveEmailConfig(ctx context.Context, logger *slog.Logger, env config.SMTPConfig,
	store smtpSettingsReader, secret, siteName string) (email.Config, emailResolution) {
	envCfg := envEmailConfig(env)
	envRes := emailResolution{Source: emailSourceNone}
	if env.Host != "" && env.Host != "localhost" {
		envRes.Source = emailSourceServer
	}

	set, found, err := store.Get(ctx)
	if err != nil {
		logger.Warn("could not read saved SMTP settings; using the server's SMTP_* settings",
			slog.String("error", err.Error()))
		envRes.ReadFailed = true
		return envCfg, envRes
	}
	if !found {
		return envCfg, envRes
	}
	envRes.SavedAt = set.UpdatedAt

	if set.Security == "" || !set.Security.Valid() {
		// A hand-edited row or a newer replica's value: not usable, and never
		// to be read as plain text.
		logger.Warn("saved SMTP settings have an unknown security value; using the server's SMTP_* settings",
			slog.String("security", string(set.Security)))
		return envCfg, envRes
	}

	var password string
	if set.HasPassword() {
		var ok bool
		if password, ok = set.Password(mailsettings.PasswordKey(secret)); !ok {
			logger.Warn("saved SMTP password cannot be read (the server SECRET changed); re-enter it in Settings > Email. Using the server's SMTP_* settings meanwhile")
			envRes.PasswordLost = true
			return envCfg, envRes
		}
	}
	cfg := set.EmailConfig(password)
	if cfg.FromName == "" {
		// Same as the env path: a blank From name is the site name, for every
		// mail (forms, confirmations, plugins), not only the admin's own.
		cfg.FromName = siteName
	}
	return cfg, emailResolution{Source: emailSourceSaved, SavedAt: set.UpdatedAt}
}

// applyEmail resolves the email configuration and swaps it into the shared
// service in place, so the admin, plugins and form mailers all see it. One
// mutex covers the read, the Reload and the recorded resolution, so an admin
// save and the convergence tick cannot leave an older row live under a newer
// timestamp. When the saved row cannot be read after a configuration was
// already applied, the live configuration is kept rather than dropping to env.
func (s *Server) applyEmail(ctx context.Context) emailResolution {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	cfg, res := resolveEmailConfig(ctx, s.logger, s.cfg.SMTP, s.mailStore, s.cfg.Secret, s.cfg.Hatamoto.SiteName)
	s.emailMu.Lock()
	defer s.emailMu.Unlock()
	if res.ReadFailed && s.emailApplied {
		return s.emailRes
	}
	s.emailService.Reload(cfg)
	s.emailRes, s.emailApplied = res, true
	return res
}

// currentEmailResolution returns how the live configuration was last chosen.
func (s *Server) currentEmailResolution() emailResolution {
	s.emailMu.Lock()
	defer s.emailMu.Unlock()
	return s.emailRes
}
