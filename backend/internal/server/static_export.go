package server

import (
	"context"

	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
)

// StaticExport renders the public site through this server's own router and
// publishes it to t. Callers in the serving process must use the live *Server
// (server.New sets package globals; never build a second one).
func (s *Server) StaticExport(ctx context.Context, t staticexport.Target, o staticexport.Options) (staticexport.Result, error) {
	// The CLI never calls Start, so the hosting snapshot is read here.
	s.refreshStaticHosting(ctx)
	return staticexport.Export(ctx, &staticSource{s: s}, staticexport.NewRunStore(s.db.Pool()), t, o)
}

// StaticTestExport is StaticExport with the hosting snapshot hs applied to this
// export only: it does not refresh or change s.hosting, so visitors keep the
// saved mode.
func (s *Server) StaticTestExport(ctx context.Context, t staticexport.Target, hs statichost.StaticHosting, o staticexport.Options) (staticexport.Result, error) {
	return staticexport.Export(statichost.WithHosting(ctx, hs), &staticSource{s: s}, staticexport.NewRunStore(s.db.Pool()), t, o)
}
