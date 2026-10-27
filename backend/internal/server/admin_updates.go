package server

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/updater"
)

// Timings of the Updates adapter.
const (
	updatesApplyLimit   = 45 * time.Minute // one run downloads, verifies and switches
	updatesBannerTTL    = 30 * time.Second // the owner banner and the top-bar chip read the row at most this often
	updatesHistoryLimit = 20
)

// updaterRunner is the part of *updater.Updater the adapter runs.
type updaterRunner interface {
	Run(ctx context.Context, o updater.RunOpts) (updater.Status, error)
}

// updatesStore is the part of *updater.Store the adapter uses.
type updatesStore interface {
	Load(ctx context.Context) (updater.Settings, updater.Status, error)
	SavePolicy(ctx context.Context, s updater.Settings, cols ...string) error
}

// updatesLog is what the adapter reads and writes outside the updater: the
// supervisor's event history and the off_since stamp. The updater never
// touches either.
type updatesLog interface {
	History(ctx context.Context, limit int) ([]admin.UpdatesHistoryRow, error)
	OffSince(ctx context.Context) (*time.Time, error)
	SetOffSince(ctx context.Context, at *time.Time) error
}

// adminUpdates implements admin.UpdatesService over the update agent. Check
// and Apply go through Updater.Run, the path the scheduled job and the CLI
// use; this file adds no update logic of its own.
type adminUpdates struct {
	run     updaterRunner
	store   updatesStore
	log     updatesLog
	env     func(string) string
	now     func() time.Time
	running func() string
	logger  *slog.Logger
	// base is the context Update now runs under: the server's lifecycle
	// context, so a shutdown stops the download. nil reads as Background.
	base func() context.Context

	applying atomic.Bool
	wg       sync.WaitGroup // background Update now runs

	mu        sync.Mutex
	banner    *admin.UpdateBanner
	bannerAt  time.Time
	bannerSet bool
	glance    admin.UpdatesGlance
	glanceAt  time.Time
	glanceSet bool
}

func newAdminUpdates(u *updater.Updater, log updatesLog, env func(string) string, logger *slog.Logger, base func() context.Context) *adminUpdates {
	if logger == nil {
		logger = slog.Default()
	}
	return &adminUpdates{
		run: u, store: u.Store(), log: log, env: env, now: time.Now, logger: logger,
		running: u.RunningVersion, base: base,
	}
}

// state reads everything the page and the apply rule need.
func (s *adminUpdates) state(ctx context.Context) (admin.UpdatesState, error) {
	set, st, err := s.store.Load(ctx)
	if err != nil {
		return admin.UpdatesState{}, err
	}
	eff := set.Effective(s.env)
	running := s.running()
	// A stored target or major that is not newer than what runs now is stale
	// (a rollback that notify mode only reported, or a run that changed the
	// version without writing the status): never offered, so Update now cannot
	// install an older release. A target equal to running is left for the
	// page to read as "done".
	if st.Target != running && !packages.Newer(st.Target, running) {
		st.Target = ""
	}
	if st.AvailableMajor != running && !packages.Newer(st.AvailableMajor, running) {
		st.AvailableMajor = ""
	}
	out := admin.UpdatesState{
		Running: running, Dev: !release.ValidVersion(running),
		Mode: eff.Mode, Channel: eff.Channel, Channels: release.Channels,
		WindowStart: eff.WindowStart, WindowEnd: eff.WindowEnd, DeferDays: eff.DeferDays, Pin: eff.Pin,
		Locks:    s.locks(),
		Status:   adminStatus(st),
		Applying: s.applying.Load(),
	}
	return out, nil
}

// locks names the fields the environment sets. An unreadable channel value is
// ignored by Effective, so it does not lock; a mode value always does (an
// unreadable one fails closed to off).
func (s *adminUpdates) locks() admin.UpdatesLocks {
	var l admin.UpdatesLocks
	if strings.TrimSpace(s.env(updater.EnvMode)) != "" {
		l.Mode, l.ModeVar = true, updater.EnvMode
	}
	if v := s.env(updater.EnvChannel); v != "" && slices.Contains(release.Channels, v) {
		l.Channel, l.ChannelVar = true, updater.EnvChannel
	}
	return l
}

func adminStatus(st updater.Status) admin.UpdatesStatus {
	return admin.UpdatesStatus{
		LastCheckAt: st.LastCheckAt, LastResult: st.LastResult, Decision: st.Decision,
		Reason: st.Reason, Target: st.Target, AvailableMajor: st.AvailableMajor,
	}
}

