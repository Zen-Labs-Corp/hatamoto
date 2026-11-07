package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/user"
)

// writeJSON sends a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) { jsonx.WriteJSON(w, status, data) }

// writeError sends an error response.
func writeError(w http.ResponseWriter, status int, message string) {
	jsonx.WriteError(w, status, message)
}

// parseJSON strictly decodes the JSON request body (size limited, duplicate
// names and trailing data rejected).
func parseJSON(r *http.Request, v interface{}) error {
	_, err := jsonx.DecodeRequest(nil, r, v, jsonx.DefaultMaxBody)
	return err
}

// softAuthed reports whether the request carries a valid admin session cookie,
// a valid Bearer session token or a valid X-API-Key. It never writes a
// response and never rejects: callers use it only to decide how much detail to
// show, so a bad credential looks the same as none.
func (s *Server) softAuthed(r *http.Request) bool {
	ctx := r.Context()
	validate := s.validateSession
	if validate == nil && s.authService != nil {
		validate = s.authService.ValidateBearerSession
	}
	if validate != nil {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
			if _, err := validate(ctx, c.Value); err == nil {
				return true
			}
		}
		if parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			if _, err := validate(ctx, parts[1]); err == nil {
				return true
			}
		}
	}
	raw := r.Header.Get("X-API-Key")
	if raw == "" {
		return false
	}
	if s.keyValid != nil {
		return s.keyValid(ctx, raw)
	}
	if s.apiKeyService == nil {
		return false
	}
	key, err := s.apiKeyService.ValidateKey(ctx, raw)
	return err == nil && key != nil && key.CreatedBy != nil && key.CreatorCanAct()
}

// probeText answers a public probe: a bare word, never cached.
func probeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// handleHealth answers liveness. Publicly it is a bare "ok"; the JSON detail
// is for an authenticated caller only, so the probe says nothing about the
// software behind it.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.softAuthed(r) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "healthy",
		})
		return
	}
	probeText(w, http.StatusOK, "ok")
}

// handleReady answers readiness (includes a DB check), with the same public
// versus authenticated split as handleHealth. The status code is the same for
// both; supervisors and health checks read only that.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ping := s.pingDB
	if ping == nil {
		ping = s.db.Ping
	}
	detail := s.softAuthed(r)
	if err := ping(r.Context()); err != nil {
		if detail {
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"status":   "not ready",
				"database": "unavailable",
			})
			return
		}
		probeText(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	if detail {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "ready",
			"database": "connected",
		})
		return
	}
	probeText(w, http.StatusOK, "ok")
}

// handleLogin authenticates a user.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var input auth.LoginRequest
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Capture UA + IP for the server-side session audit fields AND per-account
	// lockout. Use the shared trusted-proxy-aware helper (rightmost-untrusted
	// XFF hop) so login sees the same IP as the rest of the authenticated
	// surface and an attacker can't spoof it via a prepended XFF entry.
	userAgent := r.UserAgent()
	clientIP := middleware.GetClientIP(r)

	result, err := s.authService.Login(r.Context(), &input, userAgent, clientIP)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		if errors.Is(err, auth.ErrFactorSetupRequired) {
			writeError(w, http.StatusForbidden, "this site requires two-step sign-in: set up an authenticator app or a passkey in the admin, then sign in again")
			return
		}
		if errors.Is(err, auth.ErrSecondFactorRequired) {
			// A user with a second factor needs a valid "code"; without one the
			// answer is the same generic 401, never "needs a code".
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		if errors.Is(err, auth.ErrAccountLocked) {
			// Return the SAME generic 401 as a bad password so a locked account is
			// not distinguishable from a wrong password or an unknown email
			// (avoids account enumeration). The lock still blocks auth server-side.
			s.logger.Warn("login attempt on locked account", "ip", clientIP)
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		if errors.Is(err, context.Canceled) {
			s.logger.Debug("sign-in abandoned", "error", err)
			return
		}
		s.logger.Error("login failed", "error", err)
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	// The session token is in the body for bearer clients. With cookie-auth
	// enabled it is also set as an HttpOnly API session cookie (never the admin
	// cookie), and the body carries the CSRF token bound to that cookie.
	if s.cfg.EnableCookieAuth {
		auth.SetAPISessionCookie(w, result.SessionToken, s.cfg.AdminCookieSecure)
		result.CSRFToken = middleware.APICSRFToken(s.csrfKey(), result.SessionToken)
	}

	writeJSON(w, http.StatusOK, result)
}

// handleLogout ends this device's API session and clears its cookie. It revokes
// every session the request presents (bearer and, with cookie-auth on, the API
// cookie), so a stale Authorization header cannot leave the cookie session
// alive. The admin form session cookie is never read or cleared here.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.sessionStore != nil {
		var tokens []string
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			tokens = append(tokens, h[7:])
		}
		if c, err := r.Cookie(auth.APISessionCookieName); err == nil && s.cfg.EnableCookieAuth {
			tokens = append(tokens, c.Value)
		}
		for _, token := range tokens {
			if sess, verr := s.sessionStore.Validate(r.Context(), token); verr == nil {
				_ = s.sessionStore.Revoke(r.Context(), sess.ID)
			}
		}
	}
	if s.cfg.EnableCookieAuth {
		auth.ClearAPISessionCookie(w, s.cfg.AdminCookieSecure)
		// Expire a CSRF cookie left by an older version; harmless otherwise.
		middleware.ClearCSRFCookie(w, s.cfg.AdminCookieSecure)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// handleGetMe returns the current user's profile.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	u, err := s.userRepo.GetByID(r.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.logger.Error("failed to get user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	u.PasswordHash = "" // Never expose

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user": u,
	})
}

