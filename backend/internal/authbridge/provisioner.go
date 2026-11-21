// Package authbridge connects the public sign-in interface (authstate) to
// core's user, session, key and token services.
package authbridge

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

// ProvisionerDeps are the services a Provisioner drives.
type ProvisionerDeps struct {
	Users      *user.PostgresRepository
	Identities *user.IdentityStore
	Sessions   *session.Store
	Keys       *auth.APIKeyService
	Tokens     *auth.UserTokens
	// AdminPath returns the admin mount path ("/admin" by default).
	AdminPath func() string
	Now       func() time.Time
	// Audit records a change made through the provisioner (nil: none).
	Audit func(ctx context.Context, ev auditstate.Event)
}

// Provisioner implements authstate.Provisioner on core's services.
type Provisioner struct{ d ProvisionerDeps }

var _ authstate.Provisioner = (*Provisioner)(nil)

// NewProvisioner returns a Provisioner over d.
func NewProvisioner(d ProvisionerDeps) *Provisioner {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Provisioner{d: d}
}

// audit records one change; the actor is the signed-in user when ctx has one,
// else the sign-in provider. It is called only after the change succeeded.
func (p *Provisioner) audit(ctx context.Context, action, target string, detail map[string]any) {
	if p.d.Audit == nil {
		return
	}
	ev := auditstate.Event{Actor: "system (sign-in provider)", ActorKind: "system", Action: action, Target: target, Detail: detail}
	if id, ok := database.GetUserID(ctx); ok {
		ev.ActorID, ev.Actor, ev.ActorKind = id.String(), id.String(), "user"
	}
	p.d.Audit(ctx, ev)
}

func toDTO(u *user.User) authstate.User {
	out := authstate.User{
		ID:           u.ID.String(),
		Email:        u.Email,
		Role:         string(u.Role),
		AccessEndsAt: u.AccessEndsAt,
		HasPassword:  u.PasswordHash != "",
	}
	if u.Name != nil {
		out.Name = *u.Name
	}
	return out
}

func parseID(id string) (uuid.UUID, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, authstate.ErrNotFound
	}
	return uid, nil
}

// mapErr turns repository errors into the public ones.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, user.ErrUserNotFound), errors.Is(err, user.ErrIdentityNotFound):
		return authstate.ErrNotFound
	case errors.Is(err, user.ErrLastOwner):
		return authstate.ErrLastOwner
	case errors.Is(err, user.ErrInvalidRole):
		return authstate.ErrInvalidRole
	case errors.Is(err, user.ErrEmailAlreadyExists):
		return authstate.ErrEmailTaken
	case errors.Is(err, user.ErrIdentityTaken):
		return authstate.ErrIdentityTaken
	case errors.Is(err, auth.ErrResetThrottle):
		return authstate.ErrThrottled
	}
	return err
}

// FindByID returns the user with the given id.
func (p *Provisioner) FindByID(ctx context.Context, id string) (authstate.User, error) {
	uid, err := parseID(id)
	if err != nil {
		return authstate.User{}, err
	}
	u, err := p.d.Users.GetByID(database.WithSuperadmin(ctx), uid)
	if err != nil {
		return authstate.User{}, mapErr(err)
	}
	return toDTO(u), nil
}

// FindByEmail finds a user by email, ignoring case and surrounding space.
func (p *Provisioner) FindByEmail(ctx context.Context, email string) (authstate.User, error) {
	u, err := p.d.Users.GetByEmailFold(database.WithSuperadmin(ctx), email)
	if err != nil {
		if errors.Is(err, user.ErrEmailAmbiguous) {
			return authstate.User{}, authstate.ErrEmailTaken
		}
		return authstate.User{}, mapErr(err)
	}
	return toDTO(u), nil
}

// FindByIdentity returns the user linked to (issuer, subject).
func (p *Provisioner) FindByIdentity(ctx context.Context, issuer, subject string) (authstate.User, error) {
	ctx = database.WithSuperadmin(ctx)
	uid, err := p.d.Identities.UserFor(ctx, issuer, subject)
	if err != nil {
		return authstate.User{}, mapErr(err)
	}
	u, err := p.d.Users.GetByID(ctx, uid)
	if err != nil {
		return authstate.User{}, mapErr(err)
	}
	return toDTO(u), nil
}

// Create adds a user without a password.
func (p *Provisioner) Create(ctx context.Context, in authstate.NewUser) (authstate.User, error) {
	ctx = database.WithSuperadmin(ctx)
	role := user.Role(in.Role)
	if !user.ValidRole(role) {
		return authstate.User{}, authstate.ErrInvalidRole
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" {
		return authstate.User{}, authstate.ErrInvalidEmail
	}
	if _, err := p.d.Users.GetByEmailFold(ctx, email); err == nil || errors.Is(err, user.ErrEmailAmbiguous) {
		return authstate.User{}, authstate.ErrEmailTaken
	} else if !errors.Is(err, user.ErrUserNotFound) {
		return authstate.User{}, err
	}
	u, err := p.d.Users.Create(ctx, &user.CreateUserInput{
		Email: email, Role: role, Name: in.Name, AccessEndsAt: in.AccessEndsAt,
	}, "")
	if err != nil {
		return authstate.User{}, mapErr(err)
	}
	p.audit(ctx, "user.created", u.Email, map[string]any{"role": string(u.Role), "via": "provider"})
	return toDTO(u), nil
}

