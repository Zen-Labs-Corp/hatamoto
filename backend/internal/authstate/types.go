package authstate

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// MaxDataLen is the largest StartResult.Data core carries in the state cookie.
const MaxDataLen = 2048

// Method is how a user proved who they are.
type Method string

// Sign-in methods.
const (
	MethodPassword Method = "password"
	MethodPasskey  Method = "passkey"
	MethodProvider Method = "provider"
)

// Via names the sign-in path of one attempt.
type Via struct {
	Method   Method
	Provider string // set only for MethodProvider
}

// String returns "password", "passkey" or "provider:<id>".
func (v Via) String() string {
	switch v.Method {
	case MethodPasskey:
		return "passkey"
	case MethodProvider:
		return "provider:" + v.Provider
	}
	return "password"
}

// ParseVia is the inverse of String. Empty or unknown input means password.
func ParseVia(s string) Via {
	switch {
	case s == "passkey":
		return Via{Method: MethodPasskey}
	case strings.HasPrefix(s, "provider:") && len(s) > len("provider:"):
		return Via{Method: MethodProvider, Provider: strings.TrimPrefix(s, "provider:")}
	}
	return Via{Method: MethodPassword}
}

// User is a user as a provider or policy sees it.
type User struct {
	ID           string
	Email        string
	Name         string
	Role         string
	AccessEndsAt *time.Time
	HasPassword  bool
}

// Identity is what a provider asserts about the person who signed in.
// Issuer and Subject together identify the account at the provider. MFA
// states that the provider itself performed a second factor.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	MFA           bool
}

// StartRequest is passed to Provider.Start.
type StartRequest struct {
	CallbackURL string
	State       string
}

// StartResult tells core where to send the browser. Data is opaque, at most
// MaxDataLen bytes, and comes back in FinishRequest.Data.
type StartResult struct {
	RedirectURL string
	Data        string
}

// FinishRequest is passed to Provider.Finish after core checked the state.
type FinishRequest struct {
	CallbackURL string
	State       string
	Data        string
	Query       url.Values
}

// MapRequest is passed to Provider.MapUser. Linked is the user already
// linked to the identity, or nil.
type MapRequest struct {
	Identity Identity
	Linked   *User
	Users    Provisioner
}

// Provider signs users in through an outside identity service.
type Provider interface {
	Start(ctx context.Context, req StartRequest) (StartResult, error)
	Finish(ctx context.Context, req FinishRequest) (Identity, error)
	MapUser(ctx context.Context, req MapRequest) (User, error)
}

// ProviderInfo is a registered provider as listed on the login page.
type ProviderInfo struct {
	ID   string
	Name string
}

// SignInAttempt is what a SignInPolicy decides on.
type SignInAttempt struct {
	User       User
	Method     Method
	ProviderID string
}

// SignInPolicy may refuse a sign-in. It must be free of side effects.
type SignInPolicy interface {
	AllowSignIn(ctx context.Context, a SignInAttempt) (bool, error)
}

// NewUser describes a user to create.
type NewUser struct {
	Email        string
	Name         string
	Role         string
	AccessEndsAt *time.Time
}

// Changes lists the fields Update changes; nil fields stay as they are.
type Changes struct {
	Name *string
	Role *string
}

// Provisioner manages users and identities. Core implements it.
type Provisioner interface {
	FindByID(ctx context.Context, id string) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	FindByIdentity(ctx context.Context, issuer, subject string) (User, error)
	Create(ctx context.Context, u NewUser) (User, error)
	FindOrCreate(ctx context.Context, u NewUser) (User, bool, error)
	Update(ctx context.Context, id string, c Changes) (User, error)
	SetAccessEnd(ctx context.Context, id string, end *time.Time) error
	Disable(ctx context.Context, id string) error
	RevokeSessionsAndKeys(ctx context.Context, id string) error
	LinkIdentity(ctx context.Context, id, issuer, subject string) error
	UnlinkIdentity(ctx context.Context, id, issuer, subject string) error
	IssuePasswordSetLink(ctx context.Context, id string) (string, error)
}

// Errors returned by the Provisioner and by Allow.
var (
	ErrNotFound      = errors.New("auth: not found")
	ErrNotAvailable  = errors.New("auth: provisioning is not available in this process")
	ErrEmailTaken    = errors.New("auth: email is already in use")
	ErrIdentityTaken = errors.New("auth: identity is linked to another user")
	ErrInvalidRole   = errors.New("auth: invalid role")
	ErrInvalidEmail  = errors.New("auth: email is empty")
	ErrLastOwner     = errors.New("auth: this would leave no open owner")
	ErrThrottled     = errors.New("auth: too many requests, try again later")
	ErrRefused       = errors.New("auth: sign-in refused")
)
