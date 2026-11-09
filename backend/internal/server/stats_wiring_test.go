package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/worker"
)

// memStats is an in-memory installstats.Backend.
type memStats struct {
	mu    sync.Mutex
	st    installstats.State
	added installstats.Counts // everything AddHealth received
}

func (m *memStats) Load(context.Context) (installstats.State, error) { return m.st, nil }
func (m *memStats) Enable(_ context.Context, level installstats.Level) (installstats.State, error) {
	m.st = installstats.State{Consent: installstats.ConsentYes, Level: level, InstallID: "0123456789abcdef0123456789abcdef"}
	return m.st, nil
}
func (m *memStats) Disable(context.Context) error {
	m.st = installstats.State{Consent: installstats.ConsentNo, Level: installstats.LevelOff}
	return nil
}
func (m *memStats) Reset(context.Context) (installstats.State, error)   { return m.st, nil }
func (m *memStats) Claim(context.Context, string) (string, bool, error) { return "", false, nil }
func (m *memStats) AddHealth(_ context.Context, d installstats.Counts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added.Panics += d.Panics
	m.added.HTTP5xx += d.HTTP5xx
	m.added.RenderErrors += d.RenderErrors
	return nil
}
func (m *memStats) addedCounts() installstats.Counts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.added
}
func (m *memStats) RecordResult(context.Context, string, string, *installstats.Counts) error {
	return nil
}
func (m *memStats) RecordKeptDay(context.Context, string) error { return nil }
func (m *memStats) DismissNudge(context.Context) error          { return nil }
func (m *memStats) Banner(context.Context) (installstats.BannerKind, error) {
	return installstats.BannerNone, nil
}

func TestAdminStatsViewShowsLastSuccess(t *testing.T) {
	s, m := statsTestServer(false)
	ok := time.Date(2026, 10, 27, 4, 12, 0, 0, time.UTC)
	claimed := ok.Add(30 * time.Hour)
	m.st = installstats.State{Consent: installstats.ConsentYes, LastSentAt: &claimed, LastOKAt: &ok, LastStatus: "error: http 404"}
	v, err := adminStats{s.stats}.View(context.Background())
	if err != nil || v.LastSent == nil || !v.LastSent.Equal(ok) {
		t.Fatalf("LastSent = %v, err %v, want the last success %v", v.LastSent, err, ok)
	}
}

func statsTestServer(withScheduler bool) (*Server, *memStats) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: log}
	if withScheduler {
		s.scheduler = worker.NewScheduler(nil, log)
	}
	m := &memStats{}
	s.stats = installstats.New(installstats.Config{Store: m, Version: "v1.2.3", Log: log, Health: installstats.NewHealth()})
	return s, m
}

func TestStatsJobIsKnown(t *testing.T) {
	for _, j := range knownWorkerJobs {
		if j == "stats_ping" {
			return
		}
	}
	t.Fatal("knownWorkerJobs is missing stats_ping")
}

// The job checks hourly so a restart cannot push a report a day out; the
// store's 20-hour claim keeps it to one send a day.
func TestStatsJobChecksHourly(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	s, _ := statsTestServer(true)
	if !s.scheduleStats() {
		t.Fatal("job not scheduled")
	}
	for _, j := range s.scheduler.Jobs() {
		if j.ID == statsJob {
			if j.Every != time.Hour || j.Jitter >= 30*time.Minute {
				t.Fatalf("every %s jitter %s, want hourly with a small jitter", j.Every, j.Jitter)
			}
			return
		}
	}
	t.Fatal("stats_ping not registered")
}

func TestStatsJobHonoursDisableListAndWorker(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	if s, _ := statsTestServer(true); !s.scheduleStats() {
		t.Fatal("the job should be scheduled with the worker and no disabled jobs")
	}
	if s, _ := statsTestServer(false); s.scheduleStats() {
		t.Fatal("no worker, no job")
	}
	t.Setenv("WORKER_DISABLE_JOBS", "Stats_Ping")
	if s, _ := statsTestServer(true); s.scheduleStats() {
		t.Fatal("a disabled job was scheduled")
	}
	if got := disabledWorkerJobs(); len(got) != 1 || got[0] != "stats_ping" {
		t.Fatalf("disabled: %v", got)
	}
}

