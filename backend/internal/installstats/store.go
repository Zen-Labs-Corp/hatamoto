// Package installstats is the anonymous install stats sender. A release
// install the owner never answered for defaults to Detailed once setup is
// complete (HATAMOTO_TELEMETRY=off wins); an explicit Off or Basic is kept.
// See Service.Run for the rules.
package installstats

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/jsonx"
)

// Consent is the owner's stored answer.
type Consent string

const (
	ConsentUnset Consent = "unset" // never asked, or not answered yet
	ConsentYes   Consent = "yes"
	ConsentNo    Consent = "no"
)

// sendInterval is the least time between two sends. A little under a day so a
// daily job with jitter is never skipped.
const sendInterval = "20 hours"

// versionRetry is the least time between two sends when only the running
// version changed, so a receiver that keeps failing sees one attempt per
// hourly check at most.
const versionRetry = "30 minutes"

// Level is how much the owner shares.
type Level string

const (
	LevelUnset    Level = "unset" // never asked, or not answered yet
	LevelOff      Level = "off"
	LevelBasic    Level = "basic"
	LevelDetailed Level = "detailed"
)

// BannerKind says which owner banner, if any, to show.
type BannerKind string

const (
	BannerNone       BannerKind = "none"
	BannerUnanswered BannerKind = "unanswered" // ask Off, Basic or Detailed
	BannerNudge      BannerKind = "nudge"      // on Basic, not dismissed: offer Detailed
	BannerDefaulted  BannerKind = "defaulted"  // Detailed was applied by default: tell the owner once
)

// maxPluginErrorKeys bounds the stored per-plugin error map; names beyond it
// are summed under otherKey.
const (
	maxPluginErrorKeys = 200
	otherKey           = "other"
)

// Counts are the Detailed level's health counts.
type Counts struct {
	Panics, HTTP5xx, RenderErrors, MigrationFailed int64
	PluginErrors                                   map[string]int64
}

// IsZero reports whether nothing is counted.
func (c Counts) IsZero() bool {
	return c.Panics == 0 && c.HTTP5xx == 0 && c.RenderErrors == 0 && c.MigrationFailed == 0 && len(c.PluginErrors) == 0
}

// add returns c plus d, folding plugin names beyond the cap into "other".
func (c Counts) add(d Counts) Counts {
	out := Counts{
		Panics: c.Panics + d.Panics, HTTP5xx: c.HTTP5xx + d.HTTP5xx,
		RenderErrors: c.RenderErrors + d.RenderErrors, MigrationFailed: c.MigrationFailed + d.MigrationFailed,
		PluginErrors: make(map[string]int64, len(c.PluginErrors)),
	}
	for k, v := range c.PluginErrors {
		out.PluginErrors[k] = v
	}
	for k, v := range d.PluginErrors {
		if _, known := out.PluginErrors[k]; !known && len(out.PluginErrors) >= maxPluginErrorKeys {
			k = otherKey
		}
		out.PluginErrors[k] += v
	}
	return out
}

// sub returns c minus d, floored at zero; zero plugin entries are dropped.
func (c Counts) sub(d Counts) Counts {
	floor := func(a, b int64) int64 { return max(a-b, 0) }
	out := Counts{
		Panics: floor(c.Panics, d.Panics), HTTP5xx: floor(c.HTTP5xx, d.HTTP5xx),
		RenderErrors: floor(c.RenderErrors, d.RenderErrors), MigrationFailed: floor(c.MigrationFailed, d.MigrationFailed),
		PluginErrors: map[string]int64{},
	}
	for k, v := range c.PluginErrors {
		if n := floor(v, d.PluginErrors[k]); n > 0 {
			out.PluginErrors[k] = n
		}
	}
	return out
}

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// State is the stored row. InstallID is empty unless Consent is yes.
type State struct {
	Consent         Consent
	Level           Level
	InstallID       string
	LastSentAt      *time.Time
	LastOKAt        *time.Time // last successful report; unchanged by failed attempts
	LastStatus      string
	LastSentVersion string // version of the last successful report
	NudgeDismissed  bool   // the owner dismissed the Detailed offer
	// DefaultNoticePending is set when the default wrote the row, until the
	// owner acknowledges it or makes an explicit choice.
	DefaultNoticePending bool
	WindowStart          *time.Time // start of the health counts; nil outside Detailed
	Counts               Counts
	Sent                 Counts // the counts the last report carried; Counts includes them until the next UTC day
	// DetailedSentAt is when the last ok report that carried a Detailed body
	// was claimed; nil when none did since the level or consent changed.
	DetailedSentAt *time.Time
	// DetailedSentToday says that time is on the current UTC day, by the
	// database clock.
	DetailedSentToday bool
}

