package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

const legacyWebhooksPlugin = packages.ReservedPluginName

// retireWebhooksPlugin moves the former first-party webhooks plugin into core:
// its subscriptions are copied (same ids and secrets), then the plugin is
// uninstalled with its data. It runs in setupPackages before the packages are
// reconciled, so the plugin is never loaded here and its cron never runs again,
// and before the scheduler starts, so nothing is sent twice.
//
// It is idempotent (no plugin and no schema: nothing happens), only touches a
// plugin signed by a first-party key, never uninstalls
// when the copy failed (the plugin then keeps working; the next boot retries),
// and never logs a secret.
//
// A schema with no package row is deliberately not imported: nothing proves the
// data is the first-party plugin's (anyone could have created that schema), and
// importing it would copy unvouched URLs and secrets into core. The admin notice
// therefore never offers Remove until the import is recorded (webhooksImportRecorded).
func (s *Server) retireWebhooksPlugin(ctx context.Context) {
	if s.webhooks == nil || s.packages == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	log := s.logger.With(slog.String("plugin", legacyWebhooksPlugin))

	row, err := s.packages.Store().Get(ctx, packages.KindPlugin, legacyWebhooksPlugin)
	installed := err == nil
	if err != nil && !errors.Is(err, packages.ErrNotFound) {
		log.Error("webhooks: cannot look for the old plugin; it is left alone", slog.Any("error", err))
		return
	}
	// pg_namespace and pg_class, not information_schema: that hides what the app role has no privilege on.
	var schema, table bool
	if err := s.db.Pool().QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1),
		EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = 'subscriptions' AND c.relkind = 'r')`, pluginhost.SchemaName(legacyWebhooksPlugin)).Scan(&schema, &table); err != nil {
		log.Error("webhooks: cannot look for the old plugin's data; it is left alone", slog.Any("error", err))
		return
	}
	if !installed && !schema {
		return
	}
	// Only the real first-party plugin is retired. A package by that name signed
	// by anyone else (or unsigned) is not ours; and data with no package row
	// belongs to nobody we can vouch for. Both are left alone.
	if !installed {
		log.Warn("webhooks: a plugin_webhooks schema exists without a package row; it is not imported and not dropped")
		return
	}
	if !firstPartySigned(row) {
		log.Warn("webhooks: a plugin named webhooks is installed but is not signed by a first-party key; it is not imported and not uninstalled. Re-create its subscriptions in Settings > Webhooks, then uninstall the plugin from Plugins",
			slog.String("signer", row.SignerKeyID))
		return
	}

	// Read before the uninstall deletes the grants. Subscriptions are imported
	// paused unless the plugin was really sending: enabled, installed, still
	// passing this instance's trust policy and granted everything it needed to
	// send (db to read them, http.fetch to call out, http.fetch.any for any host).
	// The owner resumes them in Settings > Webhooks.
	paused := !row.Enabled || row.State != packages.StateInstalled || s.packages.Usable(row) != nil
	if !paused {
		var granted bool
		if err := s.db.Pool().QueryRow(ctx, `SELECT coalesce(permissions @> $2::text[], false)
			FROM hatamoto_plugin_grants WHERE plugin = $1`, legacyWebhooksPlugin,
			[]string{string(sdk.PermDB), string(sdk.PermHTTPFetch), string(sdk.PermHTTPFetchAny)}).Scan(&granted); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			log.Error("webhooks: cannot read the old plugin's grants; it is left alone", slog.Any("error", err))
			return
		}
		paused = !granted
	}

	copied := 0
	if table {
		if s.pluginDB == nil {
			log.Error("webhooks: the old plugin's subscriptions cannot be read without the plugin runtime; the plugin is kept")
			return
		}
		// Nothing is provisioned yet this early in boot; Query needs the plugin's role state.
		if err := s.pluginDB.Provision(ctx, legacyWebhooksPlugin); err != nil {
			log.Error("webhooks: the old plugin's database role is not usable; the plugin is kept and the copy retried next start", slog.Any("error", err))
			return
		}
		n, err := s.webhooks.Repo().ImportPluginSubscriptions(ctx, s.pluginDB, paused)
		if err != nil {
			log.Error("webhooks: copying the old plugin's subscriptions failed; the plugin is kept and the copy retried next start", slog.Any("error", err))
			return
		}
		copied = n
	}

	uninstalled := false
	if err := s.packages.Uninstall(ctx, packages.KindPlugin, legacyWebhooksPlugin, true); err != nil {
		log.Error("webhooks: uninstalling the old plugin failed; it is retried next start", slog.Any("error", err))
	} else {
		uninstalled = true
	}
	s.packages.Audit(ctx, "webhooks.import_plugin", "plugin:"+legacyWebhooksPlugin,
		map[string]any{"copied": copied, "uninstalled": uninstalled, "paused": paused})
	log.Info("webhooks: the old plugin was moved into core", slog.Int("copied", copied), slog.Bool("uninstalled", uninstalled), slog.Bool("paused", paused))
}

// firstPartySigned reports whether the package row was signed by a first-party
// key. Whether that key is still trusted is a separate question (Usable).
func firstPartySigned(row packages.Row) bool {
	if !row.Signed() {
		return false
	}
	return packages.IsFirstPartyKey(row.SignerKeyID)
}

// webhooksPluginLeftBehind reports whether a plugin named webhooks is installed
// after boot that the boot step did not retire because it is not first-party.
// A first-party row is not "left behind": it is retired (or retried) at boot.
// The Settings > Webhooks page tells the owner how to move.
func (s *Server) webhooksPluginLeftBehind(ctx context.Context) bool {
	if s.packages == nil {
		return false
	}
	row, err := s.packages.Store().Get(ctx, packages.KindPlugin, legacyWebhooksPlugin)
	return err == nil && !firstPartySigned(row)
}

// webhooksImportRecorded reports whether the boot step finished copying the old
// plugin's subscriptions into core: it writes the webhooks.import_plugin audit
// record only after the copy committed (a failed copy returns before it). False
// when unsure (no record, or the lookup failed), which keeps the Remove button hidden.
func (s *Server) webhooksImportRecorded(ctx context.Context) bool {
	var done bool
	err := s.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_admin_audit
		WHERE action = 'webhooks.import_plugin' AND target = $1)`, "plugin:"+legacyWebhooksPlugin).Scan(&done)
	return err == nil && done
}
