package authbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// maxIdentityPart bounds issuer and subject, matching the table's CHECKs.
const maxIdentityPart = 512

var (
	errUnknownProvider = errors.New("auth: unknown sign-in provider")
	errBadIdentity     = errors.New("auth: provider returned an unusable identity")
	errNotLinked       = errors.New("auth: provider mapped a user that is not linked to the identity")
)

// SignIn runs provider sign-in for the admin login routes.
type SignIn struct {
	prov   authstate.Provisioner
	idents *user.IdentityStore
}

// NewSignIn returns a SignIn that hands providers p as their Provisioner.
func NewSignIn(p authstate.Provisioner, idents *user.IdentityStore) *SignIn {
	return &SignIn{prov: p, idents: idents}
}

// Providers lists the registered providers that are available now, in
// registration order.
func (b *SignIn) Providers(ctx context.Context) []authstate.ProviderInfo {
	return authstate.AvailableProviders(ctx)
}

// Start asks provider id where to send the browser.
func (b *SignIn) Start(ctx context.Context, id, callbackURL, state string) (redirectURL, data string, err error) {
	p, ok := authstate.LookupAvailable(ctx, id)
	if !ok {
		return "", "", errUnknownProvider
	}
	res, err := p.Start(ctx, authstate.StartRequest{CallbackURL: callbackURL, State: state})
	if err != nil {
		return "", "", err
	}
	return res.RedirectURL, res.Data, nil
}

// Finish completes a sign-in and returns the user to open a session for and
// whether the provider asserted a second factor. Core never links an identity
// to a user on its own: MapUser must have linked it, or Finish fails closed.
func (b *SignIn) Finish(ctx context.Context, id string, req authstate.FinishRequest) (uuid.UUID, bool, error) {
	p, ok := authstate.LookupAvailable(ctx, id)
	if !ok {
		return uuid.Nil, false, errUnknownProvider
	}
	ident, err := p.Finish(ctx, req)
	if err != nil {
		return uuid.Nil, false, err
	}
	if ident.Issuer == "" || ident.Subject == "" || len(ident.Issuer) > maxIdentityPart || len(ident.Subject) > maxIdentityPart {
		return uuid.Nil, false, errBadIdentity
	}

	var linked *authstate.User
	switch u, err := b.prov.FindByIdentity(ctx, ident.Issuer, ident.Subject); {
	case err == nil:
		linked = &u
	case !errors.Is(err, authstate.ErrNotFound):
		return uuid.Nil, false, err
	}

	mapped, err := p.MapUser(ctx, authstate.MapRequest{Identity: ident, Linked: linked, Users: b.prov})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("provider could not map the identity: %w", err)
	}
	// Whatever MapUser returned, the identity must now belong to that user.
	now, err := b.prov.FindByIdentity(ctx, ident.Issuer, ident.Subject)
	if err != nil || now.ID != mapped.ID {
		return uuid.Nil, false, errNotLinked
	}
	uid, err := uuid.Parse(now.ID)
	if err != nil {
		return uuid.Nil, false, errNotLinked
	}
	if err := b.idents.Touch(database.WithSuperadmin(ctx), ident.Issuer, ident.Subject); err != nil {
		slog.Info("could not record provider sign-in time", "provider", id)
	}
	return uid, ident.MFA, nil
}