// Store reads and writes the single hatamoto_install_stats row. It does no
// caching: every call goes to the database, so a choice takes effect at once
// on every replica.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

const stateColumns = `consent, level, install_id, last_sent_at, last_ok_at, last_status, last_sent_version,
	nudge_dismissed, default_notice_pending, health_window_start, h_panics, h_http_5xx, h_render_errors, h_migration_failed, h_plugin_errors, h_sent,
	h_sent_at, COALESCE((h_sent_at AT TIME ZONE 'UTC')::date = (now() AT TIME ZONE 'UTC')::date, FALSE)`

func scanState(row pgx.Row) (State, error) {
	var (
		st     State
		id     *string
		errsJS []byte
		sentJS []byte
	)
	if err := row.Scan(&st.Consent, &st.Level, &id, &st.LastSentAt, &st.LastOKAt, &st.LastStatus, &st.LastSentVersion,
		&st.NudgeDismissed, &st.DefaultNoticePending, &st.WindowStart, &st.Counts.Panics, &st.Counts.HTTP5xx, &st.Counts.RenderErrors,
		&st.Counts.MigrationFailed, &errsJS, &sentJS, &st.DetailedSentAt, &st.DetailedSentToday); err != nil {
		return State{}, err
	}
	if id != nil {
		st.InstallID = *id
	}
	if len(errsJS) > 0 {
		if err := jsonx.Unmarshal(errsJS, &st.Counts.PluginErrors); err != nil {
			return State{}, fmt.Errorf("decode plugin errors: %w", err)
		}
	}
	if len(sentJS) > 0 {
		if err := jsonx.Unmarshal(sentJS, &st.Sent); err != nil {
			return State{}, fmt.Errorf("decode sent counts: %w", err)
		}
	}
	return st, nil
}