func TestStartupEnforcesTelemetryOff(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	t.Setenv("HATAMOTO_TELEMETRY", "off")
	s, m := statsTestServer(false) // the worker is off: the ID must go anyway
	_, _ = m.Enable(context.Background(), installstats.LevelBasic)
	s.SetStats(s.stats)
	if m.st.Consent != installstats.ConsentNo || m.st.InstallID != "" {
		t.Fatalf("env off at startup must clear the id: %+v", m.st)
	}
}

func TestStartupLeavesConsentAloneWhenNotOff(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	t.Setenv("HATAMOTO_TELEMETRY", "")
	s, m := statsTestServer(false)
	_, _ = m.Enable(context.Background(), installstats.LevelBasic)
	s.SetStats(s.stats)
	if m.st.Consent != installstats.ConsentYes || m.st.InstallID == "" {
		t.Fatalf("startup must not change consent: %+v", m.st)
	}
}

// serveThrough runs one request through the recovery chain around h.
func serveThrough(s *Server, h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	httpx.Chain(s.recoveryChain()...)(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec
}

func TestRecoveryChainCountsServerErrorsAndPanics(t *testing.T) {
	s, _ := statsTestServer(false)
	s.SetStats(s.stats)
	serveThrough(s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	serveThrough(s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	rec := serveThrough(s, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", rec.Code)
	}
	c := s.healthSink().Drain()
	if c.HTTP5xx != 2 || c.Panics != 1 { // the recovered panic is also a 500
		t.Fatalf("counts = %+v", c)
	}
}

func TestRecoveryChainBeforeSetStatsIsHarmless(t *testing.T) {
	s, _ := statsTestServer(false) // SetStats not called: no collector yet
	serveThrough(s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
}

type failingEngine struct{ pageRenderer }

func (failingEngine) RenderPage(io.Writer, *render.PageView) error { return errors.New("template") }
func (failingEngine) RenderError(io.Writer, *render.PageView, int) error {
	return errors.New("template")
}

func TestRenderFailureIsCounted(t *testing.T) {
	s, _ := statsTestServer(false)
	s.SetStats(s.stats)
	h := &renderHandlers{engine: failingEngine{}, logger: s.logger, renderFailed: func() { s.healthSink().RenderError() }}
	rec := httptest.NewRecorder()
	h.writeError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), http.StatusNotFound, &render.PageView{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if c := s.healthSink().Drain(); c.RenderErrors != 1 {
		t.Fatalf("counts = %+v", c)
	}
	(&renderHandlers{engine: failingEngine{}, logger: s.logger}).writeError(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/x", nil), http.StatusNotFound, &render.PageView{}) // no counter wired: no panic
}

func TestStatsFlushTickerFlushesAndStops(t *testing.T) {
	old := statsFlushEvery
	statsFlushEvery = 5 * time.Millisecond
	defer func() { statsFlushEvery = old }()
	s, m := statsTestServer(false)
	s.SetStats(s.stats)
	s.healthSink().Panic()
	deadline := time.Now().Add(5 * time.Second)
	for m.addedCounts().Panics == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.addedCounts().Panics != 1 {
		t.Fatalf("ticker never flushed: %+v", m.addedCounts())
	}
	s.healthSink().HTTP5xx()
	s.stopStatsFlush() // returns only once the goroutine is gone, and flushes what is left
	if m.addedCounts().HTTP5xx != 1 {
		t.Fatalf("shutdown did not flush: %+v", m.addedCounts())
	}
}

func TestStatsFlushNotStartedInDemo(t *testing.T) {
	s, _ := statsTestServer(false)
	s.cfg = &config.Config{}
	s.cfg.Demo.Enabled = true
	s.SetStats(s.stats)
	if s.stopStatsFlush != nil {
		t.Fatal("a public demo must not flush stats")
	}
}