// handleUpdateMe updates the current user's profile.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	var input user.UpdateUserInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Users cannot change their own role
	input.Role = nil
	// Nor their own end of access: only an owner sets that, on /users.
	input.AccessEndsAt = user.AccessEnd{}

	u, err := s.userRepo.Update(r.Context(), claims.UserID, &input)
	if err != nil {
		s.logger.Error("failed to update user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, u)
}

// handleListUsers lists all admin users. Pre-Path-B this filtered by the
// caller's tenant; the singleton-tenant strip makes that filter a no-op,
// so we list everyone the admin is allowed to see.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	_, _ = middleware.GetClaims(r.Context())

	users, err := s.userRepo.List(r.Context(), 100, 0)
	if err != nil {
		s.logger.Error("failed to list users", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": users,
		"total": len(users),
	})
}

// handleCreateUser creates a new user in the current tenant.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		// AccessEndsAt is an RFC 3339 instant with a zone; owners only.
		AccessEndsAt *time.Time `json:"access_ends_at"`
	}
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.AccessEndsAt != nil {
		if claims.Role != string(user.RoleOwner) {
			writeError(w, http.StatusForbidden, "only owners can set an access end date")
			return
		}
		if user.ValidateAccessEnd(*input.AccessEndsAt, time.Now()) != nil {
			writeError(w, http.StatusBadRequest, "access_ends_at must be in the future")
			return
		}
	}

	role, err := user.ParseRole(input.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}

	// Non-owners cannot create owners
	if role == user.RoleOwner && claims.Role != string(user.RoleOwner) {
		writeError(w, http.StatusForbidden, "only owners can create owner accounts")
		return
	}

	// Someone whose own access ends cannot create a user who outlasts them.
	own, ok := s.callerAccessEnd(w, r, claims)
	if !ok {
		return
	}
	if !user.MayGrant(own, input.AccessEndsAt) {
		writeError(w, http.StatusForbidden, user.ErrAccessEndNotAllowed.Error())
		return
	}

	passwordHash, err := auth.HashPasswordContext(r.Context(), input.Password)
	if err != nil {
		s.logger.Error("failed to hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	u, err := s.userRepo.Create(r.Context(), &user.CreateUserInput{
		Email:    input.Email,
		Password: input.Password,
		Role:     role,
		Name:     input.Name,

		AccessEndsAt: input.AccessEndsAt,
	}, passwordHash)
	if err != nil {
		if errors.Is(err, user.ErrEmailAlreadyExists) {
			writeError(w, http.StatusConflict, "email already exists")
			return
		}
		if errors.Is(err, user.ErrLastOwner) {
			writeError(w, http.StatusConflict, "another owner without an end date is needed first")
			return
		}
		s.logger.Error("failed to create user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	if input.AccessEndsAt != nil {
		s.auditAccessChange(r, claims, u.Email, nil, input.AccessEndsAt, "created")
	}

	writeJSON(w, http.StatusCreated, u)
}

