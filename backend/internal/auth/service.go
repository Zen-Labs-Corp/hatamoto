package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// adminSessionTTL is the lifetime of server-side admin sessions. Per the
// Path B wizard (2026-05-28): 30 days, forces quarterly re-auth, friendly
// to a future MFA upgrade.
const adminSessionTTL = 30 * 24 * time.Hour

// Login-lockout policy: after maxFailedLogins consecutive failures an account
// is locked for lockoutDuration. Tuned for an admin CMS (few legit users);
// the per-IP AuthRateLimiter is the complementary first line of defense.
const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute
)

// Common authentication errors.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserDisabled       = errors.New("user account is disabled")
	// ErrAccountLocked is returned when an account is temporarily locked due to
	// too many failed login attempts.
	ErrAccountLocked = errors.New("account temporarily locked due to failed login attempts")
)

// TenantInfo represents a tenant membership for the current user.
type TenantInfo struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
	Role string    `json:"role"`
}

// TenantLookup provides tenant name/slug lookup without importing the tenant package.
type TenantLookup interface {
	GetTenantNameSlug(ctx context.Context) (name, slug string, err error)
}

// Service handles authentication operations.
type Service struct {
	userRepo     user.Repository
	tenantLookup TenantLookup
	sessions     *session.Store // login and ValidateSession need it
	// noLockoutEmail is an account that is never locked (lower case). The public
	// demo sets its shared sign-in: its address and password are on the login
	// page, so a lock would only let anyone keep every visitor out.
	noLockoutEmail string
}

// NewService creates a new authentication service. Sessions are the only
// credential: Login issues one, ValidateSession looks them up.
func NewService(userRepo user.Repository, tenantLookup TenantLookup, sessions *session.Store) *Service {
	return &Service{userRepo: userRepo, tenantLookup: tenantLookup, sessions: sessions}
}

// NeverLock exempts one account, by email, from the failed-login lockout (the
// public demo's shared login). The per-address rate limit still applies.
func (s *Service) NeverLock(email string) {
	s.noLockoutEmail = strings.ToLower(strings.TrimSpace(email))
}

