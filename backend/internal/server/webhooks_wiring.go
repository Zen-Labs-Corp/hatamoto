package server

import (
	"time"

	"hatamoto.dev/core/internal/webhook"
	"hatamoto.dev/core/internal/worker"
)

// Worker job names WORKER_DISABLE_JOBS uses for outgoing webhooks.
const (
	webhookDeliverJob = "webhook_deliver"
	webhookPruneJob   = "webhook_prune"
)

// setupWebhooks builds the webhook service and subscribes its enqueue
// listeners. Events are queued even when nothing delivers them (worker off),
// so nothing is lost; the rows wait, capped per subscription.
//
// It must run before setupPackages: the plugin import there reads the repo, and
// delivery only starts at Start, after the old plugin has been retired.
func (s *Server) setupWebhooks() {
	s.webhooks = webhook.NewService(s.db, s.logger)
	webhook.Register(s.hooks, s.webhooks.Repo(), s.logger)
	// Production never allows private targets or odd ports (Options{} only).
	s.webhookDeliverer = webhook.NewDeliverer(s.webhooks.Repo(), s.logger, webhook.Options{})
}

// scheduleWebhooks puts delivery and pruning on the worker scheduler, honouring
// WORKER_DISABLE_JOBS, and records whether anything will deliver.
func (s *Server) scheduleWebhooks() {
	if s.webhooks == nil || s.scheduler == nil {
		return
	}
	// A public demo never sends webhooks (its subscriptions cannot be made either).
	if workerJobEnabled(webhookDeliverJob) && !s.demoOn() {
		err := s.scheduler.ScheduleJitter(webhookDeliverJob, 10*time.Second, 2*time.Second, 5*time.Second, s.webhookDeliverer.Run)
		if err != nil {
			s.logger.Error("webhooks: delivery job not scheduled", "error", err)
		}
		// Under WORKER_DRAIN the scheduler starts no dynamic jobs, so nothing delivers.
		s.webhookDeliveryOn = err == nil && !worker.DrainMode()
	}
	if workerJobEnabled(webhookPruneJob) {
		if err := s.scheduler.ScheduleJitter(webhookPruneJob, time.Hour, 5*time.Minute, 2*time.Minute, s.webhooks.Repo().Prune); err != nil {
			s.logger.Error("webhooks: prune job not scheduled", "error", err)
		}
	}
}

// logWebhookDelivery says at startup when queued webhooks will not be sent.
func (s *Server) logWebhookDelivery() {
	if s.webhooks != nil && !s.webhookDeliveryOn {
		s.logger.Info("webhooks are queued but not delivered: the worker, WORKER_DRAIN or the webhook_deliver job is off")
	}
}
