package analytics

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// PostgresTrafficRepository is the TrafficRepository backed by
// hatamoto_traffic_daily.
type PostgresTrafficRepository struct{ db *database.DB }

// NewPostgresTrafficRepository creates the Postgres-backed traffic repository.
func NewPostgresTrafficRepository(db *database.DB) *PostgresTrafficRepository {
	return &PostgresTrafficRepository{db: db}
}

const addTrafficSQL = `
	INSERT INTO hatamoto_traffic_daily AS t (day, dim, k1, k2, visits, pageviews, engaged, multi, scrolled, form_seen, form_started)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (day, dim, k1, k2) DO UPDATE SET
		visits = t.visits + EXCLUDED.visits,
		pageviews = t.pageviews + EXCLUDED.pageviews,
		engaged = t.engaged + EXCLUDED.engaged,
		multi = t.multi + EXCLUDED.multi,
		scrolled = t.scrolled + EXCLUDED.scrolled,
		form_seen = t.form_seen + EXCLUDED.form_seen,
		form_started = t.form_started + EXCLUDED.form_started`

// AddTraffic implements TrafficRepository: one batch, one transaction.
func (r *PostgresTrafficRepository) AddTraffic(ctx context.Context, rows []TrafficRow) error {
	if len(rows) == 0 {
		return nil
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("add traffic: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	batch := &pgx.Batch{}
	for _, row := range rows {
		batch.Queue(addTrafficSQL, row.Day, string(row.Dim), row.K1, row.K2,
			row.Visits, row.Pageviews, row.Engaged, row.Multi, row.Scrolled, row.FormSeen, row.FormStarted)
	}
	res := tx.SendBatch(ctx, batch)
	for range rows {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return fmt.Errorf("add traffic: %w", err)
		}
	}
	if err := res.Close(); err != nil {
		return fmt.Errorf("add traffic: %w", err)
	}
	return tx.Commit(ctx)
}
