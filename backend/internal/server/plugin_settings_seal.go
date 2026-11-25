package server

import (
	"context"
	"log/slog"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/plugmanifest"
	"hatamoto.dev/sdk"
)

// pluginSettingsSchemas maps installed plugins to their settings_schema
// (manifests that do not decode or declare none are skipped). It reads the
// package table, so plugins that fail to load count.
func pluginSettingsSchemas(rows []packages.Row) map[string][]byte {
	out := map[string][]byte{}
	for _, r := range rows {
		if r.Kind != packages.KindPlugin {
			continue
		}
		var m sdk.Manifest
		if plugmanifest.Decode(r.Manifest, &m) != nil || len(m.SettingsSchema) == 0 {
			continue
		}
		out[r.Name] = m.SettingsSchema
	}
	return out
}

// sealPluginSettings seals plain write-only plugin settings. It runs once per
// boot, before any plugin loads; a failure is logged and boot continues. Logs
// carry counts only.
func (s *Server) sealPluginSettings(ctx context.Context) {
	if s.pluginSettings == nil || s.packages == nil {
		return
	}
	rows, err := s.packages.Store().List(ctx)
	if err != nil {
		s.logger.Error("plugin settings: cannot list plugins to seal", slog.Any("error", err))
		return
	}
	rep, err := s.pluginSettings.SealExisting(ctx, pluginSettingsSchemas(rows))
	if err != nil {
		s.logger.Error("plugin settings: sealing write-only values failed", slog.Any("error", err))
	}
	attrs := []any{slog.Int("count", rep.Sealed), slog.Int("plugins", rep.Plugins)}
	if rep.Unreadable > 0 {
		attrs = append(attrs, slog.Int("unreadable", rep.Unreadable))
	}
	s.logger.Info("plugin settings: sealed write-only values", attrs...)
}
