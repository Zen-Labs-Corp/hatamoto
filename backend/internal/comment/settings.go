package comment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/jsonx"
)

const settingsTTL = 30 * time.Second

// Querier is the slice of pgx the settings store needs; *pgxpool.Pool satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// SettingsStore reads and writes the single hatamoto_comment_settings row,
// behind a short cache because every thread render reads it.
type SettingsStore struct {
	db  Querier
	now func() time.Time

	mu     sync.Mutex
	cached Settings
	at     time.Time
	ok     bool
	gen    uint64 // bumped by Invalidate so a load that raced a save is not cached
}

// NewSettingsStore returns a store over db.
func NewSettingsStore(db Querier) *SettingsStore {
	return &SettingsStore{db: db, now: time.Now}
}

// Get returns the cached settings. On a read error it returns Defaults (comments
// off: fail closed) and the error.
func (s *SettingsStore) Get(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	if s.ok && s.now().Sub(s.at) < settingsTTL {
		c := s.cached
		s.mu.Unlock()
		return c, nil
	}
	gen := s.gen
	s.mu.Unlock()

	set, err := s.load(ctx)
	if err != nil {
		return Defaults(), err
	}
	s.mu.Lock()
	if s.gen == gen {
		s.cached, s.at, s.ok = set, s.now(), true
	}
	s.mu.Unlock()
	return set, nil
}

// Invalidate drops the cached copy so the next Get reads the database.
func (s *SettingsStore) Invalidate() {
	s.mu.Lock()
	s.ok = false
	s.gen++
	s.mu.Unlock()
}

func (s *SettingsStore) load(ctx context.Context) (Settings, error) {
	var enabled bool
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT enabled, settings FROM hatamoto_comment_settings WHERE id`).Scan(&enabled, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("load comment settings: %w", err)
	}
	set := Defaults()
	if len(raw) > 0 {
		// A bad stored document reads as defaults for the keys it breaks.
		if err := jsonx.Unmarshal(raw, &set, jsonx.StoredLoose); err != nil {
			set = Defaults()
		}
	}
	set.Enabled = enabled
	return set.Normalize(), nil
}

// Save validates and stores set, then drops the cache. Who may enable comments
// is the caller's rule; this store does not check it.
func (s *SettingsStore) Save(ctx context.Context, set Settings, by *uuid.UUID) error {
	if err := set.Validate(); err != nil {
		return err
	}
	raw, err := jsonx.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode comment settings: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_comment_settings (id, enabled, settings, updated_at, updated_by)
		VALUES (true, $1, $2::jsonb, now(), $3)
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, settings = EXCLUDED.settings,
			updated_at = now(), updated_by = EXCLUDED.updated_by`,
		set.Enabled, string(raw), by)
	if err != nil {
		return fmt.Errorf("save comment settings: %w", err)
	}
	s.Invalidate()
	return nil
}

// ResolveMode gives the comment mode of one entity: site off -> off; the
// entity's own mode, else the schema's, else open; an open entity published
// longer ago than the auto-close days is closed. Unknown strings read as absent.
func ResolveMode(site Settings, schemaMode, entityMode string, publishedAt *time.Time, now time.Time) Mode {
	if !site.Enabled {
		return ModeOff
	}
	mode := ModeOpen
	switch {
	case validMode(entityMode):
		mode = Mode(entityMode)
	case validMode(schemaMode):
		mode = Mode(schemaMode)
	}
	if mode == ModeOpen && site.AutoCloseDays > 0 && publishedAt != nil &&
		now.Sub(*publishedAt) > time.Duration(site.AutoCloseDays)*24*time.Hour {
		return ModeClosed
	}
	return mode
}

func validMode(s string) bool {
	return s == string(ModeOpen) || s == string(ModeClosed) || s == string(ModeHidden)
}
