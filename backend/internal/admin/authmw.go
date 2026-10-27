package admin

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// idleTimeout signs a session out after a week without use; the absolute
// lifetime (30 d) is enforced by the session store.
const idleTimeout = session.IdleTimeout

// CurrentUser is the signed-in user as templates see them.
type CurrentUser struct {
	ID    string
	Name  string
	Email string
	Role  string
}

func userFrom(ctx context.Context) *CurrentUser {
	u, _ := ctx.Value(userKey).(*CurrentUser)
	return u
}

// UserFromContext is the signed-in user of an admin request (false outside one).
func UserFromContext(ctx context.Context) (CurrentUser, bool) {
	u := userFrom(ctx)
	if u == nil {
		return CurrentUser{}, false
	}
	return *u, true
}

func sessionFrom(ctx context.Context) *session.Session {
	s, _ := ctx.Value(sessionKey).(*session.Session)
	return s
}

// RequireSession authenticates the hatamoto_session cookie (independent of
// ENABLE_COOKIE_AUTH, which only gates the JSON API) and puts the user, their
// claims and the DB context on the request. Failures redirect to the login
// page (htmx: 401 + HX-Redirect so the whole page navigates). It never rotates
// the session: parallel htmx requests would race the rotation.
func (a *Admin) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, sess, ok := a.cachedAuth(r)
		if !ok {
			a.toLogin(w, r)
			return
		}
		ctx := r.Context()
		claims := &auth.Claims{UserID: u.ID, Email: u.Email, Role: string(u.Role), IsSuperadmin: u.Role == user.RoleOwner}
		ctx = middleware.ContextWithClaims(ctx, claims)
		ctx = database.WithUser(ctx, u.ID)
		if claims.IsSuperadmin {
			ctx = database.WithSuperadmin(ctx)
		}
		name := u.Email
		if u.Name != nil && *u.Name != "" {
			name = *u.Name
		}
		ctx = context.WithValue(ctx, userKey, &CurrentUser{ID: u.ID.String(), Name: name, Email: u.Email, Role: string(u.Role)})
		ctx = context.WithValue(ctx, sessionKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Admin) authenticate(r *http.Request) (*user.User, *session.Session, bool) {
	if a.deps.Sessions == nil || a.deps.Users == nil {
		return nil, nil, false
	}
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return nil, nil, false
	}
	sess, err := a.deps.Sessions.Validate(r.Context(), c.Value)
	if err != nil || sess.Kind != session.KindAdmin || sess.UserID == nil {
		return nil, nil, false
	}
	if sess.LastUsedAt != nil && time.Since(*sess.LastUsedAt) > idleTimeout {
		// Revoke, not just refuse: the store touches last_used_at on every
		// Validate, so a merely rejected session would be fresh next request.
		_ = a.deps.Sessions.Revoke(r.Context(), sess.ID)
		return nil, nil, false
	}
	u, err := a.deps.Users.GetByID(database.WithSuperadmin(r.Context()), *sess.UserID)
	if err != nil {
		return nil, nil, false
	}
	return u, sess, true
}

// toLogin answers an unauthenticated request. Only GET navigations carry a
// next= target; a POST that lost its session just lands on the login page.
func (a *Admin) toLogin(w http.ResponseWriter, r *http.Request) {
	q := url.Values{}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if n := SafeNextFor(a.adminPath, r.URL.RequestURI()); n != "" {
			q.Set("next", n)
		}
	}
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		q.Set("expired", "1")
	}
	target := a.url("login")
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// RequireRole allows only the listed roles; others get the 403 page.
func (a *Admin) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := userFrom(r.Context())
			if u == nil || !contains(roles, u.Role) {
				if a.fetchError(w, r, http.StatusForbidden, "Your role does not have access to this page.") {
					return
				}
				a.Error(w, r, http.StatusForbidden, "Not allowed", "Your role does not have access to this page.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// landing is where sign-in and setup end under this instance's prefix. It is a Go-owned screen.
func (a *Admin) landing() string { return a.url("entities") }
