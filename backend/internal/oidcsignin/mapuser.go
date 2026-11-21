package oidcsignin

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authstate"
)

const maxNameRunes = 200

// MapUser decides which account an identity signs in as. An identity already
// linked keeps its user (the link is keyed by issuer and subject; the domain
// rules govern create and link only). Otherwise the email must be verified and
// in an allowed domain; then an existing account is linked only when the owner
// allowed it, and a new account is created with the default role.
func (p *Provider) MapUser(ctx context.Context, req authstate.MapRequest) (authstate.User, error) {
	if req.Linked != nil {
		return *req.Linked, nil
	}
	rt, s, ok := p.active(ctx)
	if !ok {
		return authstate.User{}, refuse(reasonDisabled, nil)
	}
	id := req.Identity
	if !id.EmailVerified {
		return authstate.User{}, refuse(reasonEmailUnverified, nil)
	}
	if !domainAllowed(id.Email, s.AllowedDomains) {
		return authstate.User{}, refuse(reasonDomainNotAllowed, nil)
	}

	u, err := req.Users.FindByEmail(ctx, id.Email)
	switch {
	case err == nil && s.LinkExisting:
		if err := req.Users.LinkIdentity(ctx, u.ID, id.Issuer, id.Subject); err != nil {
			return authstate.User{}, linkRefusal(err)
		}
		audit(ctx, rt, "signin.identity_linked", u, nil)
		return u, nil
	case err == nil, errors.Is(err, authstate.ErrEmailTaken):
		return authstate.User{}, refuse(reasonAccountExists, nil)
	case !errors.Is(err, authstate.ErrNotFound):
		return authstate.User{}, refuse(reasonCreateFailed, err)
	}

	// Create and link must both happen or neither is useful: an unlinked
	// account is refused forever. Do not let a disconnect split them.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	u, err = req.Users.Create(wctx, authstate.NewUser{Email: id.Email, Name: nameOr(id), Role: s.DefaultRole})
	if errors.Is(err, authstate.ErrEmailTaken) {
		return authstate.User{}, refuse(reasonAccountExists, nil)
	}
	if err != nil {
		return authstate.User{}, refuse(reasonCreateFailed, err)
	}
	if err := req.Users.LinkIdentity(wctx, u.ID, id.Issuer, id.Subject); err != nil {
		return authstate.User{}, linkRefusal(err)
	}
	audit(ctx, rt, "signin.user_created", u, map[string]any{"role": u.Role})
	return u, nil
}

func linkRefusal(err error) error {
	if errors.Is(err, authstate.ErrIdentityTaken) {
		return refuse(reasonIdentityTaken, err)
	}
	return refuse(reasonLinkFailed, err)
}

// domainAllowed compares the part after the last "@" with the allowed domains,
// exactly: "a.acme.com" does not match "acme.com".
func domainAllowed(email string, allowed []string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range allowed {
		if d == domain {
			return true
		}
	}
	return false
}

// nameOr is the claim name (at most 200 characters), else the email's local part.
func nameOr(id authstate.Identity) string {
	if n := strings.TrimSpace(id.Name); n != "" {
		if utf8.RuneCountInString(n) > maxNameRunes {
			n = string([]rune(n)[:maxNameRunes])
		}
		return n
	}
	if at := strings.LastIndexByte(id.Email, '@'); at > 0 {
		return id.Email[:at]
	}
	return id.Email
}

// audit records an account change this provider made. Refusals are audited by
// core, so nothing about a refused person is written here.
func audit(ctx context.Context, rt *Runtime, action string, u authstate.User, extra map[string]any) {
	if rt.Audit == nil {
		return
	}
	detail := map[string]any{"provider": "oidc"}
	for k, v := range extra {
		detail[k] = v
	}
	rt.Audit(ctx, auditstate.Event{
		ActorID: u.ID, Actor: u.Email, ActorKind: "user",
		Action: action, Target: "user:" + u.ID, Detail: detail,
	})
}
