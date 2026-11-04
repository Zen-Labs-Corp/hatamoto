package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/tags"
)

// legacyTrackingPlugins maps the retired first-party plugins to the tag manager
// vendor they became and the plugin setting that held the id.
var legacyTrackingPlugins = []struct{ plugin, vendor, key string }{
	{"gtm", "gtm", "container_id"},
	{"google-analytics", "ga4", "measurement_id"},
}

// retireTrackingPlugins folds the former gtm and google-analytics plugins into
// Settings > Tracking: the id is copied and the vendor turned on (unless the
// owner already configured it there), then the plugin is uninstalled. It runs
// in setupPackages before the packages are reconciled, like retireWebhooksPlugin,
// so the plugin never loads again and the tag is never emitted twice.
//
// It is idempotent, only touches a plugin signed by a first-party key, and
// keeps the plugin when the copy failed (the next boot retries). Ids are not
// secrets but the audit record carries only the plugin names anyway.
func (s *Server) retireTrackingPlugins(ctx context.Context) {
	if s.tagsStore == nil || s.packages == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for _, lp := range legacyTrackingPlugins {
		log := s.logger.With(slog.String("plugin", lp.plugin))
		row, err := s.packages.Store().Get(ctx, packages.KindPlugin, lp.plugin)
		if errors.Is(err, packages.ErrNotFound) {
			continue
		}
		if err != nil {
			log.Error("tracking: cannot look for the old plugin; it is left alone", slog.Any("error", err))
			continue
		}
		if !firstPartySigned(row) {
			log.Warn("tracking: a plugin with this name is installed but is not signed by a first-party key; it is left alone. Enter its ID in Settings > Tracking, then uninstall the plugin from Plugins")
			continue
		}
		id, err := s.legacyPluginID(ctx, lp.plugin, lp.key)
		if err != nil {
			log.Error("tracking: cannot read the old plugin's settings; the plugin is kept", slog.Any("error", err))
			continue
		}
		set, err := s.tagsStore.Get(ctx)
		if err != nil {
			log.Error("tracking: cannot read the tracking settings; the plugin is kept", slog.Any("error", err))
			continue
		}
		copied := false
		if cur := set.Vendors[lp.vendor]; id != "" && !cur.Enabled && cur.ID == "" {
			cand, _ := tags.ByID(lp.vendor)
			if norm := cand.NormalizeID(id); cand.ValidID(norm) {
				set.Vendors[lp.vendor] = tags.VendorState{Enabled: row.Enabled, ID: norm}
				copied = true
			}
		}
		if !slices.Contains(set.MovedFrom, lp.plugin) {
			set.MovedFrom = append(set.MovedFrom, lp.plugin)
		}
		set.NoticeDismissed = false
		if err := s.tagsStore.Save(ctx, set); err != nil {
			log.Error("tracking: copying the old plugin's ID failed; the plugin is kept and the copy retried next start", slog.Any("error", err))
			continue
		}
		uninstalled := s.packages.Uninstall(ctx, packages.KindPlugin, lp.plugin, true) == nil
		if !uninstalled {
			log.Error("tracking: uninstalling the old plugin failed; it is retried next start")
		}
		s.packages.Audit(ctx, "tracking.import_plugin", "plugin:"+lp.plugin,
			map[string]any{"vendor": lp.vendor, "copied": copied, "uninstalled": uninstalled})
		log.Info("tracking: the old plugin was moved into core", slog.Bool("copied", copied), slog.Bool("uninstalled", uninstalled))
	}
}

// legacyPluginID reads one string setting of a plugin; "" when none is stored.
func (s *Server) legacyPluginID(ctx context.Context, plugin, key string) (string, error) {
	var raw []byte
	err := s.db.Pool().QueryRow(ctx, `SELECT settings FROM hatamoto_plugin_settings WHERE plugin = $1`, plugin).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", nil
	}
	v, _ := m[key].(string)
	return v, nil
}

// legacyTrackingLeftBehind names the retired tracking plugins still installed
// after boot because they are not first-party signed.
func (s *Server) legacyTrackingLeftBehind(ctx context.Context) []string {
	if s.packages == nil {
		return nil
	}
	var out []string
	for _, lp := range legacyTrackingPlugins {
		if row, err := s.packages.Store().Get(ctx, packages.KindPlugin, lp.plugin); err == nil && !firstPartySigned(row) {
			out = append(out, lp.plugin)
		}
	}
	return out
}