// observeOff keeps off_since in step with the effective mode: stamped the
// first time updates are seen off (whatever turned them off), cleared when
// they are on again. It returns the stamp as it stands.
func (s *adminUpdates) observeOff(ctx context.Context, mode string) (*time.Time, error) {
	cur, err := s.log.OffSince(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case mode == updater.ModeOff && cur == nil:
		at := s.now()
		if err := s.log.SetOffSince(ctx, &at); err != nil {
			return nil, err
		}
		return &at, nil
	case mode != updater.ModeOff && cur != nil:
		return nil, s.log.SetOffSince(ctx, nil)
	}
	return cur, nil
}

func (s *adminUpdates) View(ctx context.Context) (admin.UpdatesState, error) {
	out, err := s.state(ctx)
	if err != nil {
		return out, err
	}
	if out.OffSince, err = s.observeOff(ctx, out.Mode); err != nil {
		s.logger.Warn("update off-since", "err", err) // the banner is optional
	}
	if out.History, err = s.log.History(ctx, updatesHistoryLimit); err != nil {
		s.logger.Warn("update history", "err", err)
		out.History = nil
	}
	return out, nil
}

// Save applies the form to the stored policy. It writes only what changed:
// the form always posts the effective mode and channel, and storing those
// would turn "use the default" into a fixed value, and a pin set from the
// command line meanwhile is never rewritten. Locked fields keep their stored
// value; the pin is never set here.
func (s *adminUpdates) Save(ctx context.Context, in admin.UpdatesForm) (map[string]string, error) {
	set, _, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	lock := s.locks()
	cur := set.Effective(func(string) string { return "" }) // stored over defaults, no environment
	var cols []string
	if !lock.Mode && in.Mode != cur.Mode {
		set.Mode = in.Mode
		cols = append(cols, updater.ColMode)
	}
	if !lock.Channel && in.Channel != cur.Channel {
		set.Channel = in.Channel
		cols = append(cols, updater.ColChannel)
	}
	if !sameHour(in.WindowStart, cur.WindowStart) || !sameHour(in.WindowEnd, cur.WindowEnd) {
		set.WindowStart, set.WindowEnd = in.WindowStart, in.WindowEnd
		cols = append(cols, updater.ColWindow)
	}
	if in.DeferDays != cur.DeferDays {
		set.DeferDays = in.DeferDays
		cols = append(cols, updater.ColDefer)
	}
	if err := set.Validate(); err != nil {
		if id := fieldOf(err); id != "" {
			return map[string]string{id: err.Error()}, nil
		}
		return nil, err
	}
	if err := s.store.SavePolicy(ctx, set, cols...); err != nil {
		return nil, err
	}
	s.resetBanner()
	return nil, nil
}

func sameHour(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// fieldOf maps a Settings.Validate message to the form element it is about.
func fieldOf(err error) string {
	switch msg := err.Error(); {
	case strings.HasPrefix(msg, "update mode"):
		return "mode"
	case strings.HasPrefix(msg, "update channel"):
		return "channel"
	case strings.HasPrefix(msg, "window hour"):
		return "window-start"
	}
	return ""
}

// Check fetches, decides and preflights without downloading, and stores the
// outcome like a scheduled check, so the page shows what the owner just saw.
func (s *adminUpdates) Check(ctx context.Context) (admin.UpdatesResult, error) {
	st, err := s.run.Run(ctx, updater.RunOpts{CheckOnly: true})
	s.resetBanner() // a failed check saves its status too
	if err != nil {
		return admin.UpdatesResult{}, err
	}
	return admin.UpdatesResult{Status: adminStatus(st), Busy: st.Decision == updater.KindBusy}, nil
}

// Apply starts the run to the target derived from the stored status. The
// target never comes from the caller. The run outlives the request but not
// the server: it runs under the lifecycle context and Wait joins it. It takes
// the scheduled path's pre-update backup and stores its outcome, whatever it
// is, for the page to show.
func (s *adminUpdates) Apply(ctx context.Context) (admin.UpdatesResult, error) {
	return s.apply(ctx, false)
}

// ApplyWithoutBackup is Apply for an update the pre-update backup stopped. It
// refuses unless the stored status says so, and skips the backup for this one
// run only; the next Apply backs up again.
func (s *adminUpdates) ApplyWithoutBackup(ctx context.Context) (admin.UpdatesResult, error) {
	return s.apply(ctx, true)
}

func (s *adminUpdates) apply(ctx context.Context, skipBackup bool) (admin.UpdatesResult, error) {
	state, err := s.state(ctx)
	if err != nil {
		return admin.UpdatesResult{}, err
	}
	if state.Applying {
		return admin.UpdatesResult{Busy: true}, nil
	}
	target := state.ApplyTarget()
	if skipBackup {
		target = state.ApplyWithoutBackupTarget()
	}
	if target == "" {
		return admin.UpdatesResult{}, admin.ErrNoUpdate
	}
	if !s.applying.CompareAndSwap(false, true) {
		return admin.UpdatesResult{Target: target, Busy: true}, nil
	}
	parent := context.Background()
	if s.base != nil {
		if c := s.base(); c != nil {
			parent = c
		}
	}
	bg, cancel := context.WithTimeout(parent, updatesApplyLimit)
	s.resetBanner()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer s.applying.Store(false)
		defer s.resetBanner()
		st, err := s.run.Run(bg, updater.RunOpts{Apply: target, Managed: true, SkipBackup: skipBackup})
		if err != nil {
			s.logger.Error("update now failed", "target", target, "err", err)
			return
		}
		s.logger.Info("update now finished", "target", target, "decision", st.Decision, "reason", st.Reason)
	}()
	return admin.UpdatesResult{Target: target, Started: true}, nil
}

