// Package middleware provides HTTP middleware for the API server.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
)

type contextKey string

const (
	claimsContextKey   contextKey = "claims"
	apiKeyContextKey   contextKey = "api_key"
	authTypeContextKey contextKey = "auth_type"
)

// AuthType represents the type of authentication used.
type AuthType string

const (
	AuthTypeAPIKey AuthType = "api_key"
)

// SessionValidator resolves an opaque session token to the principal it
// belongs to (server-side session lookup).
type SessionValidator func(ctx context.Context, token string) (*auth.Claims, error)

type apiCookieKey struct{}

// GetAPICookieToken returns the API session cookie token the request was
// authenticated with. It is absent for bearer and API-key requests, which is
// what lets CSRF protect cookie-authenticated requests only.
func GetAPICookieToken(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(apiCookieKey{}).(string)
	return v, ok && v != ""
}

// AuthMiddleware authenticates a request by its server-side session: the opaque
// token in Authorization: Bearer (checked by validateBearer), else (when
// cookieAuth) the HttpOnly API session cookie (checked by validateCookie, which
// must accept API-kind sessions only). The admin form session cookie is never
// read here. Anything else is a 401.
func AuthMiddleware(validateBearer, validateCookie SessionValidator, cookieAuth bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			var bearer string
			if authHeader != "" {
				parts := strings.SplitN(authHeader, " ", 2)
				if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
					http.Error(w, `{"error":"invalid authorization header format"}`, http.StatusUnauthorized)
					return
				}
				bearer = parts[1]
			}

			var claims *auth.Claims
			cookieToken := ""
			err := auth.ErrInvalidToken
			if bearer != "" {
				if sc, se := validateBearer(r.Context(), bearer); se == nil {
					claims, err = sc, nil
				}
			}
			// Cookie fallback, gated on cookieAuth (ENABLE_COOKIE_AUTH): with it
			// OFF, cookies are ignored, so bearer-only clients keep their
			// CSRF-exempt posture.
			if cookieAuth && err != nil {
				if c, cerr := r.Cookie(auth.APISessionCookieName); cerr == nil && c.Value != "" {
					if sc, se := validateCookie(r.Context(), c.Value); se == nil {
						claims, err = sc, nil
						cookieToken = c.Value
					}
				}
			}
			if err != nil {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claims)
			if cookieToken != "" {
				ctx = context.WithValue(ctx, apiCookieKey{}, cookieToken)
			}
			ctx = database.WithUser(ctx, claims.UserID)
			if claims.IsSuperadmin {
				ctx = database.WithSuperadmin(ctx)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves the authenticated principal from the request context.
func GetClaims(ctx context.Context) (*auth.Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey).(*auth.Claims)
	return claims, ok
}

// ContextWithClaims returns a copy of ctx carrying the given claims. Exposed so
// tests (and any in-process caller) can populate the same context slot the auth
// middleware uses, without importing the unexported context key.
func ContextWithClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey, claims)
}

// RequireRole middleware ensures the user has one of the specified roles.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace(r, fmt.Sprint("role", roles))
			claims, ok := GetClaims(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if slices.Contains(roles, claims.Role) {
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		})
	}
}

