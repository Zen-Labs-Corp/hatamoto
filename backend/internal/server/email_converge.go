package server

import (
	"context"
	"time"

	"hatamoto.dev/core/internal/idle"
)

// emailConvergeEvery is how often each replica checks whether another one
// changed the saved SMTP settings.
const emailConvergeEvery = 30 * time.Second

// runEmailConvergence re-applies the email configuration when the saved row
// changed (saved, replaced or cleared) since this replica last resolved it, so
// a change made on one replica reaches the others without a restart. It runs
// until ctx ends.
// Once the install has been idle for a while the check moves to the shared
// idle beat (see package idle); the next request wakes it at once.
func (s *Server) runEmailConvergence(ctx context.Context, every time.Duration) {
	loopEmailConvergence(ctx, s.idle, every, s.syncEmail)
}

// loopEmailConvergence calls sync after every wait through the gate (nil gate:
// a plain timer of every).
func loopEmailConvergence(ctx context.Context, g *idle.Gate, every time.Duration, sync func(context.Context)) {
	for g.Wait(ctx, every, nil) {
		sync(ctx)
	}
}

// syncEmail applies the saved settings again if their updated_at differs from
// what the live configuration was resolved from. A read error changes nothing.
func (s *Server) syncEmail(ctx context.Context) {
	set, found, err := s.mailStore.Get(ctx)
	if err != nil {
		return
	}
	var savedAt time.Time
	if found {
		savedAt = set.UpdatedAt
	}
	if !savedAt.Equal(s.currentEmailResolution().SavedAt) {
		s.applyEmail(ctx)
	}
}
