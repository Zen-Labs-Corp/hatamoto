package server

import (
	"context"
	"log/slog"
	"time"
)

// warnUnsendableNotifications logs a loud warning at boot when a production
// deploy has forms with a notification email but no usable SMTP host: every
// notification would be dropped silently. smtpConfigured is judged on the
// resolved configuration (saved settings or SMTP_*). It never blocks boot.
func warnUnsendableNotifications(ctx context.Context, logger *slog.Logger, environment string, smtpConfigured bool,
	countForms func(context.Context) (int, error)) {
	if environment != "production" || smtpConfigured {
		return
	}
	n, err := countForms(ctx)
	if err != nil || n == 0 {
		return
	}
	logger.Warn("email is not set up (Settings > Email or SMTP_HOST) but forms have notification emails: form notifications and confirmations will NOT be sent",
		slog.Int("forms_with_notify_email", n))
}

// countFormsWithNotifyEmail counts forms whose settings name a notify_email.
func (s *Server) countFormsWithNotifyEmail(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var n int
	err := s.db.Pool().QueryRow(ctx,
		`SELECT count(*) FROM hatamoto_forms WHERE COALESCE(settings->>'notify_email', '') <> ''`).Scan(&n)
	return n, err
}