// reconcile repairs a row an older binary changed. That binary only knows
// consent: its Enable leaves level off or unset, and its Disable leaves basic
// or detailed (with the health counts). Consent wins: yes becomes basic, no
// becomes off with the counts wiped. A row where the two agree is untouched.
func (s *Store) reconcile(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		UPDATE hatamoto_install_stats SET
			level = CASE consent WHEN 'yes' THEN 'basic' ELSE 'off' END,
			health_window_start = NULL, h_panics = 0, h_http_5xx = 0, h_render_errors = 0,
			h_migration_failed = 0, h_plugin_errors = '{}'::jsonb, h_sent = '{}'::jsonb, h_sent_at = NULL
		WHERE (consent = 'yes' AND level NOT IN ('basic', 'detailed'))
		   OR (consent = 'no' AND level <> 'off')`)
	if err != nil {
		return fmt.Errorf("reconcile install stats level: %w", err)
	}
	return nil
}

// Load returns the stored state; a missing row is consent unset. It first
// repairs a level that disagrees with consent (see reconcile).
func (s *Store) Load(ctx context.Context) (State, error) {
	if err := s.reconcile(ctx); err != nil {
		return State{}, err
	}
	st, err := scanState(s.db.QueryRow(ctx, `SELECT `+stateColumns+` FROM hatamoto_install_stats WHERE id`))
	if errors.Is(err, pgx.ErrNoRows) {
		return State{Consent: ConsentUnset, Level: LevelUnset}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("load install stats: %w", err)
	}
	return st, nil
}

// Enable records consent yes at level (basic or detailed). It is the owner's
// explicit choice, so the Detailed offer counts as made: only rows migrated
// from consent yes keep it. An existing ID is kept; otherwise a new random one
// is created. Entering Detailed starts a
// fresh health window with zero counts; leaving it zeroes them.
func (s *Store) Enable(ctx context.Context, level Level) (State, error) {
	if level != LevelBasic && level != LevelDetailed {
		return State{}, fmt.Errorf("enable install stats: level %q shares nothing", level)
	}
	id, err := newID()
	if err != nil {
		return State{}, err
	}
	// The counts restart whenever the Detailed state changes; staying put
	// keeps them.
	const t = `hatamoto_install_stats`
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_install_stats (id, consent, level, install_id, nudge_dismissed, health_window_start)
		VALUES (TRUE, 'yes', $2, $1, TRUE, CASE WHEN $2::text = 'detailed' THEN now() END)
		ON CONFLICT (id) DO UPDATE SET consent = 'yes', level = EXCLUDED.level, nudge_dismissed = TRUE,
			default_notice_pending = FALSE,
			install_id = COALESCE(`+t+`.install_id, EXCLUDED.install_id),
			health_window_start = CASE
				WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.health_window_start
				WHEN EXCLUDED.level = 'detailed' THEN now() END,
			h_panics = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_panics ELSE 0 END,
			h_http_5xx = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_http_5xx ELSE 0 END,
			h_render_errors = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_render_errors ELSE 0 END,
			h_migration_failed = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_migration_failed ELSE 0 END,
			h_plugin_errors = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_plugin_errors ELSE '{}'::jsonb END,
			h_sent = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_sent ELSE '{}'::jsonb END,
			h_sent_at = CASE WHEN `+t+`.level = EXCLUDED.level THEN `+t+`.h_sent_at END`,
		id, string(level))
	if err != nil {
		return State{}, fmt.Errorf("enable install stats: %w", err)
	}
	return s.Load(ctx)
}

// ApplyDefault records the default (consent yes, level detailed, a new ID, the
// Detailed offer counted as made, the notice pending) when the install was
// never asked. One statement, so replicas and repeated runs write it once;
// applied is false when a row already holds an answer.
func (s *Store) ApplyDefault(ctx context.Context) (applied bool, err error) {
	id, err := newID()
	if err != nil {
		return false, err
	}
	const t = `hatamoto_install_stats`
	err = s.db.QueryRow(ctx, `
		INSERT INTO hatamoto_install_stats (id, consent, level, install_id, nudge_dismissed, health_window_start, default_notice_pending)
		VALUES (TRUE, 'yes', 'detailed', $1, TRUE, now(), TRUE)
		ON CONFLICT (id) DO UPDATE SET consent = EXCLUDED.consent, level = EXCLUDED.level, install_id = EXCLUDED.install_id,
			nudge_dismissed = TRUE, health_window_start = now(), default_notice_pending = TRUE
		WHERE `+t+`.level = 'unset' AND `+t+`.consent = 'unset'
		RETURNING TRUE`, id).Scan(&applied)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("apply default install stats: %w", err)
	}
	return applied, nil
}

