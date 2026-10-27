package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/updater"
	"hatamoto.dev/core/internal/worker"
	"hatamoto.dev/sdk"
)

// updateJob is the worker job name WORKER_DISABLE_JOBS uses for the update check.
const updateJob = "update_check"

// SetUpdater builds the update agent from env, adding what only the server
// knows (installed packages, site timezone), and puts its periodic check on
// the worker scheduler. Call it before Start. With the worker off, or the job
// disabled by WORKER_DISABLE_JOBS, nothing is scheduled; the agent is built
// anyway so a bad update URL is reported at boot.
func (s *Server) SetUpdater(env updater.Env) error {
	if s.packages != nil {
		env.Plugins = pluginSource{pk: s.packages.Store()}
	}
	if s.siteConfig != nil {
		env.Site = func(ctx context.Context) *time.Location {
			cfg, err := s.siteConfig.Get(ctx)
			if err != nil || cfg == nil || cfg.Timezone == "" {
				return time.UTC
			}
			loc, err := time.LoadLocation(cfg.Timezone)
			if err != nil {
				return time.UTC
			}
			return loc
		}
	}
	env.Log = s.logger
	u, err := updater.New(env)
	if err != nil {
		err = fmt.Errorf("update agent: %w", err)
		s.NoUpdater(err)
		return err
	}
	s.updater = u
	s.refreshAdminUpdates()
	if s.scheduler != nil {
		scheduleUpdater(s.scheduler, u, s.logger)
	}
	return nil
}

// NoUpdater records why there is no update agent, for the Updates page.
func (s *Server) NoUpdater(err error) {
	s.updaterProblem = err.Error()
	s.refreshAdminUpdates()
}

// scheduleUpdater registers the check unless WORKER_DISABLE_JOBS names it. It
// reports whether the job was scheduled.
func scheduleUpdater(sch *worker.Scheduler, u *updater.Updater, log *slog.Logger) bool {
	if !workerJobEnabled(updateJob) {
		return false
	}
	if err := u.Schedule(sch); err != nil {
		log.Warn("update check not scheduled", "error", err)
		return false
	}
	return true
}

// UpdatePlugins is the preflight's view of the installed packages, for the
// command line as well as the server.
func UpdatePlugins(pk *packages.Store) updater.PluginSource { return pluginSource{pk: pk} }

// packageReader is the part of packages.Store the preflight reads.
type packageReader interface {
	List(ctx context.Context) ([]packages.Row, error)
	Config(ctx context.Context) (packages.Config, error)
	Get(ctx context.Context, kind packages.Kind, name string) (packages.Row, error)
}

// pluginSource lists the SDK ranges the update preflight checks: every enabled
// plugin and the active theme. A theme may carry an optional "sdk" key. A
// failure to read any of it blocks the update (an unknown theme must not let an
// incompatible SDK through); only "no theme chosen" and "theme not installed"
// are fine.
type pluginSource struct{ pk packageReader }

func (p pluginSource) EnabledSDKRanges(ctx context.Context) ([]updater.NamedRange, error) {
	rows, err := p.pk.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []updater.NamedRange
	for _, r := range rows {
		if !r.Enabled || r.State == packages.StateUninstalling || r.Kind != packages.KindPlugin {
			continue
		}
		var m sdk.Manifest
		if err := json.Unmarshal(r.Manifest, &m); err != nil {
			return nil, fmt.Errorf("plugin %s manifest: %w", r.Name, err)
		}
		if m.SDK != "" {
			out = append(out, updater.NamedRange{Kind: "plugin", Name: r.Name, Range: m.SDK})
		}
	}
	cfg, err := p.pk.Config(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil // no package config row yet: no theme chosen
	}
	if err != nil {
		return nil, fmt.Errorf("active theme: %w", err)
	}
	if cfg.ActiveTheme == "" {
		return out, nil // the built-in theme declares nothing
	}
	row, err := p.pk.Get(ctx, packages.KindTheme, cfg.ActiveTheme)
	if errors.Is(err, packages.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active theme %s: %w", cfg.ActiveTheme, err)
	}
	var t struct {
		SDK string `json:"sdk"`
	}
	if json.Unmarshal(row.Manifest, &t) == nil && t.SDK != "" {
		out = append(out, updater.NamedRange{Kind: "theme", Name: row.Name, Range: t.SDK})
	}
	return out, nil
}