// handleGetUser retrieves a specific user.
func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	u, err := s.userRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.logger.Error("failed to get user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	u.PasswordHash = ""
	writeJSON(w, http.StatusOK, u)
}

// handleUpdateUser updates a specific user.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var input user.UpdateUserInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Only owners can change roles
	if input.Role != nil && claims.Role != string(user.RoleOwner) {
		writeError(w, http.StatusForbidden, "only owners can change user roles")
		return
	}

	// Someone whose own access ends cannot change an end date, nor make a user
	// without a date, or with a later one, an owner. UpdateUserInput.Role has
	// no json tag, so it decodes under its Go name ("Role"; the decoder is case
	// sensitive, so "role" is ignored): role changes over JSON are possible.
	promote := input.Role != nil && *input.Role == user.RoleOwner
	if input.AccessEndsAt.Set || promote {
		own, ok := s.callerAccessEnd(w, r, claims)
		if !ok {
			return
		}
		if input.AccessEndsAt.Set && !user.MayChangeAccessEnd(own) {
			writeError(w, http.StatusForbidden, user.ErrAccessEndNotAllowed.Error())
			return
		}
		if promote && own != nil {
			target, err := s.userRepo.GetByID(r.Context(), id)
			if err != nil {
				if errors.Is(err, user.ErrUserNotFound) {
					writeError(w, http.StatusNotFound, "user not found")
					return
				}
				s.logger.Error("failed to get user", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to update user")
				return
			}
			if target.Role != user.RoleOwner && !user.MayGrant(own, target.AccessEndsAt) {
				writeError(w, http.StatusForbidden, user.ErrAccessEndNotAllowed.Error())
				return
			}
		}
	}

	// The end date is owner only, and a changed value must be in the future.
	var before *user.User
	if input.AccessEndsAt.Set {
		if claims.Role != string(user.RoleOwner) {
			writeError(w, http.StatusForbidden, "only owners can set an access end date")
			return
		}
		if t := input.AccessEndsAt.At; t != nil && user.ValidateAccessEnd(*t, time.Now()) != nil {
			writeError(w, http.StatusBadRequest, "access_ends_at must be in the future")
			return
		}
		if before, err = s.userRepo.GetByID(r.Context(), id); err != nil {
			if errors.Is(err, user.ErrUserNotFound) {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			s.logger.Error("failed to get user", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to update user")
			return
		}
	}

	u, err := s.userRepo.Update(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, user.ErrLastOwner) {
			msg := "cannot remove or demote the last owner"
			if input.AccessEndsAt.Set && input.AccessEndsAt.At != nil {
				msg = "another owner without an end date is needed first"
			}
			writeError(w, http.StatusConflict, msg)
			return
		}
		s.logger.Error("failed to update user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	if before != nil && !sameAccessEnd(before.AccessEndsAt, u.AccessEndsAt) {
		s.auditAccessChange(r, claims, u.Email, before.AccessEndsAt, u.AccessEndsAt, "")
	}

	writeJSON(w, http.StatusOK, u)
}

// callerAccessEnd is the caller's own access end (nil: none). It writes the
// error response and returns false when the caller cannot be read.
func (s *Server) callerAccessEnd(w http.ResponseWriter, r *http.Request, claims *auth.Claims) (*time.Time, bool) {
	me, err := s.userRepo.GetByID(r.Context(), claims.UserID)
	if err != nil {
		s.logger.Error("failed to get user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to look up the caller")
		return nil, false
	}
	return me.AccessEndsAt, true
}

func sameAccessEnd(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// auditAccessChange records a set, change or clear of a user's access end, in
// the same shape as the admin screens. how is "created" for a new user.
func (s *Server) auditAccessChange(r *http.Request, claims *auth.Claims, target string, from, to *time.Time, how string) {
	text := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	detail := map[string]any{"from": text(from), "to": text(to)}
	if how != "" {
		detail[how] = true
	}
	s.adminAudit(r.Context(), admin.AuditEvent{
		ActorID: claims.UserID.String(), Actor: claims.Email,
		Action: "user.access_changed", Target: target, Detail: detail,
	})
}

// handleDeleteUser deletes a specific user.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	// Cannot delete yourself
	if id == claims.UserID {
		writeError(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}

	// Only owners can delete users
	if claims.Role != string(user.RoleOwner) {
		writeError(w, http.StatusForbidden, "only owners can delete users")
		return
	}

	if err := s.userRepo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.logger.Error("failed to delete user", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}
