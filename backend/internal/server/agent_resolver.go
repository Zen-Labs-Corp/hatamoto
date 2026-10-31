package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
)

const (
	agentResolveTTL  = 60 * time.Second
	agentResolveMax  = 5000
	agentLookupLimit = 2 * time.Second
)

// agentRedirectFn reports the status code of an active redirect for path.
type agentRedirectFn func(ctx context.Context, path string) (status int, ok bool)

// agentLookupFn finds the published entity serving path. found is false when
// nothing serves it; entityID is nil for home/archive pages.
type agentLookupFn func(ctx context.Context, path string) (entityID *uuid.UUID, found bool, err error)

// buildAgentResolver derives the status a visitor would get for path, in the
// same order as the renderer: redirect first, then entity, else 404.
// It deliberately does not log 404s (that would double count crawler hits as
// 404-log entries). Lookup errors yield status 0 (unknown), never a guess.
func buildAgentResolver(redirect agentRedirectFn, lookup agentLookupFn) agents.ResolveFunc {
	return func(ctx context.Context, path string) (*uuid.UUID, int) {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
			return nil, 0
		}
		if len(path) > 1 {
			path = strings.TrimSuffix(path, "/")
		}
		if redirect != nil {
			if status, ok := redirect(ctx, path); ok {
				return nil, status
			}
		}
		id, found, err := lookup(ctx, path)
		switch {
		case err != nil:
			return nil, 0
		case !found && path == "/":
			// The site root is always served (Next renders or redirects it
			// even when no entity claims "/"), so it is never a 404.
			return nil, http.StatusOK
		case !found:
			return nil, http.StatusNotFound
		}
		return id, http.StatusOK
	}
}

// redirectStatusFn adapts a read-only redirect lookup (redirect.Service.Peek:
// no hit counting) to agentRedirectFn. A rule without a status code is a 301.
func redirectStatusFn(peek func(ctx context.Context, path string) *redirect.Redirect) agentRedirectFn {
	return func(ctx context.Context, path string) (int, bool) {
		rd := peek(ctx, path)
		if rd == nil {
			return 0, false
		}
		if rd.StatusCode == 0 {
			return http.StatusMovedPermanently, true
		}
		return rd.StatusCode, true
	}
}

// newAgentResolver wires buildAgentResolver to the live services and caches
// the result per path.
func (s *Server) newAgentResolver() agents.ResolveFunc {
	var redirect agentRedirectFn
	if s.redirectService != nil {
		redirect = redirectStatusFn(s.redirectService.Peek)
	}

	lookup := func(ctx context.Context, path string) (*uuid.UUID, bool, error) {
		if s.entityService == nil {
			return nil, false, context.Canceled
		}
		ctx, cancel := context.WithTimeout(ctx, agentLookupLimit)
		defer cancel()
		return lookupAgentEntity(ctx, entity.NewPostgresPublicRepository(s.db), s.urlSnapshot(ctx), path)
	}

	return agents.NewCachedResolver(buildAgentResolver(redirect, lookup), agentResolveTTL, agentResolveMax)
}

// lookupAgentEntity finds the entity serving path the way the public loader
// does: the locale segment is stripped and reads are scoped to that locale.
func lookupAgentEntity(ctx context.Context, repo resolveRepo, snap *entity.URLSnapshot, path string) (*uuid.UUID, bool, error) {
	ctx, _, path, prefixed := localeScope(ctx, snap, path)
	if prefixed && path == "/" {
		return nil, true, nil // a locale home
	}
	res, err := resolveEntity(ctx, repo, snap, path)
	if err != nil || res == nil {
		return nil, false, err
	}
	if res.Entity != nil {
		id := res.Entity.ID
		return &id, true, nil
	}
	return nil, true, nil
}
