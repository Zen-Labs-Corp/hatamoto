package server

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// warnCommentsWithoutAddress logs one Warn when comments are on and no site address is
// configured: confirmation mails are then skipped. A settings read error logs nothing.
// It reports whether it logged.
func warnCommentsWithoutAddress(ctx context.Context, log *slog.Logger, enabled func(context.Context) (bool, error), base func(context.Context) string) bool {
	on, err := enabled(ctx)
	if err != nil || !on || strings.TrimSpace(base(ctx)) != "" {
		return false
	}
	log.Warn("comments are on but no site address is set: confirmation emails are not sent",
		"fix", "set PUBLIC_BASE_URL or Settings > General > Site address")
	return true
}

// warnCommentsWithoutAddressAtBoot runs once from setup (once per boot, never per request).
func (s *Server) warnCommentsWithoutAddressAtBoot() {
	if s.comments == nil || s.logger == nil {
		return
	}
	// A short deadline keeps a slow database from stalling boot.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	warnCommentsWithoutAddress(ctx, s.logger,
		func(ctx context.Context) (bool, error) { set, err := s.comments.Settings(ctx); return set.Enabled, err },
		func(ctx context.Context) string { _, b, _ := s.digestSite(ctx); return b })
}