// Wait blocks until the background Update now run has ended or ctx is done.
// The server calls it after cancelling the lifecycle context.
func (s *adminUpdates) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *adminUpdates) Unpin(ctx context.Context) error {
	if err := s.store.SavePolicy(ctx, updater.Settings{}, updater.ColPin); err != nil {
		return err
	}
	s.resetBanner()
	return nil
}

// Banner is the owner banner, recomputed at most every updatesBannerTTL.
func (s *adminUpdates) Banner(ctx context.Context) (*admin.UpdateBanner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bannerSet && s.now().Sub(s.bannerAt) < updatesBannerTTL {
		return s.banner, nil
	}
	state, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	// The banner no longer reads the stamp, but a page view is still what keeps it in step.
	if _, err := s.observeOff(ctx, state.Mode); err != nil {
		return nil, err
	}
	// The prefix is left empty: Admin.updateBanner sets the real link.
	s.banner = admin.PickBannerFor("", admin.UpdatesBannerInput{
		Status: state.Status, Mode: state.Mode, Running: state.Running, Dev: state.Dev,
	}, s.now())
	s.bannerAt, s.bannerSet = s.now(), true
	return s.banner, nil
}

// Glance is what the top-bar chip shows, recomputed at most every
// updatesBannerTTL. Unlike View it never writes (no off-since stamp), so an
// owner page view costs one cached read.
func (s *adminUpdates) Glance(ctx context.Context) (admin.UpdatesGlance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The live run flag is read on every call: the cached copy would show a
	// run that started after it was taken as idle.
	if s.glanceSet && s.now().Sub(s.glanceAt) < updatesBannerTTL {
		g := s.glance
		g.Applying = s.applying.Load()
		return g, nil
	}
	state, err := s.state(ctx)
	if err != nil {
		return admin.UpdatesGlance{}, err
	}
	g := admin.UpdatesGlance{Running: state.Running, Dev: state.Dev, Mode: state.Mode, Channel: state.Channel, Status: state.Status}
	if g.OffSince, err = s.log.OffSince(ctx); err != nil {
		s.logger.Warn("update off-since", "err", err) // the chip is optional
	}
	if g.History, err = s.log.History(ctx, updatesHistoryLimit); err != nil {
		s.logger.Warn("update history", "err", err)
		g.History = nil
	}
	s.glance, s.glanceAt, s.glanceSet = g, s.now(), true
	g.Applying = s.applying.Load()
	return g, nil
}

func (s *adminUpdates) resetBanner() {
	s.mu.Lock()
	s.bannerSet = false
	s.glanceSet = false
	s.mu.Unlock()
}

// pgUpdatesLog reads hatamoto_updates and keeps off_since on the settings row.
type pgUpdatesLog struct{ db pgxQuerier }

type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (p pgUpdatesLog) History(ctx context.Context, limit int) ([]admin.UpdatesHistoryRow, error) {
	rows, err := p.db.Query(ctx, `
		SELECT started_at, from_version, to_version, phase, error
		FROM hatamoto_updates ORDER BY started_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []admin.UpdatesHistoryRow
	for rows.Next() {
		var h admin.UpdatesHistoryRow
		if err := rows.Scan(&h.When, &h.From, &h.To, &h.Phase, &h.Error); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (p pgUpdatesLog) OffSince(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	err := p.db.QueryRow(ctx, `SELECT off_since FROM hatamoto_update_settings`).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return at, err
}

// SetOffSince stamps (creating the settings row when there is none) or clears.
func (p pgUpdatesLog) SetOffSince(ctx context.Context, at *time.Time) error {
	if at == nil {
		_, err := p.db.Exec(ctx, `UPDATE hatamoto_update_settings SET off_since = NULL WHERE off_since IS NOT NULL`)
		return err
	}
	_, err := p.db.Exec(ctx, `
		INSERT INTO hatamoto_update_settings (id, off_since) VALUES (TRUE, $1)
		ON CONFLICT (id) DO UPDATE SET off_since = COALESCE(hatamoto_update_settings.off_since, EXCLUDED.off_since)`, *at)
	return err
}
