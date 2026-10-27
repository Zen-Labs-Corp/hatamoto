package server

import (
	"io"
	"log/slog"
	"testing"

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
