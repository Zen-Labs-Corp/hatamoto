package server

import (
	"context"

	"hatamoto.dev/core/internal/webhook"
)

// adminWebhooks is the webhook service plus what only the deliverer can do,
// as admin.WebhookStore uses it.
type adminWebhooks struct {
	*webhook.Service
	deliverer *webhook.Deliverer
}

// SendTest sends a test delivery now and returns its recorded outcome.
func (a adminWebhooks) SendTest(ctx context.Context, id string) (*webhook.DeliveryRow, error) {
	deliveryID, err := a.deliverer.SendTest(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.Repo().GetDelivery(ctx, deliveryID)
}

// ListDeliveries returns one page of the delivery log (one subscription, or
// all when subscriptionID is empty) and the total number of rows.
func (a adminWebhooks) ListDeliveries(ctx context.Context, subscriptionID string, limit, offset int) ([]webhook.DeliveryRow, int, error) {
	rows, err := a.Repo().ListDeliveries(ctx, subscriptionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := a.Repo().CountDeliveries(ctx, subscriptionID)
	return rows, total, err
}
