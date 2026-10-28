package server

import (
	"context"
	"math/rand/v2"
	"time"

	"hatamoto.dev/core/internal/installstats"
)

// statsJob is the worker job name WORKER_DISABLE_JOBS uses for the install
// stats ping.
const statsJob = "stats_ping"

// SetStats hands the install stats service to the server. Call it before
// Start. HATAMOTO_TELEMETRY=off is applied here, once, even when the worker is
// off, so a stored install ID is deleted at boot. The daily job is scheduled
// when the worker is on.
func (s *Server) SetStats(svc *installstats.Service) {
	s.stats = svc
	if svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Enforce(ctx); err != nil {
		s.logger.Warn("install stats: could not apply HATAMOTO_TELEMETRY", "error", err)
	}
	s.scheduleStats()
}

// scheduleStats puts the ping on the worker scheduler: once a day, the first
// one 5 to 15 minutes after start. Nothing is scheduled in a public demo or
// when WORKER_DISABLE_JOBS names the job. The job itself sends only with the
// owner's consent. It reports whether the job was scheduled.
func (s *Server) scheduleStats() bool {
	if s.stats == nil || s.scheduler == nil || s.demoOn() || !workerJobEnabled(statsJob) {
		return false
	}
	first := 5*time.Minute + time.Duration(rand.Int64N(int64(10*time.Minute)))
	if err := s.scheduler.ScheduleJitter(statsJob, 24*time.Hour, time.Hour, first, s.stats.Run); err != nil {
		s.logger.Warn("install stats: job not scheduled", "error", err)
		return false
	}
	return true
}
