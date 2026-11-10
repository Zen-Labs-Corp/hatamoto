package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/render"
)

// statsSources answers the facts the Detailed install stats report. It reads
// names, versions and counts only: never the update error text, never a
// package payload.
type statsSources struct {
	pool   *pgxpool.Pool
	pkgs   *packages.Store
	worker bool // the background worker runs in this process
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

func (s statsSources) WorkerOn() bool { return s.worker }

// isFirstPartyOrigin is true for a package a first-party key signed (first-party
// and community builds), never for a developer package or a self-built one.
func isFirstPartyOrigin(o packages.Origin) bool {
	return o == packages.OriginFirstParty || o == packages.OriginCommunity
}
