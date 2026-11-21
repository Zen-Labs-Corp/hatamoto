// Package user provides user domain models and repository interfaces.
package user

import (
	"bytes"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
)

// Role is a user's role. Owners do everything; editors manage content; the
// other built-in roles are listed by Roles.
type Role string

// Built-in user roles.
const (
	RoleOwner       Role = "owner"
	RoleEditor      Role = "editor"
	RoleAuthor      Role = "author"
	RoleContributor Role = "contributor"
	RoleReviewer    Role = "reviewer"
	RoleReadOnly    Role = "read-only"
)

// Roles lists the built-in roles in the order screens and help text show them.
func Roles() []Role {
	return []Role{RoleOwner, RoleEditor, RoleAuthor, RoleContributor, RoleReviewer, RoleReadOnly}
}

// Label is the role's display name ("Read-only"); an unknown role returns its id.
func (r Role) Label() string {
	switch r {
	case RoleOwner:
		return "Owner"
	case RoleEditor:
		return "Editor"
	case RoleAuthor:
		return "Author"
	case RoleContributor:
		return "Contributor"
	case RoleReviewer:
		return "Reviewer"
	case RoleReadOnly:
		return "Read-only"
	}
	return string(r)
}

// Common errors.
var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidRole        = errors.New("invalid role")
	// ErrLastOwner is returned when a change would leave the site without an owner.
	ErrLastOwner = errors.New("cannot remove or demote the last owner")
)

// User represents a tenant user.
type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"` // Never expose
	Role         Role       `json:"role"`
	Name         *string    `json:"name,omitempty"`
	AvatarURL    *string    `json:"avatar_url,omitempty"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	// AccessEndsAt is the instant from which the user can no longer sign in
	// (nil: no end).
	AccessEndsAt *time.Time `json:"access_ends_at,omitempty"`
	// Login-lockout state (migration 004). Never serialized to clients.
	FailedLoginAttempts int        `json:"-"`
	LockedUntil         *time.Time `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateUserInput represents input for creating a new user.
type CreateUserInput struct {
	Email    string
	Password string
	Role     Role
	Name     string
	// AccessEndsAt is the optional end of access.
	AccessEndsAt *time.Time
}

// UpdateUserInput represents input for updating a user.
type UpdateUserInput struct {
	Name      *string
	AvatarURL *string
	Role      *Role
	// AccessEndsAt changes the end of access; the zero value leaves it alone.
	// It is a value, not a pointer: encoding/json skips UnmarshalJSON for a
	// null into a pointer, and null must mean "clear".
	AccessEndsAt AccessEnd `json:"access_ends_at"`
}

// AccessEnd is an optional change to a user's access end. Set is false when
// the JSON key was absent; At is nil when it was null (clear).
type AccessEnd struct {
	Set bool
	At  *time.Time
}

// UnmarshalJSON reads null (clear) or an RFC 3339 string. The decoder only
// calls it when the key is present.
func (a *AccessEnd) UnmarshalJSON(b []byte) error {
	a.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		a.At = nil
		return nil
	}
	var t time.Time
	if err := jsonx.Unmarshal(b, &t); err != nil {
		return errors.New("access_ends_at must be an RFC 3339 time or null")
	}
	a.At = &t
	return nil
}

// AccessEnded reports whether the user's access has ended at now.
func (u *User) AccessEnded(now time.Time) bool {
	return u != nil && u.AccessEndsAt != nil && !now.Before(*u.AccessEndsAt)
}

// ValidRole checks if a role is valid.
func ValidRole(r Role) bool {
	return slices.Contains(Roles(), r)
}

// ParseRole parses a string into a Role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !ValidRole(r) {
		return "", ErrInvalidRole
	}
	return r, nil
}
