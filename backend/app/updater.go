package app

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/backup"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/updater"
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

// startUpdater hands the update agent to the server. A problem here (say an
// unusable update URL) is logged and never stops the site from starting.
func startUpdater(srv *server.Server, pool *pgxpool.Pool, mediaDir string, demo bool, logger *slog.Logger) {
	// One engine serves the updater's pre-update backup and the admin page.
	version, _ := buildInfo()
	eng := newBackupEngine(pool, mediaDir, version, logger)
	srv.SetBackups(eng)
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
