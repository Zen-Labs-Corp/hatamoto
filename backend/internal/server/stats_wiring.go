package server

import (
	"context"
	"math/rand/v2"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/middleware"
)

// statsJob is the worker job name WORKER_DISABLE_JOBS uses for the install
// stats ping.
const statsJob = "stats_ping"

// statsCheckEvery is how often the job asks to send. The store's claim allows
// one send per 20 hours, so most runs do nothing; checking hourly keeps a
// restart (an update) from pushing the next report a full day out.
const statsCheckEvery = time.Hour

// statsFlushEvery is how often the in-memory health counts go to the database
// (at most this much is lost to a crash; a send flushes first too).
var statsFlushEvery = 5 * time.Minute

// healthSink is the install stats health collector; nil-safe to call into
// before SetStats.
func (s *Server) healthSink() *installstats.Health { return s.health.Load() }

// recoveryChain is the outermost logging middleware: the 5xx counter outside
// the panic recovery, so the 500 written for a recovered panic is counted too.
func (s *Server) recoveryChain() []httpx.Middleware {
	return []httpx.Middleware{
		middleware.CountServerErrors(func() { s.healthSink().HTTP5xx() }),
		middleware.RecoveryLogger(s.logger, s.redactLogPath, middleware.OnPanic(func() { s.healthSink().Panic() })),
	}
}

// SetStats hands the install stats service to the server. Call it before
// Start. HATAMOTO_TELEMETRY=off is applied here, once, even when the worker is
// off, so a stored install ID is deleted at boot. The daily job is scheduled
// when the worker is on.
func (s *Server) SetStats(svc *installstats.Service) {
	s.stats = svc
	if svc == nil {
		s.health.Store(nil)
		return
	}
	s.health.Store(svc.Health())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Enforce(ctx); err != nil {
		s.logger.Warn("install stats: could not apply HATAMOTO_TELEMETRY", "error", err)
	}
	if s.admin != nil {
		s.admin.SetStats(adminStats{svc})
	}
	s.scheduleStats()
	s.startStatsFlush()
}

// adminStats presents the install stats service to the admin.
type adminStats struct{ svc *installstats.Service }

// Until the admin takes a level (a later change), the three levels map onto
// the old yes/no choice: off is no, basic and detailed are yes.
func (a adminStats) View(ctx context.Context) (admin.StatsView, error) {
	v, err := a.svc.View(ctx)
	if err != nil {
		return admin.StatsView{}, err
	}
	consent := "yes"
	switch v.Level {
	case installstats.LevelUnset:
		consent = "unset"
	case installstats.LevelOff:
		consent = "no"
	}
	return admin.StatsView{Consent: consent, ForcedOff: v.ForcedOff, LastSent: v.LastSent}, nil
}

func (a adminStats) Unanswered(ctx context.Context) (bool, error) {
	k, err := a.svc.Banner(ctx)
	return k == installstats.BannerUnanswered, err
}
func (a adminStats) ForcedOff() bool { return a.svc.ForcedOff() }
func (a adminStats) Choose(ctx context.Context, yes bool) error {
	if yes {
		return a.svc.Choose(ctx, installstats.LevelBasic)
	}
	return a.svc.Choose(ctx, installstats.LevelOff)
}
func (a adminStats) Reset(ctx context.Context) error { return a.svc.Reset(ctx) }

// scheduleStats puts the ping on the worker scheduler: a check every hour
// (sending at most once a day), the first 5 to 15 minutes after start. Nothing is scheduled in a public demo or
// when WORKER_DISABLE_JOBS names the job. The job itself sends only with the
// owner's consent. It reports whether the job was scheduled.
func (s *Server) scheduleStats() bool {
	if s.stats == nil || s.scheduler == nil || s.demoOn() || !workerJobEnabled(statsJob) {
		return false
	}
	first := 5*time.Minute + time.Duration(rand.Int64N(int64(10*time.Minute)))
	if err := s.scheduler.ScheduleJitter(statsJob, statsCheckEvery, 10*time.Minute, first, s.stats.Run); err != nil {
		s.logger.Warn("install stats: job not scheduled", "error", err)
		return false
	}
	return true
}

// startStatsFlush moves the health counts into the database every
// statsFlushEvery until Shutdown, which flushes once more. It does not need
// the worker scheduler, and nothing runs in a public demo.
func (s *Server) startStatsFlush() {
	if s.stopStatsFlush != nil {
		s.stopStatsFlush()
		s.stopStatsFlush = nil
	}
	if s.stats == nil || s.demoOn() {
		return
	}
	svc, every := s.stats, statsFlushEvery
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	flush := func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := svc.Flush(ctx); err != nil {
			s.logger.Warn("install stats: could not store health counts", "error", err)
		}
	}
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				flush(ctx)
			}
		}
	}()
	s.stopStatsFlush = func() {
		cancel()
		<-done
		flush(context.Background())
	}
}
