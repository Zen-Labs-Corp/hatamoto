package server

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/email"
)

// maxExportMailError caps the engine's first error in the mail, in runes.
const maxExportMailError = 200

// exportFailNotifier mails the owners once per failure streak: when a run
// fails and the finished run before it did not. The streak is read from the
// runs table, so it survives restarts. It does not import the engine; the
// wiring supplies the func fields.
type exportFailNotifier struct {
	configured func() bool
	owners     func(ctx context.Context) ([]string, error)
	site       func(ctx context.Context) (name, base, adminPath string)
	location   func(ctx context.Context) *time.Location
	// previousFailed: did the newest finished run before `current` fail? A
	// run with no earlier finished run counts as not failed.
	previousFailed func(ctx context.Context, current uuid.UUID) (bool, error)
	send           func(ctx context.Context, to string, d email.StaticExportFailedData) error
	log            *slog.Logger
}

// RunFinished is called after every export. runID is the run's row
// (uuid.Nil = no row was written, e.g. another process held the run: nothing
// to report). It never fails the caller.
func (n *exportFailNotifier) RunFinished(ctx context.Context, runID uuid.UUID, ok bool, at time.Time, firstErr string) {
	if n == nil || ok || runID == uuid.Nil || !n.configured() {
		return
	}
	prev, err := n.previousFailed(ctx, runID)
	if err != nil {
		n.log.Warn("static export: failure notice skipped, previous run unreadable", slog.Any("error", err))
		return
	}
	if prev {
		return // this streak was already mailed
	}
	name, base, adminPath := n.site(ctx)
	if strings.TrimSpace(base) == "" {
		n.log.Warn("static export: failure notice skipped, no site address")
		return
	}
	owners, err := n.owners(ctx)
	if err != nil {
		n.log.Warn("static export: failure notice skipped, owners unreadable", slog.Any("error", err))
		return
	}
	loc := time.UTC
	if n.location != nil {
		if l := n.location(ctx); l != nil {
			loc = l
		}
	}
	msg := firstErr
	if r := []rune(msg); len(r) > maxExportMailError {
		msg = strings.TrimSpace(string(r[:maxExportMailError])) + "…"
	}
	d := email.StaticExportFailedData{
		SiteName:   name,
		When:       at.In(loc).Format("2 Jan 2006 15:04"),
		Error:      msg,
		HostingURL: strings.TrimRight(base, "/") + adminPath + "/settings/hosting",
	}
	failed := 0
	for _, to := range owners {
		if err := n.send(ctx, to, d); err != nil {
			failed++
		}
	}
	if failed > 0 {
		n.log.Warn("static export: failure notice not sent to every owner", "failed", failed, "owners", len(owners))
	}
}
