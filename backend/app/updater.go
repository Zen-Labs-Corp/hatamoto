package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/updater"
	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/core/internal/wpimport"
)

// importProbe tells the updater whether a content import holds its lock.
type importProbe struct{ pool *pgxpool.Pool }

func (p importProbe) ImportRunning(ctx context.Context) (bool, error) {
	return wpimport.ImportRunning(ctx, p.pool)
}

// updaterEnv is the update agent's view of this process.
func updaterEnv(pool *pgxpool.Pool, eng *backup.Engine, logger *slog.Logger) (updater.Env, error) {
	sup, err := superviseConfig(logger)
	if err != nil {
		return updater.Env{}, err
	}
	version, _ := buildInfo()
	return updater.Env{
		Data:        config.ResolveDataDir(),
		Version:     version,
		Supervisor:  sup,
		Store:       updater.NewStore(pool),
		Imports:     importProbe{pool},
		Backups:     preUpdateBackups{eng},
		DatabaseURL: os.Getenv("DATABASE_URL"),
		BaseURL:     os.Getenv("HATAMOTO_UPDATE_URL"),
		Log:         logger,
	}, nil
}

// installStats builds the install stats service. Its channel is the
// update channel; the update mode does not matter, stats have their own switch.
func installStats(pool *pgxpool.Pool, version string, worker, demo bool, logger *slog.Logger) *installstats.Service {
	us := updater.NewStore(pool)
	h := installstats.NewHealth()
	wasmhost.SetErrorObserver(h.PluginError)
	svc := installstats.New(installstats.Config{
		Store:   installstats.NewStore(pool),
		Health:  h,
		Sources: statsSources{pool: pool, pkgs: packages.NewStore(pool), worker: worker},
		Version: version,
		Demo:    demo,
		SetupComplete: func(ctx context.Context) (bool, error) {
			var done bool
			err := pool.QueryRow(ctx, `SELECT setup_completed_at IS NOT NULL FROM hatamoto_site_config LIMIT 1`).Scan(&done)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return done, err
		},
		Channel: func(ctx context.Context) string {
			set, _, err := us.Load(ctx)
			if err != nil {
				return ""
			}
			return set.Effective(os.Getenv).Channel
		},
		Log: logger,
	})
	countMigrationFailures(svc, h, config.ResolveDataDir(), logger)
	return svc
}

// countMigrationFailures adds the failed migrations recorded by earlier boots
// (a failed migration usually ends in a rollback, so the count waits in a
// file) to the health counts, and removes the file only once they are stored.
func countMigrationFailures(svc *installstats.Service, h *installstats.Health, dataDir string, logger *slog.Logger) {
	n := readMigrationFailures(dataDir)
	if n == 0 {
		return
	}
	h.MigrationFailed(int64(n))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Flush(ctx); err != nil {
		logger.Warn("install stats: could not store the migration failure count", "error", err)
		return
	}
	clearMigrationMarker(dataDir)
}

// startUpdater hands the update agent to the server. A problem here (say an
// unusable update URL) is logged and never stops the site from starting.
func startUpdater(srv *server.Server, pool *pgxpool.Pool, mediaDir string, demo, worker bool, logger *slog.Logger) {
	// One engine serves the updater's pre-update backup and the admin page.
	version, _ := buildInfo()
	eng := newBackupEngine(pool, mediaDir, version, logger)
	srv.SetBackups(eng)
	srv.SetStats(installStats(pool, version, worker, demo, logger))
	if demo {
		// Public demo: no update check, no apply. The Updates page says so.
		srv.NoUpdater(errors.New("updates are switched off in the public demo"))
		return
	}
	env, err := updaterEnv(pool, eng, logger)
	if err == nil {
		err = srv.SetUpdater(env)
	}
	if err != nil {
		srv.NoUpdater(err)
		logger.Warn("automatic updates are unavailable", "error", err)
	}
}
