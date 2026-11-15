package server

import (
	"context"
	"errors"
	"hatamoto.dev/core/internal/idle"
	"io"
	"log/slog"
	"testing"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/worker"
	"hatamoto.dev/sdk"
)

func webhookTestServer(withScheduler bool) *Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{logger: log, hooks: hooks.New(log)}
	if withScheduler {
		s.scheduler = worker.NewScheduler(nil, log)
	}
	s.setupWebhooks()
	return s
}

func TestWebhookJobsAreKnown(t *testing.T) {
	have := map[string]bool{}
	for _, j := range knownWorkerJobs {
		have[j] = true
	}
	for _, want := range []string{webhookDeliverJob, webhookPruneJob} {
		if !have[want] {
			t.Errorf("knownWorkerJobs missing %q", want)
		}
	}
}

func TestWebhookDeliveryHonoursDisableList(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	s := webhookTestServer(true)
	s.scheduleWebhooks()
	if !s.webhookDeliveryOn {
		t.Fatal("delivery should be on with the worker and no disabled jobs")
	}

	t.Setenv("WORKER_DISABLE_JOBS", "Webhook_Deliver")
	s = webhookTestServer(true)
	s.scheduleWebhooks()
	if s.webhookDeliveryOn {
		t.Fatal("webhook_deliver is disabled but delivery is on")
	}
	if got := disabledWorkerJobs(); len(got) != 1 || got[0] != webhookDeliverJob {
		t.Fatalf("disabled: %v", got)
	}
}

func TestWebhookDrainModeCountsAsDeliveryOff(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	t.Setenv("WORKER_DRAIN", "true")
	s := webhookTestServer(true)
	s.scheduleWebhooks()
	if s.webhookDeliveryOn {
		t.Fatal("WORKER_DRAIN=true starts no jobs, yet delivery is reported on")
	}
}

func TestWebhookWorkerOffKeepsHooksRegistered(t *testing.T) {
	t.Setenv("WORKER_DISABLE_JOBS", "")
	s := webhookTestServer(false) // HATAMOTO_WORKER=off: no scheduler
	s.scheduleWebhooks()
	if s.webhookDeliveryOn {
		t.Fatal("no scheduler, yet delivery is on")
	}
	if s.webhooks == nil || s.webhookDeliverer == nil {
		t.Fatal("service and deliverer must exist so events are still queued")
	}
	if !s.hooks.HasAction(sdk.EntityPublished.Name) {
		t.Fatal("entity.published has no listener with the worker off")
	}
}

type fakeEnqueuer struct {
	n   int
	err error
}

func (f fakeEnqueuer) Enqueue(context.Context, string, any, time.Time) (int, error) {
	return f.n, f.err
}

func TestKickingEnqueuerWakesIdleDelivery(t *testing.T) {
	for _, c := range []struct {
		name string
		enq  fakeEnqueuer
		want bool
	}{
		{"queued", fakeEnqueuer{n: 1}, true},
		{"nothing queued", fakeEnqueuer{}, false},
		{"error", fakeEnqueuer{err: errors.New("db down")}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			now := time.Unix(1_000, 0)
			g := idle.New(time.Minute, 5*time.Minute, func() time.Time { return now })
			woke := make(chan struct{})
			g.SetTimer(func(time.Duration) (<-chan time.Time, func()) { close(woke); return make(chan time.Time), func() {} })
			now = now.Add(2 * time.Minute)
			done := make(chan bool, 1)
			go func() { done <- g.Wait(context.Background(), time.Second, nil) }()
			<-woke // the loop is parked on the beat
			kickingEnqueuer{c.enq, g}.Enqueue(context.Background(), "e", nil, now)
			select {
			case <-done:
				if !c.want {
					t.Fatal("woke without a queued delivery")
				}
			case <-time.After(50 * time.Millisecond):
				if c.want {
					t.Fatal("a queued delivery did not wake the idle loop")
				}
			}
		})
	}
}