// Disable records consent no (level off), deletes the ID and wipes the health
// counts, in one statement.
func (s *Store) Disable(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_install_stats (id, consent, level, install_id) VALUES (TRUE, 'no', 'off', NULL)
		ON CONFLICT (id) DO UPDATE SET consent = 'no', level = 'off', install_id = NULL, default_notice_pending = FALSE,
			health_window_start = NULL, h_panics = 0, h_http_5xx = 0, h_render_errors = 0,
			h_migration_failed = 0, h_plugin_errors = '{}'::jsonb, h_sent = '{}'::jsonb, h_sent_at = NULL`)
	if err != nil {
		return fmt.Errorf("disable install stats: %w", err)
	}
	return nil
}

// Reset replaces the ID with a new random one. It does nothing unless consent
// is yes: an ID never exists without it.
func (s *Store) Reset(ctx context.Context) (State, error) {
	id, err := newID()
	if err != nil {
		return State{}, err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE hatamoto_install_stats SET install_id = $1 WHERE id AND consent = 'yes'`, id); err != nil {
		return State{}, fmt.Errorf("reset install id: %w", err)
	}
	return s.Load(ctx)
}

// Claim takes the right to send now: it stamps last_sent_at and returns the ID,
// but only when consent is yes, the level is Basic or Detailed and either the last claim is older than the
// interval, or the running version differs from the last one reported and the
// last claim is older than versionRetry. The check and the stamp are one
// statement, so replicas and restarts cannot both send. ok is false when
// nothing may be sent.
func (s *Store) Claim(ctx context.Context, version string) (id string, ok bool, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE hatamoto_install_stats SET last_sent_at = now()
		WHERE id AND consent = 'yes' AND level IN ('basic', 'detailed') AND install_id IS NOT NULL
		  AND (last_sent_at IS NULL
		       OR last_sent_at < now() - interval '`+sendInterval+`'
		       OR (last_sent_version IS DISTINCT FROM $1 AND last_sent_at < now() - interval '`+versionRetry+`'))
		RETURNING install_id`, version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claim install stats send: %w", err)
	}
	if err := s.startDay(ctx); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// startDay is the health window's rollover. Counts are kept through a UTC day
// and every report carries all of them, because the receiver replaces a day's
// rows. When the last report that carried a Detailed body is from an earlier
// UTC day (by the database clock), what it carried is taken off and the window
// moves forward to that report, so the next body holds only what came after
// it. The window never moves backwards, and is left alone when no Detailed
// body was sent. Counts bumped meanwhile survive.
func (s *Store) startDay(ctx context.Context) error {
	err := s.withLockedRow(ctx, func(tx pgx.Tx, st State) error {
		var rolled bool
		err := tx.QueryRow(ctx, `
			UPDATE hatamoto_install_stats SET health_window_start = GREATEST(health_window_start, h_sent_at),
				h_sent = '{}'::jsonb, h_sent_at = NULL
			WHERE id AND h_sent_at IS NOT NULL
			  AND (h_sent_at AT TIME ZONE 'UTC')::date < (now() AT TIME ZONE 'UTC')::date
			RETURNING TRUE`).Scan(&rolled)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return writeCounts(ctx, tx, st.Counts.sub(st.Sent))
	})
	if err != nil {
		return fmt.Errorf("start install stats day: %w", err)
	}
	return nil
}

// AddHealth adds d to the stored health counts. It does nothing unless the
// level is detailed. The row is locked, so replicas add up.
func (s *Store) AddHealth(ctx context.Context, d Counts) error {
	if d.IsZero() {
		return nil
	}
	return s.withLockedRow(ctx, func(tx pgx.Tx, st State) error {
		if st.Level != LevelDetailed {
			return nil
		}
		return writeCounts(ctx, tx, st.Counts.add(d))
	})
}

// RecordResult stores the outcome of the last send. On "ok" it also stores the
// version reported and the claim time of the success. sent is the health counts
// the body carried, nil for a Basic body: a Detailed one is remembered with the
// time (h_sent_at), and the next UTC day's rollover (startDay) takes it off.
// Counts bumped while the send ran are never touched. A failed send changes
// only the status. The text must not contain the install ID.
func (s *Store) RecordResult(ctx context.Context, status, version string, sent *Counts) error {
	err := s.withLockedRow(ctx, func(tx pgx.Tx, _ State) error {
		if status != "ok" {
			_, err := tx.Exec(ctx, `UPDATE hatamoto_install_stats SET last_status = $1 WHERE id`, status)
			return err
		}
		js := []byte("{}")
		if sent != nil {
			var err error
			if js, err = jsonx.Marshal(*sent); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE hatamoto_install_stats SET last_status = $1, last_ok_at = COALESCE(last_sent_at, now()),
				last_sent_version = $2,
				h_sent = CASE WHEN $4 THEN $3::jsonb ELSE h_sent END,
				h_sent_at = CASE WHEN $4 THEN COALESCE(last_sent_at, now()) ELSE h_sent_at END WHERE id`,
			status, version, string(js), sent != nil)
		return err
	})
	if err != nil {
		return fmt.Errorf("record install stats result: %w", err)
	}
	return nil
}

