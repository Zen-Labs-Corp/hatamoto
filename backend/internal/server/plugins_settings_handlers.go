package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/pluginhost"
)

// pluginSettingsStore is the persistence the settings handlers need.
type pluginSettingsStore interface {
	Get(ctx context.Context, plugin string) (json.RawMessage, error)
	Put(ctx context.Context, plugin string, v json.RawMessage) error
}

const maxPluginSettingsBody = 256 << 10

// pluginSettingsTarget resolves {name} to a plugin that declares a settings
// schema. It writes the 404 itself and returns ok=false when none matches.
func (s *Server) pluginSettingsTarget(w http.ResponseWriter, r *http.Request) (name string, schema []byte, ok bool) {
	name = r.PathValue("name")
	if s.pluginSettingsStore != nil {
		for _, st := range s.pluginStatuses() {
			if st.Name == name && len(st.Manifest.SettingsSchema) > 0 {
				return name, st.Manifest.SettingsSchema, true
			}
		}
	}
	writeError(w, http.StatusNotFound, "plugin has no settings")
	return "", nil, false
}

// view is what the API returns: schema defaults merged, secrets masked.
func pluginSettingsView(schema []byte, stored json.RawMessage) json.RawMessage {
	return pluginhost.MaskWriteOnly(schema, pluginhost.MergeDefaults(schema, stored))
}

// handleGetPluginSettings returns GET /api/v1/plugins/{name}/settings.
func (s *Server) handleGetPluginSettings(w http.ResponseWriter, r *http.Request) {
	name, schema, ok := s.pluginSettingsTarget(w, r)
	if !ok {
		return
	}
	stored, err := s.pluginSettingsStore.Get(r.Context(), name)
	if err != nil {
		s.logger.Error("load plugin settings", slog.String("plugin", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, pluginSettingsView(schema, stored))
}

// handlePutPluginSettings validates the body against the plugin's schema
// and persists it. Violations return
// 400 {"error":"validation failed","fields":[{"path","message"}]}.
func (s *Server) handlePutPluginSettings(w http.ResponseWriter, r *http.Request) {
	name, schema, ok := s.pluginSettingsTarget(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPluginSettingsBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		writeJSON(w, http.StatusBadRequest, validationFailed([]pluginhost.FieldError{{Path: "/", Message: "settings must be a JSON object"}}))
		return
	}
	compiled, err := pluginhost.CompileSchema(schema)
	if err != nil {
		s.logger.Error("plugin settings schema", slog.String("plugin", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "plugin settings schema is invalid")
		return
	}
	old, err := s.pluginSettingsStore.Get(r.Context(), name)
	if err != nil {
		s.logger.Error("load plugin settings", slog.String("plugin", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	merged := pluginhost.PreserveWriteOnly(schema, old, body)
	if fields := pluginhost.Validate(compiled, merged); len(fields) > 0 {
		writeJSON(w, http.StatusBadRequest, validationFailed(fields))
		return
	}
	if err := s.pluginSettingsStore.Put(r.Context(), name, merged); err != nil {
		s.logger.Error("save plugin settings", slog.String("plugin", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	s.invalidateRendered(r.Context()) // plugin settings feed template funcs, filters and sitemaps
	writeJSON(w, http.StatusOK, pluginSettingsView(schema, merged))
}

func validationFailed(fields []pluginhost.FieldError) map[string]any {
	return map[string]any{"error": "validation failed", "fields": fields}
}

// invalidateSitemaps drops cached sitemaps so plugin settings that feed the
// sitemap.urls filter take effect; best effort (TTL applies otherwise).
func (s *Server) invalidateSitemaps(ctx context.Context) {
	if s.infra == nil || s.infra.Cache == nil {
		return
	}
	if err := s.infra.Cache.DeletePattern(ctx, cache.SitemapPattern()); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("sitemap cache invalidation failed", slog.String("error", err.Error()))
	}
}