func (s *Service) neverLocked(email string) bool {
	return s.noLockoutEmail != "" && strings.ToLower(strings.TrimSpace(email)) == s.noLockoutEmail
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse represents a successful login response. SessionToken is sent
// as `Authorization: Bearer ...` (or carried by the session cookie).
type LoginResponse struct {
	User         *user.User   `json:"user"`
	SessionToken string       `json:"session_token"`
	Tenants      []TenantInfo `json:"tenants"`
	// CSRFToken is set only when cookie auth is on: send it as X-CSRF-Token on
	// unsafe requests authenticated by the API session cookie.
	CSRFToken string `json:"csrf_token,omitempty"`
}

var (
	dummyOnce sync.Once
	dummyHsh  string
)

// dummyHash is a valid hash nobody knows the password of.
func dummyHash() string {
	dummyOnce.Do(func() { dummyHsh, _ = HashPassword("hatamoto-no-such-user-" + time.Now().String()) })
	return dummyHsh
}

// authenticate checks email and password with the lockout policy: a locked
// account is rejected before spending a password verification, a failure is
// recorded (locking at the threshold), a success clears the failure state.
func (s *Service) authenticate(ctx context.Context, email, password string) (*user.User, error) {
	// Use superadmin context for login since we don't know the tenant yet
	superCtx := database.WithSuperadmin(ctx)

	// Find user by email
	u, err := s.userRepo.GetByEmail(superCtx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			// Spend the same hashing time as a wrong password so response
			// timing does not reveal which addresses have accounts.
			if _, err := VerifyPasswordContext(ctx, password, dummyHash()); err != nil {
				return nil, fmt.Errorf("sign-in abandoned: %w", err)
			}
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// Reject a locked account before spending a password verification. Checked
	// against the persisted lockout state loaded by GetByEmail.
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) && !s.neverLocked(u.Email) {
		// Spend the hashing time an unknown address spends, so a fast answer
		// does not mark the account as existing and locked.
		if _, err := VerifyPasswordContext(ctx, password, dummyHash()); err != nil {
			return nil, fmt.Errorf("sign-in abandoned: %w", err)
		}
		return nil, ErrAccountLocked
	}

	// Verify password
	valid, err := VerifyPasswordContext(ctx, password, u.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("failed to verify password: %w", err)
	}
	if !valid {
		// Record the failure (increments counter, locks at the threshold).
		// Best-effort: a bookkeeping error must not change the auth outcome.
		if s.neverLocked(u.Email) {
			return nil, ErrInvalidCredentials
		}
		if recErr := s.userRepo.RecordFailedLogin(superCtx, u.ID, maxFailedLogins, lockoutDuration); recErr != nil {
			fmt.Printf("failed to record failed login (non-fatal): %v\n", recErr)
		}
		return nil, ErrInvalidCredentials
	}

	// Successful password: clear any accumulated failure state.
	if rstErr := s.userRepo.ResetFailedLogins(superCtx, u.ID); rstErr != nil {
		fmt.Printf("failed to reset failed logins (non-fatal): %v\n", rstErr)
	}

	return u, nil
}

// LoginSession authenticates and issues a server-side session. It returns the
// user and the opaque session token to set as the cookie.
func (s *Service) LoginSession(ctx context.Context, email, password, userAgent, ip string) (*user.User, string, error) {
	u, err := s.authenticate(ctx, email, password)
	if err != nil {
		return nil, "", err
	}
	if s.sessions == nil {
		return nil, "", errors.New("sessions are not configured")
	}
	token, err := s.IssueSession(ctx, u.ID, userAgent, ip)
	if err != nil {
		return nil, "", err
	}
	if err := s.userRepo.UpdateLastLogin(ctx, u.ID); err != nil {
		fmt.Printf("failed to update last login: %v\n", err)
	}
	u.PasswordHash = ""
	return u, token, nil
}

// LoginAPISession authenticates and issues a kind-api session for the JSON
// API login. It returns the user and the opaque session token, which is both
// the bearer token and the API cookie value. It never issues an admin session.
func (s *Service) LoginAPISession(ctx context.Context, email, password, userAgent, ip string) (*user.User, string, error) {
	u, err := s.authenticate(ctx, email, password)
	if err != nil {
		return nil, "", err
	}
	if s.sessions == nil {
		return nil, "", errors.New("sessions are not configured")
	}
	token, _, err := s.sessions.IssueAPI(database.WithSuperadmin(ctx), u.ID, adminSessionTTL, userAgent, ip)
	if err != nil {
		return nil, "", err
	}
	if err := s.userRepo.UpdateLastLogin(ctx, u.ID); err != nil {
		fmt.Printf("failed to update last login: %v\n", err)
	}
	u.PasswordHash = ""
	return u, token, nil
}

// IssueSession starts a 30-day admin session for userID (after reset, invite
// acceptance or setup) and returns the cookie value.
func (s *Service) IssueSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (string, error) {
	if s.sessions == nil {
		return "", errors.New("sessions are not configured")
	}
	token, _, err := s.sessions.IssueAdmin(database.WithSuperadmin(ctx), userID, adminSessionTTL, userAgent, ip)
	return token, err
}

// Login authenticates a user with email and password.
//
// userAgent and ip are recorded against the server-side session for
// auditability ("active sessions" admin view). Pass empty strings if the
// caller can't determine them; they're optional.
func (s *Service) Login(ctx context.Context, req *LoginRequest, userAgent, ip string) (*LoginResponse, error) {
	u, token, err := s.LoginAPISession(ctx, req.Email, req.Password, userAgent, ip)
	if err != nil {
		return nil, err
	}
	tenants, _ := s.listTenantMemberships(database.WithSuperadmin(ctx), u.Email)
	return &LoginResponse{User: u, SessionToken: token, Tenants: tenants}, nil
}

// ValidateSession looks up a server-side admin session by its opaque token.
// It accepts the admin kind only; see ValidateBearerSession and
// ValidateAPICookieSession for the JSON API.
func (s *Service) ValidateSession(ctx context.Context, token string) (*Claims, error) {
	return s.validateKinds(ctx, token, session.KindAdmin)
}

// ValidateBearerSession validates a token presented as a bearer credential:
// admin and api sessions are both accepted, since a bearer token is never
// sent by the browser on its own.
func (s *Service) ValidateBearerSession(ctx context.Context, token string) (*Claims, error) {
	return s.validateKinds(ctx, token, session.KindAdmin, session.KindAPI)
}

// ValidateAPICookieSession validates a token read from the API session cookie.
// Only the api kind is accepted, so the admin form session never works on
// the JSON API.
func (s *Service) ValidateAPICookieSession(ctx context.Context, token string) (*Claims, error) {
	return s.validateKinds(ctx, token, session.KindAPI)
}

// validateKinds looks up a server-side session by its opaque token, requires
// its kind to be one of kinds, and synthesizes a *Claims equivalent so middleware can use the same
// context shape regardless of which auth path validated the request.
//
// Returns nil + error on any failure (missing/expired/wrong-kind/user-gone).
// Errors are intentionally opaque — callers must not differentiate.
func (s *Service) validateKinds(ctx context.Context, token string, kinds ...session.Kind) (*Claims, error) {
	if s.sessions == nil {
		return nil, errors.New("session validation not configured")
	}

	sess, err := s.sessions.Validate(ctx, token)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(kinds, sess.Kind) || sess.UserID == nil {
		return nil, session.ErrInvalid
	}

	// Load the user to synthesize claims. Use superadmin context because
	// the session row itself has no tenant_id; we need the user to know
	// which tenant to scope into.
	superCtx := database.WithSuperadmin(ctx)
	u, err := s.userRepo.GetByID(superCtx, *sess.UserID)
	if err != nil {
		return nil, session.ErrInvalid
	}

	return &Claims{
		UserID:       u.ID,
		Email:        u.Email,
		Role:         string(u.Role),
		IsSuperadmin: u.Role == user.RoleOwner,
	}, nil
}

// legacyAuthTenantID is the constant ID reported in TenantInfo (the API login
// response keeps that field for existing clients; it has no tenant meaning).
var legacyAuthTenantID = uuid.MustParse("2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3")

// listTenantMemberships returns all tenant memberships for the given email.
func (s *Service) listTenantMemberships(ctx context.Context, email string) ([]TenantInfo, error) {
	if s.tenantLookup == nil {
		return nil, nil
	}

	users, err := s.userRepo.ListByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	var tenants []TenantInfo
	for _, u := range users {
		name, slug, err := s.tenantLookup.GetTenantNameSlug(ctx)
		if err != nil {
			continue
		}
		tenants = append(tenants, TenantInfo{
			ID:   legacyAuthTenantID,
			Name: name,
			Slug: slug,
			Role: string(u.Role),
		})
	}
	return tenants, nil
}

// ListTenantMemberships is the exported version for use by handlers.
func (s *Service) ListTenantMemberships(ctx context.Context, email string) ([]TenantInfo, error) {
	return s.listTenantMemberships(ctx, email)
}