// RecordKeptDay records "error: build" for a send that was not made because a
// Detailed body already went out the same UTC day and a Basic one would
// replace it at the receiver. The version still counts as reported, so the
// hourly check does not retry.
func (s *Store) RecordKeptDay(ctx context.Context, version string) error {
	if _, err := s.db.Exec(ctx, `UPDATE hatamoto_install_stats SET last_status = 'error: build', last_sent_version = $1 WHERE id`, version); err != nil {
		return fmt.Errorf("record install stats kept day: %w", err)
	}
	return nil
}

// DismissNudge acknowledges the owner banner: it stores that the Detailed
// offer was dismissed (only on basic) and clears the defaulted notice.
func (s *Store) DismissNudge(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `UPDATE hatamoto_install_stats SET nudge_dismissed = (nudge_dismissed OR level = 'basic'),
		default_notice_pending = FALSE WHERE id`); err != nil {
		return fmt.Errorf("dismiss install stats nudge: %w", err)
	}
	return nil
}

// Banner says which owner banner to show, from one cheap query. The variable
// HATAMOTO_TELEMETRY=off is the Service's concern, not the Store's.
func (s *Store) Banner(ctx context.Context) (BannerKind, error) {
	var (
		level     Level
		dismissed bool
		pending   bool
	)
	err := s.db.QueryRow(ctx, `SELECT level, nudge_dismissed, default_notice_pending FROM hatamoto_install_stats WHERE id`).Scan(&level, &dismissed, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return BannerUnanswered, nil
	}
	if err != nil {
		return BannerNone, fmt.Errorf("load install stats banner: %w", err)
	}
	switch {
	case level == LevelUnset:
		return BannerUnanswered, nil
	case pending && level == LevelDetailed:
		return BannerDefaulted, nil
	case level == LevelBasic && !dismissed:
		return BannerNudge, nil
	}
	return BannerNone, nil
}

// withLockedRow runs fn in a transaction holding the row's lock. A missing row
// is not an error: fn is not called.
func (s *Store) withLockedRow(ctx context.Context, fn func(tx pgx.Tx, st State) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("install stats tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	st, err := scanState(tx.QueryRow(ctx, `SELECT `+stateColumns+` FROM hatamoto_install_stats WHERE id FOR UPDATE`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock install stats: %w", err)
	}
	if err := fn(tx, st); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeCounts(ctx context.Context, tx pgx.Tx, c Counts) error {
	js, err := jsonx.Marshal(c.PluginErrors)
	if err != nil {
		return err
	}
	if c.PluginErrors == nil {
		js = []byte("{}")
	}
	_, err = tx.Exec(ctx, `
		UPDATE hatamoto_install_stats SET h_panics = $1, h_http_5xx = $2, h_render_errors = $3,
			h_migration_failed = $4, h_plugin_errors = $5::jsonb WHERE id`,
		c.Panics, c.HTTP5xx, c.RenderErrors, c.MigrationFailed, string(js))
	return err
}

// newID returns 16 random bytes as hex.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("install id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
