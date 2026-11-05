package server

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/tags"
	"hatamoto.dev/sdk"
)

// legacyTrackingPlugins maps the retired plugins to the tag manager vendor they
// became and the plugin setting that held the id. firstParty says whether the
// release before this one trusted the plugin with site.scripts: google-analytics
// was never in the first-party list, so policy refused its script and it never
// emitted anything.
var legacyTrackingPlugins = []struct {
	plugin, vendor, key string
	firstParty          bool
}{
	{"gtm", "gtm", "container_id", true},
	{"google-analytics", "ga4", "measurement_id", false},
}

// retireTrackingPlugins folds the former gtm and google-analytics plugins into
// Settings > Tracking: the id is copied (unless the owner already configured the
// vendor there) and the vendor turned on only when the plugin was really
// emitting its tag (installed, enabled, usable and granted site.scripts, and
// trusted with scripts); otherwise it is copied off. Then the plugin is uninstalled. It runs
// in setupPackages before the packages are reconciled, like retireWebhooksPlugin,
// so the plugin never loads again and the tag is never emitted twice.
//
// It is idempotent, only touches a plugin signed by a first-party key, and
// keeps the plugin when the copy failed or its id is not valid for the vendor. Ids are not
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
			log.Warn("tracking: a plugin with this name is installed but is not signed by a first-party key; it is left alone. Enter its ID in Settings > Privacy & tracking > Tracking, then uninstall the plugin from Plugins")
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
		// An id the vendor pattern rejects cannot be copied; uninstalling would
		// destroy the only copy of it, so the plugin and its settings stay.
		if cur := set.Vendors[lp.vendor]; id != "" && !cur.Enabled && cur.ID == "" {
			if cand, _ := tags.ByID(lp.vendor); !cand.ValidID(cand.NormalizeID(id)) {
				log.Warn("tracking: the old plugin's ID is not a valid ID for this tag; the plugin and its settings are kept. Enter the ID in Settings > Privacy & tracking > Tracking, then uninstall the plugin from Plugins")
				continue
			}
		}
		// Read before the uninstall deletes the grants. A tracker that was not
		// running must not start sending visitor data because of the upgrade.
		running := row.Enabled && row.State == packages.StateInstalled && lp.firstParty && s.packages.Usable(row) == nil
		if running {
			var granted bool
			if err := s.db.Pool().QueryRow(ctx, `SELECT coalesce(permissions @> $2::text[], false)
				FROM hatamoto_plugin_grants WHERE plugin = $1`, lp.plugin,
				[]string{string(sdk.PermSiteScripts)}).Scan(&granted); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				log.Error("tracking: cannot read the old plugin's grants; the plugin is kept", slog.Any("error", err))
				continue
			}
			running = granted
		}
		copied := false
		if cur := set.Vendors[lp.vendor]; id != "" && !cur.Enabled && cur.ID == "" {
			cand, _ := tags.ByID(lp.vendor)
			if norm := cand.NormalizeID(id); cand.ValidID(norm) {
				set.Vendors[lp.vendor] = tags.VendorState{Enabled: running, ID: norm}
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
		uninstalled := s.packages.Uninstall(ctx, packages.KindPlugin, lp.plugin, packages.UninstallOpts{DropData: true}) == nil
		if !uninstalled {
			log.Error("tracking: uninstalling the old plugin failed; it is retried next start")
		}
		s.packages.Audit(ctx, "tracking.import_plugin", "plugin:"+lp.plugin,
			map[string]any{"vendor": lp.vendor, "copied": copied, "enabled": copied && running, "uninstalled": uninstalled})
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
	if jsonx.Unmarshal(raw, &m, jsonx.Stored) != nil {
		return "", nil
	}
	v, _ := m[key].(string)
	return v, nil
}

// legacyTrackingLeftBehind names the retired tracking plugins still installed
// after boot: not first-party signed, or kept because their id could not be copied.
func (s *Server) legacyTrackingLeftBehind(ctx context.Context) []string {
	if s.packages == nil {
		return nil
	}
	var out []string
	for _, lp := range legacyTrackingPlugins {
		if _, err := s.packages.Store().Get(ctx, packages.KindPlugin, lp.plugin); err == nil {
			out = append(out, lp.plugin)
		}
	}
	return out
}
