package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/worker"
)

// memStats is an in-memory installstats.Backend.
type memStats struct{ st installstats.State }

func (m *memStats) Load(context.Context) (installstats.State, error) { return m.st, nil }
func (m *memStats) Enable(context.Context) (installstats.State, error) {
	m.st = installstats.State{Consent: installstats.ConsentYes, InstallID: "0123456789abcdef0123456789abcdef"}
	return m.st, nil
}
func (m *memStats) Disable(context.Context) error {
	m.st = installstats.State{Consent: installstats.ConsentNo}
	return nil
}
func (m *memStats) Reset(context.Context) (installstats.State, error) { return m.st, nil }
func (m *memStats) Claim(context.Context) (string, bool, error)       { return "", false, nil }
func (m *memStats) RecordResult(context.Context, string) error        { return nil }

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
	s.stats = installstats.New(installstats.Config{Store: m, Version: "v1.2.3", Log: log})
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
	_, _ = m.Enable(context.Background())
	s.SetStats(s.stats)
	if m.st.Consent != installstats.ConsentNo || m.st.InstallID != "" {
		t.Fatalf("env off at startup must clear the id: %+v", m.st)
	}
}

func TestStartupLeavesConsentAloneWhenNotOff(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	t.Setenv("HATAMOTO_TELEMETRY", "")
	s, m := statsTestServer(false)
	_, _ = m.Enable(context.Background())
	s.SetStats(s.stats)
	if m.st.Consent != installstats.ConsentYes || m.st.InstallID == "" {
		t.Fatalf("startup must not change consent: %+v", m.st)
	}
}
