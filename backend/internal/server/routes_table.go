package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/taxonomy"
)

// RouteTable returns every route the core registers (method, path, handler)
// for `hatamoto routes` and the route-table test. It builds the routes on a
// Server with just enough non-nil collaborators and no database, so plugin
// routes (which live on per-plugin muxes behind fixed catch-alls) are not
// listed: `hatamoto routes` prints that note.
func RouteTable() (table []httpx.Route, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("route table: %v", r)
		}
	}()
	s, err := routeShell()
	if err != nil {
		return nil, err
	}
	s.setupRoutes()
	return s.routes.Routes(), nil
}

// routeShell is a Server that can run setupRoutes without a database. Tests add
// wiredPlugins before routing.
func routeShell() (*Server, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	s := &Server{
		mux:                http.NewServeMux(),
		cfg:                cfg,
		logger:             logger,
		authService:        &auth.Service{},
		agentLogHandlers:   &agents.LogHandlers{},
		agentAdminHandlers: &agents.AdminHandlers{},
		analyticsHandlers:  &analytics.Handlers{},
		taxonomyHandlers:   &taxonomy.Handlers{},
		//lint:ignore SA1019 the rollback mirror API stays mounted until the author package is removed
		authorHandlers:    &author.Handlers{},
		apiKeyRateLimiter: &middleware.APIKeyRateLimiter{},
		infra:             NewInfrastructure(logger),
	}
	engine, err := render.New(render.Options{})
	if err != nil {
		return nil, err
	}
	s.renderEngine = engine
	return s, nil
}
