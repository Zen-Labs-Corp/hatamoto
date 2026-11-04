package server

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/twofactor"
)

// sudoFactor adapts the two-factor service to the admin's sudo seam: the only
// difference is the error type for a wrong code.
type sudoFactor struct{ svc *twofactor.Service }

func (f sudoFactor) Has(ctx context.Context, id uuid.UUID) (bool, error) { return f.svc.Has(ctx, id) }

func (f sudoFactor) Verify(ctx context.Context, id uuid.UUID, code string) error {
	err := f.svc.Verify(ctx, id, code)
	if errors.Is(err, twofactor.ErrWrongCode) {
		return admin.ErrWrongCode
	}
	return err
}

// initTwoFactor builds the second-factor service and passkeys and closes the
// password-only session paths of the auth service.
func (s *Server) initTwoFactor() {
	if s.db == nil || s.authService == nil || s.userRepo == nil {
		return
	}
	store := twofactor.NewStore(s.db.Pool())
	svc := twofactor.NewService(store, s.userRepo, twofactor.Config{
		SealKey:     twofactor.SealKeyFor(s.cfg.Secret),
		RecoveryKey: twofactor.RecoveryKey(s.cfg.Secret),
		MaxFailed:   auth.MaxFailedLogins,
		Lock:        auth.LockoutDuration,
		Issuer:      s.siteName,
	})
	s.twoFactor = svc
	s.passkeys = twofactor.NewPasskeys(s.passkeyRP, store, svc, nil)
	s.authService.SetSecondFactor(svc, svc)
}

// passkeyRP is the relying party: the host of the configured site address.
// Without an address there is none, and passkeys are refused (the
// authenticator app still works).
func (s *Server) passkeyRP(ctx context.Context) (twofactor.RP, error) {
	base := ""
	if s.cfg != nil {
		base = strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/")
	}
	if base == "" && s.siteConfig != nil {
		if c, err := s.siteConfig.Get(ctx); err == nil {
			base = strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return twofactor.RP{}, twofactor.ErrNoRelyingParty
	}
	return twofactor.RP{ID: u.Hostname(), Name: s.siteName(ctx), Origins: []string{u.Scheme + "://" + u.Host}}, nil
}

// wireTwoFactor hands the admin its two-factor services and the sudo provider.
func (s *Server) wireTwoFactor(d *admin.Deps) {
	if s.twoFactor == nil {
		return
	}
	d.TwoFactor = s.twoFactor
	d.Passkeys = s.passkeys
	d.PendingKey = twofactor.PendingKeyFor(s.cfg.Secret)
}
