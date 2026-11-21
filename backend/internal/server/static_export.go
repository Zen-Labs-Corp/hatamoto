package server

import (
	"context"

	"hatamoto.dev/core/internal/staticexport"
)

// StaticExport renders the public site through this server's own router and
// publishes it to t. Callers in the serving process must use the live *Server
// (server.New sets package globals; never build a second one).
func (s *Server) StaticExport(ctx context.Context, t staticexport.Target, o staticexport.Options) (staticexport.Result, error) {
	return staticexport.Export(ctx, &staticSource{s: s}, staticexport.NewRunStore(s.db.Pool()), t, o)
}
