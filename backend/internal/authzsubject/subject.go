// Package authzsubject builds the authz.Subject for a request. Every gate uses
// these constructors, so a request made with an API key can never be described
// as a session.
package authzsubject

import (
	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

// ForKey is the only constructor for a request made with an API key. KeyID is
// always set (a key subject is decided by its scopes and creator, never by a
// role) and Scopes is a copy.
func ForKey(k *auth.APIKey) authz.Subject {
	s := authz.Subject{KeyID: k.ID.String(), CreatorRole: k.CreatorRole, Scopes: make([]string, len(k.Scopes))}
	for i, sc := range k.Scopes {
		s.Scopes[i] = string(sc)
	}
	if k.CreatedBy != nil {
		s.UserID = k.CreatedBy.String()
	}
	return s
}

// ForSession describes a signed-in user: the admin, a REST session or a plugin
// route caller.
func ForSession(userID, role string) authz.Subject {
	return authz.Subject{UserID: userID, Role: role}
}
