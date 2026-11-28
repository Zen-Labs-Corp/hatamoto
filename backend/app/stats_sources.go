package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/wpimport"
)

// statsSources answers the facts the Detailed install stats report. It reads
// names, versions and counts only: never the update error text, never a
// package payload.
type statsSources struct {
	pool   *pgxpool.Pool
	pkgs   *packages.Store
	worker bool // the background worker runs in this process
	// baseURL is the PUBLIC_SITE base URL from the environment; it wins over
	// the stored site config, like the admin address check.
	baseURL string
	csp     bool // CSP_ENFORCE: the policy is enforced, not report-only
}

var _ installstats.Sources = statsSources{}

// Plugins lists the installed plugins and whether a first-party key signed
// each one (so the sender may name it).
func (s statsSources) Plugins(ctx context.Context) ([]installstats.PluginInfo, error) {
	rows, err := s.pkgs.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []installstats.PluginInfo
	for _, r := range rows {
		if r.Kind != packages.KindPlugin || r.State != packages.StateInstalled {
			continue
		}
		out = append(out, installstats.PluginInfo{
			Name: r.Name, Version: r.Version, Enabled: r.Enabled,
			FirstParty: isFirstPartyOrigin(packages.ClassifyRow(r)),
		})
	}
	return out, nil
}

// Theme is the active theme: the stored row, or the built-in default with the
// version from its embedded manifest.
func (s statsSources) Theme(ctx context.Context) (installstats.ThemeInfo, error) {
	cfg, err := s.pkgs.Config(ctx)
	if err != nil {
		return installstats.ThemeInfo{}, err
	}
	if cfg.ActiveTheme != "" && cfg.ActiveTheme != packages.DefaultTheme {
		rows, err := s.pkgs.List(ctx)
		if err != nil {
			return installstats.ThemeInfo{}, err
		}
		for _, r := range rows {
			if r.Kind == packages.KindTheme && r.Name == cfg.ActiveTheme {
				return installstats.ThemeInfo{Name: r.Name, Version: r.Version}, nil
			}
		}
		// The stored theme is gone, so the built-in one is what renders.
	}
	return builtinTheme()
}

// builtinTheme reads the version of the embedded default theme.
func builtinTheme() (installstats.ThemeInfo, error) {
	b, err := fs.ReadFile(render.DefaultFS(), "theme.json")
	if err != nil {
		return installstats.ThemeInfo{}, fmt.Errorf("read built-in theme manifest: %w", err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := jsonx.Unmarshal(b, &m, jsonx.Stored); err != nil {
		return installstats.ThemeInfo{}, fmt.Errorf("decode built-in theme manifest: %w", err)
	}
	return installstats.ThemeInfo{Name: packages.DefaultTheme, Version: m.Version}, nil
}

// PostgresMajor is the server's major version (9.6 reports 9).
func (s statsSources) PostgresMajor(ctx context.Context) (int, error) {
	var num string
	if err := s.pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&num); err != nil {
		return 0, fmt.Errorf("read postgres version: %w", err)
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, fmt.Errorf("parse postgres version %q: %w", num, err)
	}
	return n / 10000, nil // 9.6 is 90600: major 9; 16.2 is 160002: major 16
}

// InstallStart is when the first migration ran. goose stores the time without
// a zone, in the session's time zone, so it is read back in that zone.
func (s statsSources) InstallStart(ctx context.Context) (time.Time, error) {
	var t *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT min(tstamp) AT TIME ZONE current_setting('TimeZone') FROM goose_db_version WHERE version_id > 0`).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("read install start: %w", err)
	}
	if t == nil {
		return time.Time{}, errors.New("no migrations recorded")
	}
	return *t, nil
}

// UpdateCounts counts the updates that failed or were rolled back since a time.
func (s statsSources) UpdateCounts(ctx context.Context, since time.Time) (failed, rolledBack int64, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE phase = 'failed'), count(*) FILTER (WHERE phase = 'rolled_back')
		FROM hatamoto_updates WHERE started_at >= $1`, since).Scan(&failed, &rolledBack)
	if err != nil {
		return 0, 0, fmt.Errorf("count updates: %w", err)
	}
	return failed, rolledBack, nil
}

// Host is the raw public base URL, read on every call because the owner can
// change it at runtime. No stored row means no host.
func (s statsSources) Host(ctx context.Context) (string, error) {
	if base := strings.TrimSpace(s.baseURL); base != "" {
		return base, nil
	}
	var base string
	err := s.pool.QueryRow(ctx, `SELECT public_base_url FROM hatamoto_site_config LIMIT 1`).Scan(&base)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read public base url: %w", err)
	}
	return base, nil
}

func (s statsSources) WorkerOn() bool { return s.worker }

// isFirstPartyOrigin is true for a package a first-party key signed (first-party
// and community builds), never for a developer package or a self-built one.
func isFirstPartyOrigin(o packages.Origin) bool {
	return o == packages.OriginFirstParty || o == packages.OriginCommunity
}

// Flags reads one on/off fact per feature: settings switches and counts only,
// never content, visitors or secret columns. A failed read leaves that flag nil.
func (s statsSources) Flags(ctx context.Context) installstats.Flags {
	f := installstats.Flags{CSPEnforced: &s.csp}
	f.CommentsOn = s.flag(ctx, `SELECT enabled FROM hatamoto_comment_settings WHERE id`)
	f.StaticMode = s.flag(ctx, `SELECT mode = $1 FROM hatamoto_static_hosting_settings WHERE id`, staticexport.ModeStatic)
	f.SSOOn = s.flag(ctx, `SELECT enabled FROM hatamoto_signin_oidc WHERE id`)
	f.FormsInUse = s.flag(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_forms WHERE status = 'active')`)
	f.OffsiteBackupsOn = s.flag(ctx, `SELECT enabled FROM hatamoto_offsite_settings WHERE id`)
	f.WPImported = s.flag(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_import_runs WHERE source = $1 AND status = $2)`,
		wpimport.SourceName, wpimport.RunCompleted)
	var n int
	err := s.pool.QueryRow(ctx, `SELECT jsonb_array_length(locales) FROM hatamoto_site_config LIMIT 1`).Scan(&n)
	if err == nil || errors.Is(err, pgx.ErrNoRows) { // no row = single locale
		f.Locales = &n
	}
	return f
}

// flag runs a one-boolean query; no row is off, an error is unknown (nil).
func (s statsSources) flag(ctx context.Context, q string, args ...any) *bool {
	var on bool
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&on); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return &on
}
