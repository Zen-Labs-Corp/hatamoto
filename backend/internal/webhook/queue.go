package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Delivery is a claimed row: what an attempt needs.
type Delivery struct {
	ID             string
	SubscriptionID string
	Event          string
	Payload        string
	Status         string
	Attempts       int
	MaxAttempts    int
}

// DeliveryRow is a row of the delivery log. It never carries the payload.
type DeliveryRow struct {
	ID             string
	SubscriptionID string
	Event          string
	Status         string
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	ResponseStatus *int
	ResponseBody   string
	Error          string
	DurationMS     *int
	CreatedAt      time.Time
	LastAttemptAt  *time.Time
	DeliveredAt    *time.Time
}

// Enqueue queues event for every active subscription listening to it, one
// delivery row (with its own envelope id) each, and returns how many rows it
// added. It only touches the database: delivery is the job's business. A
// subscription whose backlog has reached MaxPending gets no more.
func (r *Repo) Enqueue(ctx context.Context, event string, data any, now time.Time) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT s.id, s.max_retries FROM hatamoto_webhooks s
		WHERE s.active AND s.events @> $1::jsonb
		  AND (SELECT count(*) FROM hatamoto_webhook_deliveries d WHERE d.subscription_id = s.id AND d.status = 'pending') < $2`,
		[]string{event}, MaxPending)
	if err != nil {
		return 0, err
	}
	var ids, subs, payloads []string
	var maxes []int32
	for rows.Next() {
		var sub string
		var retries int
		if err := rows.Scan(&sub, &retries); err != nil {
			rows.Close()
			return 0, err
		}
		id := uuid.NewString()
		body, err := NewEnvelope(id, event, now, data)
		if err != nil {
			rows.Close()
			return 0, err
		}
		ids, subs, payloads, maxes = append(ids, id), append(subs, sub), append(payloads, string(body)), append(maxes, int32(retries+1))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	// One statement for the whole fan-out; a subscription deleted meanwhile is skipped.
	tag, err := conn.Exec(ctx, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, max_attempts)
		SELECT u.id, u.sub, $1, u.payload, u.max
		  FROM unnest($2::text[], $3::text[], $4::text[], $5::int[]) AS u(id, sub, payload, max)
		 WHERE EXISTS (SELECT 1 FROM hatamoto_webhooks w WHERE w.id = u.sub)`,
		event, ids, subs, payloads, maxes)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// InsertParked stores a delivery that no job will pick up for the next park
// (Send test attempts its row inline, so a crash never leaves it due).
func (r *Repo) InsertParked(ctx context.Context, id, subscriptionID, event, payload string, maxAttempts int, park time.Duration) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `INSERT INTO hatamoto_webhook_deliveries (id, subscription_id, event, payload, max_attempts, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, now() + make_interval(secs => $6))`,
		id, subscriptionID, event, payload, maxAttempts, park.Seconds())
	return err
}

// DBNow is the database clock, which decides what is due (not the app's).
func (r *Repo) DBNow(ctx context.Context) (time.Time, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Release()
	var t time.Time
	err = conn.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t, err
}

