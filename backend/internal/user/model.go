// Package user provides user domain models and repository interfaces.
package user

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Role is a user's role: owner (everything) or editor (content).
type Role string

// Available user roles.
const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
)

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
}

// UpdateUserInput represents input for updating a user.
type UpdateUserInput struct {
	Name      *string
	AvatarURL *string
	Role      *Role
}

// ValidRole checks if a role is valid.
func ValidRole(r Role) bool {
	switch r {
	case RoleOwner, RoleEditor:
		return true
	default:
		return false
	}
}

// ParseRole parses a string into a Role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !ValidRole(r) {
		return "", ErrInvalidRole
	}
	return r, nil
}
