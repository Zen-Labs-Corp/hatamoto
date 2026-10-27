package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_update_settings row.
type Store struct{ db Querier }

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db} }

// Load returns the stored policy and status; a missing row is all defaults.
func (s *Store) Load(ctx context.Context) (Settings, Status, error) {
	var (
		set           Settings
		st            Status
		mode, channel *string
		pin           *string
		wStart, wEnd  *int16
		deferDays     int16
		firstSeen     []byte
	)
	err := s.db.QueryRow(ctx, `
		SELECT mode, channel, window_start, window_end, pin, defer_days,
		       last_check_at, last_result, decision, reason, target, available_major, first_seen
		FROM hatamoto_update_settings WHERE id`).Scan(
		&mode, &channel, &wStart, &wEnd, &pin, &deferDays,
		&st.LastCheckAt, &st.LastResult, &st.Decision, &st.Reason, &st.Target, &st.AvailableMajor, &firstSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, Status{}, nil
	}
	if err != nil {
		return Settings{}, Status{}, fmt.Errorf("load update settings: %w", err)
	}
	set.Mode, set.Channel, set.Pin = deref(mode), deref(channel), deref(pin)
	set.WindowStart, set.WindowEnd = hour(wStart), hour(wEnd)
	set.DeferDays = int(deferDays)
	if err := json.Unmarshal(firstSeen, &st.FirstSeen); err != nil {
		return Settings{}, Status{}, fmt.Errorf("decode first_seen: %w", err)
	}
	return set, st, nil
}

// SaveSettings validates and stores the policy columns, leaving the status alone.
func (s *Store) SaveSettings(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_update_settings (id, mode, channel, window_start, window_end, pin, defer_days)
		VALUES (TRUE, NULLIF($1,''), NULLIF($2,''), $3, $4, NULLIF($5,''), $6)
		ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode, channel = EXCLUDED.channel,
			window_start = EXCLUDED.window_start, window_end = EXCLUDED.window_end,
			pin = EXCLUDED.pin, defer_days = EXCLUDED.defer_days`,
		set.Mode, set.Channel, set.WindowStart, set.WindowEnd, set.Pin, set.DeferDays)
	if err != nil {
		return fmt.Errorf("save update settings: %w", err)
	}
	return nil
}

// Policy columns SavePolicy can write.
const (
	ColMode    = "mode"
	ColChannel = "channel"
	ColWindow  = "window"
	ColDefer   = "defer_days"
	ColPin     = "pin"
)

// SavePolicy validates set and writes only the named policy columns, so what
// was not named keeps its stored value: a column left NULL stays the built-in
// default, and a pin set meanwhile (the command line) is not overwritten.
func (s *Store) SavePolicy(ctx context.Context, set Settings, cols ...string) error {
	if err := set.Validate(); err != nil {
		return err
	}
	names, holders, sets := []string{"id"}, []string{"TRUE"}, []string{}
	var args []any
	add := func(col, expr string, vals ...any) {
		names = append(names, col)
		holders = append(holders, expr)
		sets = append(sets, col+" = EXCLUDED."+col)
		args = append(args, vals...)
	}
	n := func() string { return fmt.Sprintf("$%d", len(args)+1) }
	for _, c := range cols {
		switch c {
		case ColMode:
			add("mode", "NULLIF("+n()+",'')", set.Mode)
		case ColChannel:
			add("channel", "NULLIF("+n()+",'')", set.Channel)
		case ColDefer:
			add("defer_days", n(), set.DeferDays)
		case ColPin:
			add("pin", "NULLIF("+n()+",'')", set.Pin)
		case ColWindow:
			a := n()
			args = append(args, set.WindowStart)
			b := fmt.Sprintf("$%d", len(args)+1)
			names, holders = append(names, "window_start", "window_end"), append(holders, a, b)
			sets = append(sets, "window_start = EXCLUDED.window_start", "window_end = EXCLUDED.window_end")
			args = append(args, set.WindowEnd)
		default:
			return fmt.Errorf("save update policy: unknown column %q", c)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	q := "INSERT INTO hatamoto_update_settings (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(holders, ", ") +
		") ON CONFLICT (id) DO UPDATE SET " + strings.Join(sets, ", ")
	if _, err := s.db.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("save update policy: %w", err)
	}
	return nil
}

// SaveStatus stores the status columns, leaving the policy alone.
func (s *Store) SaveStatus(ctx context.Context, st Status) error {
	seen := map[string]time.Time{}
	for k, v := range st.FirstSeen {
		seen[k] = v
	}
	firstSeen, err := json.Marshal(seen)
	if err != nil {
		return fmt.Errorf("encode first_seen: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO hatamoto_update_settings (id, last_check_at, last_result, decision, reason, target, available_major, first_seen)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET last_check_at = EXCLUDED.last_check_at,
			last_result = EXCLUDED.last_result, decision = EXCLUDED.decision, reason = EXCLUDED.reason,
			target = EXCLUDED.target, available_major = EXCLUDED.available_major, first_seen = EXCLUDED.first_seen`,
		st.LastCheckAt, st.LastResult, st.Decision, st.Reason, st.Target, st.AvailableMajor, firstSeen)
	if err != nil {
		return fmt.Errorf("save update status: %w", err)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func hour(v *int16) *int {
	if v == nil {
		return nil
	}
	h := int(*v)
	return &h
}

// ServerMajor returns the major version of the database server.
func (s *Store) ServerMajor(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&n); err != nil {
		return 0, err
	}
	return n / 10000, nil
}
