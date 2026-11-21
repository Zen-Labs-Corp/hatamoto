package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/oidcsignin"
	"hatamoto.dev/core/internal/safefetch"
)

// signInSettingsStore is the saved OIDC sign-in row (a *oidcsignin.Store fits).
type signInSettingsStore interface {
	Get(ctx context.Context) (oidcsignin.Settings, bool, error)
	Save(ctx context.Context, set oidcsignin.Settings) error
}

// signInChecker is the live provider: it drops its caches after a save and
// checks an issuer (a *oidcsignin.Provider fits).
type signInChecker interface {
	Invalidate()
	Check(ctx context.Context, s oidcsignin.Settings) error
}

// signInAdmin adapts the OIDC sign-in store and provider to the admin.
type signInAdmin struct {
	store signInSettingsStore
	key   []byte
	prov  signInChecker
}

func (a signInAdmin) Get(ctx context.Context) (admin.SignInOIDCView, error) {
	set, found, err := a.store.Get(ctx)
	if err != nil {
		return admin.SignInOIDCView{}, err
	}
	v := admin.SignInOIDCView{SignInOIDCInput: admin.SignInOIDCInput{DefaultRole: oidcDefaultRole}}
	if !found {
		return v, nil
	}
	v.Enabled, v.Label, v.Issuer, v.ClientID = set.Enabled, set.Label, set.Issuer, set.ClientID
	v.Domains, v.DefaultRole, v.LinkExisting = set.AllowedDomains, set.DefaultRole, set.LinkExisting
	if set.HasSecret() {
		_, opens := set.Secret(a.key)
		v.SecretSaved, v.SecretLost = opens, !opens
	}
	return v, nil
}

// oidcDefaultRole is the role shown before anything is saved.
const oidcDefaultRole = "editor"

func (a signInAdmin) Save(ctx context.Context, in admin.SignInOIDCInput, keepSecret bool) error {
	set := oidcsignin.Settings{
		Enabled: in.Enabled, Label: in.Label, Issuer: in.Issuer, ClientID: in.ClientID,
		AllowedDomains: in.Domains, DefaultRole: in.DefaultRole, LinkExisting: in.LinkExisting,
	}
	if keepSecret {
		stored, found, err := a.store.Get(ctx)
		if err != nil {
			return err
		}
		if !found || !stored.HasSecret() {
			return errors.New("no client secret is saved; enter it")
		}
		if _, ok := stored.Secret(a.key); !ok {
			return errors.New("the saved client secret cannot be read; enter it again")
		}
		set.SecretSealed = stored.SecretSealed
	} else if in.Secret != "" {
		sealed, err := oidcsignin.SealSecret(a.key, in.Secret)
		if err != nil {
			return fmt.Errorf("seal client secret: %w", err)
		}
		set.SecretSealed = sealed
	}
	if err := a.store.Save(ctx, set); err != nil {
		return err
	}
	a.prov.Invalidate()
	return nil
}

func (a signInAdmin) Check(ctx context.Context, issuer string) error {
	return a.prov.Check(ctx, oidcsignin.Settings{Issuer: issuer})
}

// setOIDCRuntime gives the compiled-in OIDC provider what it needs to serve:
// the settings row, the key that seals its secret, an outbound client that
// refuses private addresses, and the audit trail. Serve only.
func (s *Server) setOIDCRuntime() {
	pool := s.db.Pool()
	oidcsignin.Default().SetRuntime(oidcsignin.Runtime{
		Settings:  oidcsignin.NewStore(pool),
		SecretKey: oidcsignin.SecretKey(s.cfg.Secret),
		Client:    safefetch.NewClient(safefetch.ClientOptions{Timeout: 10 * time.Second}),
		Audit: func(ctx context.Context, ev auditstate.Event) {
			if err := (auditlog.Recorder{Pool: pool}).Record(ctx, nil, ev); err != nil {
				s.logger.Error("sign-in audit write failed", "action", ev.Action, "error", err)
			}
		},
	})
}
