package server

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// pluginView is one item of GET /api/v1/plugins: the load status plus
// manifest metadata the admin UI needs.
type pluginView struct {
	Name           string          `json:"name"`
	Loaded         bool            `json:"loaded"`
	Error          string          `json:"error,omitempty"`
	Hooks          []string        `json:"hooks"`
	Version        string          `json:"version,omitempty"`
	SDK            string          `json:"sdk,omitempty"`
	Description    string          `json:"description,omitempty"`
	AllowedHosts   []string        `json:"allowed_hosts,omitempty"`
	SettingsSchema json.RawMessage `json:"settings_schema,omitempty"`
	// Capabilities: what the manifest requests, what the owner granted, and
	// what the plugin exposes.
	Permissions []string      `json:"permissions"`
	Granted     []string      `json:"granted"`
	Routes      []sdk.Route   `json:"routes,omitempty"`
	Cron        []sdk.CronJob `json:"cron,omitempty"`
	Cookies     []string      `json:"cookies,omitempty"`
	Funcs       []string      `json:"funcs,omitempty"`
	DataError   string        `json:"data_error,omitempty"`
}

// pluginStatuses returns the load results of the plugin runtime (none before
// plugins are set up).
func (s *Server) pluginStatuses() []pluginhost.Status {
	switch {
	case s.pluginStatusFn != nil:
		return s.pluginStatusFn()
	case s.plugins != nil:
		return s.plugins.Statuses()
	}
	return nil
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Server) pluginViews() []pluginView {
	views := []pluginView{}
	for _, st := range s.pluginStatuses() {
		m := st.Manifest
		v := pluginView{
			Name: st.Name, Loaded: st.Loaded, Error: st.Error, Hooks: st.Hooks,
			Version: m.Version, SDK: m.SDK, Description: m.Description, AllowedHosts: m.AllowedHosts,
			Permissions: orEmpty(m.Permissions), Granted: []string{}, Routes: m.Routes, Cron: m.Cron,
			Cookies: m.Cookies, Funcs: m.Funcs, DataError: st.DataError,
		}
		if s.plugins != nil {
			v.Granted = orEmpty(s.plugins.Grants().Granted(st.Name))
		}
		if v.Hooks == nil {
			v.Hooks = []string{}
		}
		// Invalid JSON would make the whole list response fail to encode.
		if json.Valid(m.SettingsSchema) {
			v.SettingsSchema = m.SettingsSchema
		}
		views = append(views, v)
	}
	return views
}

// handleListPlugins returns every plugin found in the plugin directory with
// its load status and manifest metadata. Mounted at GET /api/v1/plugins
// (owner role enforced in server_core.go).
//
// Response shape:
//
//	{"public_objects"?: [...], "public_objects_allowed"?: bool,
//	 "plugins": [{name, loaded, error?, hooks[], version, sdk, description,
//	  allowed_hosts?, settings_schema?, permissions[], granted[], routes?,
//	  cron?, cookies?, funcs?, data_error?}]}
//
// Order: load order (plugin directory names, sorted).
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"plugins": s.pluginViews()}
	if s.pluginDB != nil {
		// Objects PUBLIC can use that the application role cannot close: while
		// any exist, db.* is refused (unless the operator accepted the risk).
		if objs, err := s.pluginDB.PublicObjects(r.Context()); err == nil && len(objs) > 0 {
			out["public_objects"] = objs
			out["public_objects_allowed"] = s.pluginDB.AllowPublicObjects
		}
	}
	writeJSON(w, http.StatusOK, out)
}
