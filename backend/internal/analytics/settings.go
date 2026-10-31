package analytics

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// SettingsStore persists the analytics settings singleton
// (hatamoto_analytics_settings). It is read at boot and on change; the
// request path uses the Service's cached copy, never this store.
type SettingsStore struct{ db *database.DB }

// NewSettingsStore creates the Postgres-backed settings store.
func NewSettingsStore(db *database.DB) *SettingsStore { return &SettingsStore{db: db} }

// EngagementEnabled reports whether engagement events are counted. A missing
// row means the default, enabled.
func (s *SettingsStore) EngagementEnabled(ctx context.Context) (bool, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return false, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	var on bool
	err = conn.QueryRow(ctx, `SELECT engagement_enabled FROM hatamoto_analytics_settings WHERE id`).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read engagement setting: %w", err)
	}
	return on, nil
}

// SetEngagement turns engagement counting on or off.
func (s *SettingsStore) SetEngagement(ctx context.Context, on bool) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `INSERT INTO hatamoto_analytics_settings (id, engagement_enabled) VALUES (true, $1)
		ON CONFLICT (id) DO UPDATE SET engagement_enabled = EXCLUDED.engagement_enabled`, on)
	if err != nil {
		return fmt.Errorf("write engagement setting: %w", err)
	}
	return nil
}