// FindOrCreate returns the user with the email, creating one when there is
// none. The bool reports whether it was created.
func (p *Provisioner) FindOrCreate(ctx context.Context, in authstate.NewUser) (authstate.User, bool, error) {
	u, err := p.FindByEmail(ctx, in.Email)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, authstate.ErrNotFound) {
		return authstate.User{}, false, err
	}
	u, err = p.Create(ctx, in)
	if err != nil {
		return authstate.User{}, false, err
	}
	return u, true, nil
}

// Update changes a user's name or role. Owner guards apply.
func (p *Provisioner) Update(ctx context.Context, id string, c authstate.Changes) (authstate.User, error) {
	uid, err := parseID(id)
	if err != nil {
		return authstate.User{}, err
	}
	in := &user.UpdateUserInput{Name: c.Name}
	var before user.Role
	if c.Role != nil {
		role := user.Role(*c.Role)
		if !user.ValidRole(role) {
			return authstate.User{}, authstate.ErrInvalidRole
		}
		in.Role = &role
		if p.d.Audit != nil {
			old, err := p.d.Users.GetByID(database.WithSuperadmin(ctx), uid)
			if err != nil {
				return authstate.User{}, mapErr(err)
			}
			before = old.Role
		}
	}
	u, err := p.d.Users.Update(database.WithSuperadmin(ctx), uid, in)
	if err != nil {
		return authstate.User{}, mapErr(err)
	}
	if in.Role != nil && before != "" && before != *in.Role {
		p.audit(ctx, "user.role_changed", u.ID.String(), map[string]any{"from": string(before), "to": string(*in.Role)})
	}
	if c.Name != nil {
		p.audit(ctx, "user.updated", u.ID.String(), map[string]any{"changed": []string{"name"}})
	}
	return toDTO(u), nil
}

// SetAccessEnd sets or clears the end of access. An end that is not in the
// future also revokes the user's sessions and keys.
func (p *Provisioner) SetAccessEnd(ctx context.Context, id string, end *time.Time) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	if _, err := p.d.Users.Update(database.WithSuperadmin(ctx), uid, &user.UpdateUserInput{
		AccessEndsAt: user.AccessEnd{Set: true, At: end},
	}); err != nil {
		return mapErr(err)
	}
	var to any
	if end != nil {
		to = end.UTC().Format(time.RFC3339)
	}
	p.audit(ctx, "user.access_changed", id, map[string]any{"to": to})
	if end != nil && !end.After(p.d.Now()) {
		return p.RevokeSessionsAndKeys(ctx, id)
	}
	return nil
}

// Disable ends the user's access now and revokes their sessions and keys.
func (p *Provisioner) Disable(ctx context.Context, id string) error {
	now := p.d.Now().UTC()
	return p.SetAccessEnd(ctx, id, &now)
}

// RevokeSessionsAndKeys signs the user out everywhere and revokes the API
// keys they created.
func (p *Provisioner) RevokeSessionsAndKeys(ctx context.Context, id string) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	ctx = database.WithSuperadmin(ctx)
	errSessions := p.d.Sessions.RevokeAllForUser(ctx, uid)
	keys, errKeys := p.d.Keys.RevokeAllForCreator(ctx, uid)
	if err := errors.Join(errSessions, errKeys); err != nil {
		return err
	}
	p.audit(ctx, "user.sessions_revoked", id, map[string]any{"keys": keys})
	return nil
}

// LinkIdentity attaches (issuer, subject) to the user.
func (p *Provisioner) LinkIdentity(ctx context.Context, id, issuer, subject string) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	if err := p.d.Identities.Link(database.WithSuperadmin(ctx), uid, issuer, subject); err != nil {
		return mapErr(err)
	}
	p.audit(ctx, "user.identity_linked", id, map[string]any{"issuer": issuer})
	return nil
}

// UnlinkIdentity removes (issuer, subject) from the user.
func (p *Provisioner) UnlinkIdentity(ctx context.Context, id, issuer, subject string) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	if err := p.d.Identities.Unlink(database.WithSuperadmin(ctx), uid, issuer, subject); err != nil {
		return mapErr(err)
	}
	p.audit(ctx, "user.identity_unlinked", id, map[string]any{"issuer": issuer})
	return nil
}

// IssuePasswordSetLink returns a site-root-relative path that lets the user
// choose a password.
func (p *Provisioner) IssuePasswordSetLink(ctx context.Context, id string) (string, error) {
	uid, err := parseID(id)
	if err != nil {
		return "", err
	}
	tok, err := p.d.Tokens.IssueReset(database.WithSuperadmin(ctx), uid)
	if err != nil {
		return "", mapErr(err)
	}
	p.audit(ctx, "user.reset_link_sent", id, nil)
	return p.d.AdminPath() + "/reset?token=" + tok, nil
}
