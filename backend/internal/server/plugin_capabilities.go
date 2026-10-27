package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// Mount points of plugin routes. The patterns are fixed: a plugin's own paths
// are only ever looked up below them, so they cannot shadow a core route.
const (
	pluginAdminMount  = "/p/{plugin}/{rest...}" // under /api/v1, behind auth and CSRF
	pluginPublicMount = "/p/{plugin}/{rest...}" // at the root, anonymous and rate limited
)

// mountPluginAdminRoutes registers the fixed catch-all for plugin routes in
// the authenticated /api/v1 group.
func (s *Server) mountPluginAdminRoutes(r *httpx.Group) {
	for _, m := range catchAllMethods {
		r.HandleFunc(m+" "+pluginAdminMount, s.handlePluginAdminRoute)
	}
}

// mountPluginPublicRoutes registers the fixed catch-all for public plugin
// routes: anonymous, so limited per client address.
func (s *Server) mountPluginPublicRoutes(r *httpx.Group) {
	limiter := middleware.NewRateLimiter(10, 30)
	g := r.Group("", limiter.Middleware)
	for _, m := range catchAllMethods {
		g.HandleFunc(m+" "+pluginPublicMount, s.handlePluginPublicRoute)
	}
}

func (s *Server) handlePluginAdminRoute(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		notFoundHandler(w, r)
		return
	}
	var user *sdk.HTTPUser
	if c, ok := middleware.GetClaims(r.Context()); ok {
		user = &sdk.HTTPUser{ID: c.UserID.String(), Role: c.Role}
	}
	s.plugins.AdminRoutes().Serve(w, r, r.PathValue("plugin"), r.PathValue("rest"), user)
}

func (s *Server) handlePluginPublicRoute(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		notFoundHandler(w, r)
		return
	}
	plugin, rest := r.PathValue("plugin"), r.PathValue("rest")
	// assets/ is the plugin package's public file slot (plugin.css, plugin.js,
	// images); the plugin's own routes cannot shadow it.
	if file, ok := strings.CutPrefix(rest, "assets/"); ok && s.packages != nil {
		if pkg := s.packages.PluginPackage(plugin); pkg != nil {
			packages.ServeFile(w, r, pkg, "assets/"+file, "public, max-age=300", "")
			return
		}
	}
	s.plugins.PublicRoutes().Serve(w, r, plugin, rest, nil)
}

// handleGrantPlugin sets the capabilities the owner allows a plugin:
// POST /api/v1/plugins/{name}/grant {"permissions": ["kv", ...]}. The list
// replaces the earlier grant; it must be a subset of what the plugin's
// manifest requests.
func (s *Server) handleGrantPlugin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := parseJSON(r, &body); err != nil || body.Permissions == nil {
		writeError(w, http.StatusBadRequest, `body must be {"permissions": [...]}`)
		return
	}
	if s.plugins == nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	before := s.plugins.Grants().Granted(name)
	_, err := s.plugins.Grant(r.Context(), name, body.Permissions)
	if err == nil && s.packages != nil {
		s.packages.Audit(packages.WithActor(r.Context(), packages.Actor{Email: "api"}), "package.grants", "plugin:"+name, map[string]any{"from": before, "to": body.Permissions})
	}
	if err == nil {
		// As the admin form does: pages cached under the old grants (a script
		// link a revoke must remove) are dropped.
		if s.packages != nil {
			s.packages.Reconcile(r.Context())
		}
		s.invalidateRendered(r.Context())
	}
	if err != nil {
		switch {
		case errors.Is(err, pluginhost.ErrCommunityScripts):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, pluginhost.ErrNotRequested), errors.Is(err, pluginhost.ErrUnknownPermission):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, pluginhost.ErrPluginNotFound):
			writeError(w, http.StatusNotFound, "plugin not found")
		default:
			s.logger.Error("grant plugin permissions", "plugin", name, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save grants")
		}
		return
	}
	for _, v := range s.pluginViews() {
		if v.Name == name {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "plugin not found")
}

// fireFormSubmitted announces a stored submission to plugins granted
// forms.read. Visitor metadata (address, user agent) is not part of the event.
func (s *Server) fireFormSubmitted(ctx context.Context, f *form.Form, sub *form.FormSubmission, data map[string]any, meta submitMeta) {
	if s.hooks == nil {
		return
	}
	s.hooks.Fire(ctx, sdk.FormSubmitted.Name, sdk.FormSubmittedEvent{
		FormID: f.ID.String(), FormSlug: f.Slug, SubmissionID: sub.ID.String(),
		Fields: data, SourcePath: meta.PageURL, SubmittedAt: sub.CreatedAt,
	})
}
