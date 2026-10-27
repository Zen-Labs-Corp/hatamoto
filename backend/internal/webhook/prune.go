package webhook

import "context"

// Prune keeps the delivery log bounded: delivered rows live 30 days, every
// other row 90 days (a pending row older than that is abandoned), and each
// subscription keeps at most LogPerSubscription finished rows.
func (r *Repo) Prune(ctx context.Context) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `DELETE FROM hatamoto_webhook_deliveries WHERE (status = 'delivered' AND created_at < now() - interval '30 days')
		OR (status <> 'delivered' AND created_at < now() - interval '90 days')`); err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `DELETE FROM hatamoto_webhook_deliveries WHERE id IN (
		SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY subscription_id ORDER BY created_at DESC) AS n
		                  FROM hatamoto_webhook_deliveries WHERE status <> 'pending') ranked WHERE n > $1)`, LogPerSubscription)
	return err
}
