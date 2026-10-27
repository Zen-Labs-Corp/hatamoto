package middleware

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
)

// APIKeyValidator is the slice of auth.APIKeyService that AuthOrAPIKey needs.
type APIKeyValidator interface {
	ValidateKey(ctx context.Context, plainKey string) (*auth.APIKey, error)
	RecordUsage(ctx context.Context, keyID uuid.UUID, ipAddress string) error
}

// APIKeyAllower is the slice of APIKeyRateLimiter that AuthOrAPIKey needs.
type APIKeyAllower interface {
	Allow(keyID uuid.UUID, rateLimit int) bool
}

type keyRoute struct {
	method string
	re     *regexp.Regexp
	scope  auth.APIKeyScope
}

func keyRoutePattern(p string) *regexp.Regexp { return regexp.MustCompile(p) }

var (
	entityPath = keyRoutePattern(`^/api/v1/entities/[^/]+/?$`)

	// keyRoutes is the complete, fail-closed list of routes an API key may
	// reach through AuthOrAPIKey. Anything not listed is 403, whatever the
	// key's scopes (including "*"). Patterns are anchored; ids are one
	// segment.
	keyRoutes = []keyRoute{
		{http.MethodGet, keyRoutePattern(`^/api/v1/entities/?$`), auth.ScopeReadEntities},
		{http.MethodGet, entityPath, auth.ScopeReadEntities},
		{http.MethodGet, keyRoutePattern(`^/api/v1/entities/[^/]+/versions(/[^/]+)?/?$`), auth.ScopeReadEntities},
		{http.MethodGet, keyRoutePattern(`^/api/v1/render/theme/?$`), auth.ScopeReadEntities},
		{http.MethodPost, keyRoutePattern(`^/api/v1/render/preview/?$`), auth.ScopeReadEntities},
		{http.MethodPost, keyRoutePattern(`^/api/v1/entities/?$`), auth.ScopeWriteEntities},
		{http.MethodPatch, keyRoutePattern(`^/api/v1/entities/[^/]+/status/?$`), auth.ScopeWriteEntities},
		{http.MethodPut, entityPath, auth.ScopeWriteEntities},
		{http.MethodPatch, entityPath, auth.ScopeWriteEntities},
		{http.MethodPost, keyRoutePattern(`^/api/v1/entities/[^/]+/restore/[^/]+/?$`), auth.ScopeWriteEntities},
		// Internal-linking v2 for automated publishers: suggest is a
		// non-mutating scan of the rules catalog against one entity's
		// content (read); insert wraps one anchor in a source entity's
		// stored content, the same power as PATCH /entities/{id} (write).
		{http.MethodPost, keyRoutePattern(`^/api/v1/linking/suggest/[^/]+/?$`), auth.ScopeReadEntities},
		{http.MethodPost, keyRoutePattern(`^/api/v1/linking/insert/?$`), auth.ScopeWriteEntities},
	}
)

// matchKeyRoute returns the scope required for method+path, or false when the
// route is not on the allowlist. Paths that are not in canonical form
// (dot segments, encoded slashes or backslashes, empty segments) never match.
func matchKeyRoute(r *http.Request) (auth.APIKeyScope, bool) {
	p := r.URL.Path
	if strings.Contains(p, "..") || strings.Contains(p, "//") || strings.Contains(p, `\`) {
		return "", false
	}
	// RawPath is only set when the escaped form differs from the default
	// encoding of Path (e.g. %2F). ServeMux routes on the escaped form, so never trust such paths.
	if r.URL.RawPath != "" && r.URL.RawPath != p {
		return "", false
	}
	for _, kr := range keyRoutes {
		if kr.method == r.Method && kr.re.MatchString(p) {
			return kr.scope, true
		}
	}
	return "", false
}

// AuthOrAPIKey authenticates a request that carries an X-API-Key header as an
// API key, restricted to keyRoutes; requests without the header go to next
// (the regular session auth) unchanged. A request with both a header and a
// session cookie is key-authenticated.
//
// Key requests run with synthesised editor claims for the key's creator (never
// superadmin) so existing handlers work unmodified, plus the API key and
// AuthTypeAPIKey in the context (so CSRF can be skipped and saves attributed).
func AuthOrAPIKey(v APIKeyValidator, limiter APIKeyAllower, next func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		session := next(h)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace(r, "auth")
			raw := r.Header.Get("X-API-Key")
			if raw == "" {
				session.ServeHTTP(w, r)
				return
			}

			key, err := v.ValidateKey(r.Context(), raw)
			// The creator must still be an owner/editor (key power never outlives it).
			if err != nil || key == nil || key.CreatedBy == nil || !key.CreatorCanAct() {
				http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
				return
			}

			if limiter != nil && !limiter.Allow(key.ID, key.RateLimit) {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(key.RateLimit))
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			scope, ok := matchKeyRoute(r)
			if !ok || !key.HasScope(scope) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}

			ip := getIP(r)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = v.RecordUsage(ctx, key.ID, ip)
			}()

			claims := &auth.Claims{UserID: *key.CreatedBy, Role: "editor"}
			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			ctx = context.WithValue(ctx, apiKeyContextKey, key)
			ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)
			ctx = database.WithUser(ctx, claims.UserID)
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