// GetUserID extracts user ID from context (shorthand).
func GetUserID(ctx context.Context) (uuid.UUID, bool) {
	claims, ok := GetClaims(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return claims.UserID, true
}

// GetTenantID is retained for callers that historically asked "is the
// current request authenticated as a tenant principal?". Post-Path-B
// the tenant dimension is gone; the second return value is the only
// useful signal. Returns (uuid.Nil, true) when the request carries an
// admin claim or API key, (uuid.Nil, false) otherwise.
func GetTenantID(ctx context.Context) (uuid.UUID, bool) {
	if _, ok := GetClaims(ctx); ok {
		return uuid.Nil, true
	}
	if _, ok := GetAPIKey(ctx); ok {
		return uuid.Nil, true
	}
	return uuid.Nil, false
}

// GetAPIKey retrieves the API key from the request context.
func GetAPIKey(ctx context.Context) (*auth.APIKey, bool) {
	apiKey, ok := ctx.Value(apiKeyContextKey).(*auth.APIKey)
	return apiKey, ok
}

// GetAuthType returns the authentication type used for the request.
func GetAuthType(ctx context.Context) AuthType {
	authType, ok := ctx.Value(authTypeContextKey).(AuthType)
	if !ok {
		return ""
	}
	return authType
}

// APIKeyRateLimiter implements per-key rate limiting for API keys.
type APIKeyRateLimiter struct {
	limiters map[uuid.UUID]*keyLimiter
	mu       sync.RWMutex
	cleanup  time.Duration
	stopCh   chan struct{}
}

type keyLimiter struct {
	limiter    *rate.Limiter
	lastSeen   time.Time
	hourlyReqs int64
	hourStart  time.Time
}

// NewAPIKeyRateLimiter creates a new API key rate limiter.
func NewAPIKeyRateLimiter() *APIKeyRateLimiter {
	rl := &APIKeyRateLimiter{
		limiters: make(map[uuid.UUID]*keyLimiter),
		cleanup:  5 * time.Minute,
		stopCh:   make(chan struct{}),
	}

	go rl.cleanupRoutine()
	return rl
}

// Allow checks if a request is allowed for the given API key.
// Returns true if allowed, false if rate limited.
func (rl *APIKeyRateLimiter) Allow(keyID uuid.UUID, rateLimit int) bool {
	rateLimit = auth.NormalizeRateLimit(rateLimit)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	kl, exists := rl.limiters[keyID]
	if !exists {
		// Create new limiter: convert hourly limit to per-second rate
		// With burst allowing 10% of hourly in a burst
		rps := float64(rateLimit) / 3600.0
		burst := max(rateLimit/10, 10)

		kl = &keyLimiter{
			limiter:    rate.NewLimiter(rate.Limit(rps), burst),
			lastSeen:   now,
			hourlyReqs: 0,
			hourStart:  now,
		}
		rl.limiters[keyID] = kl
	}

	kl.lastSeen = now

	// Reset hourly counter if hour has passed
	if now.Sub(kl.hourStart) >= time.Hour {
		kl.hourlyReqs = 0
		kl.hourStart = now
	}

	// Check hourly limit
	if kl.hourlyReqs >= int64(rateLimit) {
		return false
	}

	// Check token bucket
	if !kl.limiter.Allow() {
		return false
	}

	kl.hourlyReqs++
	return true
}

// cleanupRoutine removes stale limiters. It exits when stopCh is closed.
func (rl *APIKeyRateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for id, kl := range rl.limiters {
				if now.Sub(kl.lastSeen) > rl.cleanup {
					delete(rl.limiters, id)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}

// Stop terminates the background cleanup goroutine.
func (rl *APIKeyRateLimiter) Stop() {
	close(rl.stopCh)
}

// APIKeyAuthMiddleware validates API keys from the X-API-Key header.
// It sets tenant context similar to session auth, enabling RLS.
func APIKeyAuthMiddleware(apiKeyService *auth.APIKeyService, rateLimiter *APIKeyRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKeyHeader := r.Header.Get("X-API-Key")
			if apiKeyHeader == "" {
				http.Error(w, `{"error":"missing X-API-Key header"}`, http.StatusUnauthorized)
				return
			}

			// Validate the API key (this queries without tenant context)
			apiKey, err := apiKeyService.ValidateKey(r.Context(), apiKeyHeader)
			if err != nil {
				switch err {
				case auth.ErrAPIKeyNotFound:
					http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
				case auth.ErrAPIKeyExpired:
					http.Error(w, `{"error":"api key has expired"}`, http.StatusUnauthorized)
				case auth.ErrAPIKeyRevoked:
					http.Error(w, `{"error":"api key has been revoked"}`, http.StatusUnauthorized)
				default:
					http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
				}
				return
			}

			// Check rate limit
			if rateLimiter != nil && !rateLimiter.Allow(apiKey.ID, apiKey.RateLimit) {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(apiKey.RateLimit))
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			// Record usage (async to not slow down request)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ip := getIP(r)
				_ = apiKeyService.RecordUsage(ctx, apiKey.ID, ip)
			}()

			// Set context with API key and tenant
			ctx := context.WithValue(r.Context(), apiKeyContextKey, apiKey)
			ctx = context.WithValue(ctx, authTypeContextKey, AuthTypeAPIKey)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAPIKeyScope middleware ensures the API key has the required scope.
func RequireAPIKeyScope(scope auth.APIKeyScope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check API key scope
			apiKey, ok := GetAPIKey(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !apiKey.HasScope(scope) {
				http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
