package server

import (
	"context"
	"net/http"

	"hatamoto.dev/core/internal/importjob"
	"hatamoto.dev/core/internal/wpimport"
)

// newImportManager builds the WordPress import of the admin wizard. It writes
// through its own services (the same ones `hatamoto import wordpress` uses), so
// after a run it clears every cache the server keeps over what it wrote.
// The private-host override is not wired: the wizard cannot reach it.
//
// The key sealing stored credentials is derived from the server secret; it must
// stay stable, or every stored credential becomes unreadable (the wizard then
// asks for it again).
func (s *Server) newImportManager() *importjob.Manager {
	deps := wpimport.NewDeps(s.db, wpimport.DepsConfig{
		MediaDir: mediaDirOr(s.cfg.Server.MediaUploadDir), MediaBaseURL: s.cfg.Server.MediaBaseURL, MediaMaxMB: s.cfg.Server.MediaMaxSizeMB,
	}, s.logger)
	return importjob.New(importjob.Config{
		Pool: s.db.Pool(), Deps: deps, Key: importjob.DeriveKey(s.cfg.Secret), Logger: s.logger,
		Handler: func() http.Handler { return s.router },
		Purge: func(ctx context.Context) {
			s.entityService.URLRegistry().Invalidate()
			if s.redirectService != nil {
				s.redirectService.InvalidateCache()
			}
			s.invalidateRendered(ctx)
		},
	})
}

func mediaDirOr(dir string) string {
	if dir == "" {
		return "./uploads"
	}
	return dir
}
