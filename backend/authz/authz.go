package authz

import (
	"context"
	"errors"

	"hatamoto.dev/core/internal/authzstate"
)

// Subject is who asks. A signed-in session fills UserID and Role. A request
// made with an API key also fills KeyID, Scopes and CreatorRole.
//
// A subject with a non-empty KeyID is a key subject: Role is ignored and
// CreatorRole and Scopes decide. Callers must set KeyID for every request made
// with a key, including REST requests, which carry synthesised editor claims;
// a key request described without KeyID is treated as an editor session.
type Subject struct {
	// UserID is the signed-in user, or the key's creator.
	UserID string
	// Role is the session role ("owner", "editor", "author", "contributor", "reviewer" or "read-only"). It is ignored for key
	// subjects (KeyID != ""); leave it empty there and set CreatorRole.
	Role string
	// KeyID is non-empty when the request is made with an API key.
	KeyID string
	// Scopes are the key's scopes, for example "read:entities" or "*".
	Scopes []string
	// CreatorRole is the key creator's current role.
	CreatorRole string
}

// Object is what an action is about. The zero Object asks whether the subject
// may perform the action on some object at all (route gates, showing a
// button); a check about one entity or media item passes it. Every field is
// optional.
type Object struct {
	// Kind names the type of object, for example "entity" or "media".
	Kind string
	// ID identifies the object.
	ID string
	// Schema is the content type of an entity.
	Schema string
	// Locale is the locale of the object.
	Locale string
	// Section names the area of a settings page.
	Section string
	// CreatedBy is the user who created the entity or uploaded the media
	// item, not the byline; "" when unknown.
	CreatedBy string
	// Status is the entity status: "draft", "scheduled", "published" or
	// "archived"; "" for other kinds.
	Status string
}

// Action names an operation, such as ActionEntityPublish. The catalogue
// constants are listed by Actions; a module may define more.
type Action string

// Authorizer decides whether a subject may perform an action on an object.
// A non-nil error means deny.
type Authorizer interface {
	Can(ctx context.Context, s Subject, a Action, o Object) (bool, error)
}

// ErrUnknownAction is returned (wrapped) by the default authorizer for an
// action outside the catalogue.
var ErrUnknownAction = errors.New("authz: unknown action")

// Register installs a, which then answers every Can call instead of the
// default authorizer. A module delegates to Default for the actions it does
// not care about. Call it from an init function: it panics after app.Main has
// started, on a second call, and with a nil (or typed-nil) authorizer.
func Register(a Authorizer) { authzstate.Set(a) }

// Default returns the built-in authorizer, which answers what the built-in
// roles and API key scopes allow.
func Default() Authorizer { return defaultAuthorizer{} }

// Can asks the registered authorizer, or the default one when none is
// registered. It fails closed: when the authorizer returns an error the
// answer is false and the error is passed on. A panic is not recovered.
func Can(ctx context.Context, s Subject, a Action, o Object) (bool, error) {
	if v := authzstate.Load(); v != nil {
		ok, err := v.(Authorizer).Can(ctx, s, a, o)
		if err != nil {
			return false, err
		}
		return ok, nil
	}
	return Default().Can(ctx, s, a, o)
}
