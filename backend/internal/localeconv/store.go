package localeconv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockTimeout is how long apply and revert wait for editors' writes to finish.
const lockTimeout = "5s"

// retagGuard is what entity_locale_retag() contains once the multi-locale
// migration ran: without it a language change would re-tag every entity.
const retagGuard = "jsonb_array_length(NEW.locales)"

type pgService struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewService returns the Postgres-backed Service.
func NewService(pool *pgxpool.Pool, logger *slog.Logger) Service {
	return &pgService{pool: pool, logger: logger}
}

// querier is the part of pgx.Tx and *pgxpool.Pool the loaders use.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// readOnly runs fn in a repeatable-read read-only transaction.
func (s *pgService) readOnly(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return fn(tx)
}

// LoadSnapshot reads the data the analysis works on in one consistent read.
func (s *pgService) LoadSnapshot(ctx context.Context) (*Snapshot, error) {
	var snap *Snapshot
	err := s.readOnly(ctx, func(tx pgx.Tx) (err error) {
		snap, err = loadSnapshot(ctx, tx)
		return err
	})
	return snap, err
}

func (s *pgService) Suggest(ctx context.Context) (*Plan, []Issue, error) {
	return SuggestFrom(ctx, s)
}

func (s *pgService) DryRun(ctx context.Context, plan *Plan) (*Report, error) {
	snap, err := s.LoadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	_, rep := Analyze(snap, plan)
	return rep, nil
}

func loadSnapshot(ctx context.Context, q querier) (*Snapshot, error) {
	snap := &Snapshot{}

	rows, err := q.Query(ctx, `
		SELECT e.id::text, s.slug, e.slug, COALESCE(e.parent_id::text, ''), e.locale, COALESCE(e.status, 'draft'),
		       COALESCE(e.translation_group::text, ''), e.slug_nested, e.attributes,
		       COALESCE(e.created_at, now()), COALESCE(e.updated_at, now()), e.published_at
		  FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		 WHERE e.variant_key IS NULL
		 ORDER BY e.id`)
	if err != nil {
		return nil, fmt.Errorf("load entities: %w", err)
	}
	for rows.Next() {
		var e Ent
		var attrs []byte
		if err := rows.Scan(&e.ID, &e.Schema, &e.Slug, &e.ParentID, &e.Locale, &e.Status, &e.Group, &e.Nested,
			&attrs, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan entity: %w", err)
		}
		e.Attrs = attrs
		snap.Entities = append(snap.Entities, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load entities: %w", err)
	}

	rows, err = q.Query(ctx, `SELECT id::text, slug, name, settings FROM hatamoto_entity_schemas ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("load schemas: %w", err)
	}
	for rows.Next() {
		var sc Schema
		var settings []byte
		if err := rows.Scan(&sc.ID, &sc.Slug, &sc.Name, &settings); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan schema: %w", err)
		}
		sc.Settings = settings
		snap.Schemas = append(snap.Schemas, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load schemas: %w", err)
	}

	site, err := loadSite(ctx, q)
	if err != nil {
		return nil, err
	}
	snap.Site = site

	rows, err = q.Query(ctx, `SELECT source_path FROM hatamoto_redirects WHERE is_active ORDER BY source_path`)
	if err != nil {
		return nil, fmt.Errorf("load redirects: %w", err)
	}
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan redirect: %w", err)
		}
		snap.RedirectSources = append(snap.RedirectSources, src)
	}
	rows.Close()
	return snap, rows.Err()
}

func loadSite(ctx context.Context, q querier) (SiteState, error) {
	var site SiteState
	var locales []byte
	err := q.QueryRow(ctx, `SELECT language, locales, prefix_all, x_default_locale FROM hatamoto_site_config WHERE id = TRUE`).
		Scan(&site.Language, &locales, &site.PrefixAll, &site.XDefault)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return site, fmt.Errorf("load site settings: %w", err)
	}
	if len(locales) > 0 {
		if err := json.Unmarshal(locales, &site.Locales); err != nil {
			return site, fmt.Errorf("decode locales: %w", err)
		}
	}
	var def string
	if err := q.QueryRow(ctx, `SELECT pg_get_functiondef('public.entity_locale_retag'::regproc)`).Scan(&def); err != nil || !strings.Contains(def, retagGuard) {
		site.TriggerGuardMissing = true
	}
	return site, nil
}

// settingsJSON is the recorded form of the locale settings.
type settingsJSON struct {
	Language       string          `json:"language"`
	Locales        json.RawMessage `json:"locales"`
	PrefixAll      bool            `json:"prefix_all"`
	XDefaultLocale string          `json:"x_default_locale"`
}

func readSettings(ctx context.Context, q querier) (settingsJSON, error) {
	var st settingsJSON
	var locales []byte
	if err := q.QueryRow(ctx, `SELECT language, locales, prefix_all, x_default_locale FROM hatamoto_site_config WHERE id = TRUE`).
		Scan(&st.Language, &locales, &st.PrefixAll, &st.XDefaultLocale); err != nil {
		return st, fmt.Errorf("read site settings: %w", err)
	}
	st.Locales = locales
	return st, nil
}

// lockWrites takes the write lock apply and revert share: editors' writes wait
// (or time out after lockTimeout), public reads keep serving the old data.
func lockWrites(ctx context.Context, tx pgx.Tx) error {
	for _, sql := range []string{
		`SET LOCAL lock_timeout = '` + lockTimeout + `'`,
		`LOCK TABLE hatamoto_entities, hatamoto_entity_schemas IN SHARE ROW EXCLUSIVE MODE`,
		`SELECT 1 FROM hatamoto_site_config WHERE id = TRUE FOR UPDATE`,
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("lock for writing (editors may be saving, retry): %w", err)
		}
	}
	return nil
}
