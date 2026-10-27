package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

// ErrNotFound is returned when a subscription (or delivery) does not exist.
var ErrNotFound = errors.New("webhook not found")

// Repo is the Postgres access to hatamoto_webhooks and
// hatamoto_webhook_deliveries.
type Repo struct{ db *database.DB }

// NewRepo returns a Repo on db.
func NewRepo(db *database.DB) *Repo { return &Repo{db: db} }

// Stats are the delivery counts of one subscription.
type Stats struct {
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

// ListedSubscription is a subscription as shown in lists. It has no Secret
// field at all: the secret is shown once, on creation or rotation.
type ListedSubscription struct {
	ID                string
	Name              string
	URL               string
	Events            []string
	Headers           map[string]string
	Active            bool
	MaxRetries        int
	RetryDelaySeconds int
	TimeoutSeconds    int
	AllowHTTP         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Stats
}

const subscriptionColumns = `id, name, url, secret, events, headers, active, max_retries,
	retry_delay_seconds, timeout_seconds, allow_http, created_at, updated_at`

func nonNilEvents(e []string) []string {
	if e == nil {
		return []string{}
	}
	return e
}

func nonNilHeaders(h map[string]string) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	return h
}

// Insert stores a new subscription; s.ID and s.Secret must be set.
func (r *Repo) Insert(ctx context.Context, s *Subscription) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	return conn.QueryRow(ctx, `INSERT INTO hatamoto_webhooks (id, name, url, secret, events, headers, active,
			max_retries, retry_delay_seconds, timeout_seconds, allow_http)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING created_at, updated_at`,
		s.ID, s.Name, s.URL, s.Secret, nonNilEvents(s.Events), nonNilHeaders(s.Headers), s.Active,
		s.MaxRetries, s.RetryDelaySeconds, s.TimeoutSeconds, s.AllowHTTP).Scan(&s.CreatedAt, &s.UpdatedAt)
}

// Get returns one subscription including its secret (the deliverer signs with it).
func (r *Repo) Get(ctx context.Context, id string) (*Subscription, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var s Subscription
	err = conn.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM hatamoto_webhooks WHERE id = $1`, id).
		Scan(&s.ID, &s.Name, &s.URL, &s.Secret, &s.Events, &s.Headers, &s.Active, &s.MaxRetries,
			&s.RetryDelaySeconds, &s.TimeoutSeconds, &s.AllowHTTP, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// List returns every subscription, oldest first, with delivery counts. The
// secret is deliberately not selected.
func (r *Repo) List(ctx context.Context) ([]ListedSubscription, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT s.id, s.name, s.url, s.events, s.headers, s.active, s.max_retries,
			s.retry_delay_seconds, s.timeout_seconds, s.allow_http, s.created_at, s.updated_at,
			count(d.id) FILTER (WHERE d.status = 'delivered'),
			count(d.id) FILTER (WHERE d.status = 'failed'),
			count(d.id) FILTER (WHERE d.status = 'pending')
		FROM hatamoto_webhooks s LEFT JOIN hatamoto_webhook_deliveries d ON d.subscription_id = s.id
		GROUP BY s.id ORDER BY s.created_at, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ListedSubscription{}
	for rows.Next() {
		var s ListedSubscription
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Events, &s.Headers, &s.Active, &s.MaxRetries,
			&s.RetryDelaySeconds, &s.TimeoutSeconds, &s.AllowHTTP, &s.CreatedAt, &s.UpdatedAt,
			&s.Delivered, &s.Failed, &s.Pending); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Update writes the editable fields. It never writes the secret.
func (r *Repo) Update(ctx context.Context, s *Subscription) error {
	return r.exec(ctx, `UPDATE hatamoto_webhooks SET name = $2, url = $3, events = $4, headers = $5, active = $6,
			max_retries = $7, retry_delay_seconds = $8, timeout_seconds = $9, allow_http = $10, updated_at = now()
		WHERE id = $1`, s.ID, s.Name, s.URL, nonNilEvents(s.Events), nonNilHeaders(s.Headers), s.Active,
		s.MaxRetries, s.RetryDelaySeconds, s.TimeoutSeconds, s.AllowHTTP)
}

// SetSecret replaces the signing secret.
func (r *Repo) SetSecret(ctx context.Context, id, secret string) error {
	return r.exec(ctx, `UPDATE hatamoto_webhooks SET secret = $2, updated_at = now() WHERE id = $1`, id, secret)
}

// SetActive pauses or resumes a subscription.
func (r *Repo) SetActive(ctx context.Context, id string, active bool) error {
	return r.exec(ctx, `UPDATE hatamoto_webhooks SET active = $2, updated_at = now() WHERE id = $1`, id, active)
}

// Delete removes a subscription; its deliveries go with it (ON DELETE CASCADE).
func (r *Repo) Delete(ctx context.Context, id string) error {
	return r.exec(ctx, `DELETE FROM hatamoto_webhooks WHERE id = $1`, id)
}

// Count returns the number of subscriptions.
func (r *Repo) Count(ctx context.Context) (int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var n int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM hatamoto_webhooks`).Scan(&n)
	return n, err
}

// exec runs a statement that targets one subscription and maps "no row" to ErrNotFound.
func (r *Repo) exec(ctx context.Context, sql string, args ...any) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