// SettleDead counts the attempt of every delivery whose lease ran out: the
// process that claimed it died, possibly after the receiver got the request,
// so the attempt is spent. A delivery that keeps killing its attempt ends up failed.
func (r *Repo) SettleDead(ctx context.Context) (int64, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `UPDATE hatamoto_webhook_deliveries SET attempts = attempts + 1, leased_until = NULL, last_attempt_at = now(),
		error = 'the attempt did not finish (the delivery call timed out or stopped)',
		status = CASE WHEN attempts + 1 >= max_attempts THEN 'failed' ELSE 'pending' END, next_attempt_at = now()
		WHERE status = 'pending' AND leased_until IS NOT NULL AND leased_until <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClaimOne leases the oldest due delivery of a subscription not in skip, or
// returns nil. Only rows due at runStart qualify, so a delivery that failed
// with no backoff is not tried again in the same run. Claiming is atomic
// (FOR UPDATE SKIP LOCKED), so concurrent runs never send one delivery twice,
// and one row at a time, so a run that stops early leaves nothing leased.
func (r *Repo) ClaimOne(ctx context.Context, runStart time.Time, skip []string) (*Delivery, error) {
	if skip == nil {
		skip = []string{}
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var d Delivery
	err = conn.QueryRow(ctx, `WITH next AS (
		  SELECT id FROM hatamoto_webhook_deliveries
		   WHERE status = 'pending' AND leased_until IS NULL AND next_attempt_at <= $1 AND subscription_id <> ALL($2::text[])
		   ORDER BY next_attempt_at, created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		UPDATE hatamoto_webhook_deliveries d SET leased_until = now() + make_interval(secs => $3)
		  FROM next WHERE d.id = next.id
		RETURNING d.id, d.subscription_id, d.event, d.payload, d.status, d.attempts, d.max_attempts`,
		runStart, skip, LeaseSeconds).Scan(&d.ID, &d.SubscriptionID, &d.Event, &d.Payload, &d.Status, &d.Attempts, &d.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// MarkDelivered records a 2xx answer as the next attempt.
func (r *Repo) MarkDelivered(ctx context.Context, id string, status int, body string, dur time.Duration) error {
	return r.update(ctx, `UPDATE hatamoto_webhook_deliveries SET status = 'delivered', attempts = attempts + 1, response_status = $2,
		response_body = $3, error = NULL, duration_ms = $4, leased_until = NULL, last_attempt_at = now(), delivered_at = now() WHERE id = $1`,
		id, status, body, dur.Milliseconds())
}

// MarkRetry records a failed attempt and schedules the next one after next.
// status is nil when the receiver gave no answer.
func (r *Repo) MarkRetry(ctx context.Context, id string, status *int, body, errMsg string, dur, next time.Duration) error {
	return r.update(ctx, `UPDATE hatamoto_webhook_deliveries SET attempts = attempts + 1, response_status = $2, response_body = $3,
		error = $4, duration_ms = $5, leased_until = NULL, last_attempt_at = now(),
		next_attempt_at = clock_timestamp() + make_interval(secs => $6) WHERE id = $1`,
		id, status, body, errMsg, dur.Milliseconds(), next.Seconds())
}

// MarkExhausted records the last failed attempt: the delivery is failed.
func (r *Repo) MarkExhausted(ctx context.Context, id string, status *int, body, errMsg string, dur time.Duration) error {
	return r.update(ctx, `UPDATE hatamoto_webhook_deliveries SET status = 'failed', attempts = attempts + 1, response_status = $2,
		response_body = $3, error = $4, duration_ms = $5, leased_until = NULL, last_attempt_at = now() WHERE id = $1`,
		id, status, body, errMsg, dur.Milliseconds())
}

// MarkFailed fails a delivery without counting an attempt (its subscription is gone or paused).
func (r *Repo) MarkFailed(ctx context.Context, id, errMsg string) error {
	return r.update(ctx, `UPDATE hatamoto_webhook_deliveries SET status = 'failed', leased_until = NULL, error = $2 WHERE id = $1`, id, errMsg)
}

// RetryFailed puts a failed delivery back in the queue for one more attempt;
// any other delivery is left alone.
func (r *Repo) RetryFailed(ctx context.Context, id string) error {
	return r.update(ctx, `UPDATE hatamoto_webhook_deliveries SET status = 'pending', leased_until = NULL, next_attempt_at = now(),
		max_attempts = attempts + 1 WHERE id = $1 AND status = 'failed'`, id)
}

func (r *Repo) update(ctx context.Context, sql string, args ...any) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, sql, args...)
	return err
}

// ListDeliveries returns the log, newest first, for one subscription or (empty
// id) all. limit defaults to 50 and is at most 200.
func (r *Repo) ListDeliveries(ctx context.Context, subscriptionID string, limit, offset int) ([]DeliveryRow, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	offset = max(offset, 0)
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT id, subscription_id, event, status, attempts, max_attempts, next_attempt_at, response_status,
			coalesce(response_body, ''), coalesce(error, ''), duration_ms, created_at, last_attempt_at, delivered_at
		FROM hatamoto_webhook_deliveries WHERE ($1 = '' OR subscription_id = $1)
		ORDER BY created_at DESC, id LIMIT $2 OFFSET $3`, subscriptionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryRow{}
	for rows.Next() {
		var d DeliveryRow
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.Event, &d.Status, &d.Attempts, &d.MaxAttempts, &d.NextAttemptAt, &d.ResponseStatus,
			&d.ResponseBody, &d.Error, &d.DurationMS, &d.CreatedAt, &d.LastAttemptAt, &d.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDelivery returns one delivery log row (never its payload), or ErrNotFound.
func (r *Repo) GetDelivery(ctx context.Context, id string) (*DeliveryRow, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var d DeliveryRow
	err = conn.QueryRow(ctx, `SELECT id, subscription_id, event, status, attempts, max_attempts, next_attempt_at, response_status,
			coalesce(response_body, ''), coalesce(error, ''), duration_ms, created_at, last_attempt_at, delivered_at
		FROM hatamoto_webhook_deliveries WHERE id = $1`, id).
		Scan(&d.ID, &d.SubscriptionID, &d.Event, &d.Status, &d.Attempts, &d.MaxAttempts, &d.NextAttemptAt, &d.ResponseStatus,
			&d.ResponseBody, &d.Error, &d.DurationMS, &d.CreatedAt, &d.LastAttemptAt, &d.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// CountDeliveries returns how many log rows ListDeliveries pages through, for
// one subscription or (empty id) all.
func (r *Repo) CountDeliveries(ctx context.Context, subscriptionID string) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var n int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE ($1 = '' OR subscription_id = $1)`, subscriptionID).Scan(&n)
	return n, err
}

// CountFailedSince returns how many deliveries failed for good and were queued
// at or after since, for the Overview's site health.
func (r *Repo) CountFailedSince(ctx context.Context, since time.Time) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var n int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM hatamoto_webhook_deliveries WHERE status = 'failed' AND created_at >= $1`, since).Scan(&n)
	return n, err
}
