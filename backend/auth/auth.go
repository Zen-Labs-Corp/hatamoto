package auth

import (
	"hatamoto.dev/core/internal/authstate"
)

// Types a provider or policy works with.
type (
	// Provider signs users in through an outside identity service.
	Provider = authstate.Provider
	// Identity is what a provider asserts about the person who signed in.
	Identity = authstate.Identity
	// User is a user as a provider or policy sees it.
	User = authstate.User
	// StartRequest is passed to Provider.Start.
	StartRequest = authstate.StartRequest
	// StartResult tells core where to send the browser.
	StartResult = authstate.StartResult
	// FinishRequest is passed to Provider.Finish.
	FinishRequest = authstate.FinishRequest
	// MapRequest is passed to Provider.MapUser.
	MapRequest = authstate.MapRequest
	// ProviderInfo is a registered provider as listed on the login page.
	ProviderInfo = authstate.ProviderInfo
	// Method is how a user proved who they are.
	Method = authstate.Method
	// SignInAttempt is what a SignInPolicy decides on.
	SignInAttempt = authstate.SignInAttempt
	// SignInPolicy may refuse a sign-in.
	SignInPolicy = authstate.SignInPolicy
	// NewUser describes a user to create.
	NewUser = authstate.NewUser
	// Changes lists the fields Provisioner.Update changes.
	Changes = authstate.Changes
	// Provisioner manages users and identities. Core implements it.
	Provisioner = authstate.Provisioner
)

// MaxDataLen is the largest StartResult.Data core accepts, in bytes.
const MaxDataLen = authstate.MaxDataLen

// Sign-in methods a SignInPolicy sees.
const (
	MethodPassword = authstate.MethodPassword
	MethodPasskey  = authstate.MethodPasskey
	MethodProvider = authstate.MethodProvider
)

// Errors returned by the Provisioner.
var (
	ErrNotFound      = authstate.ErrNotFound
	ErrNotAvailable  = authstate.ErrNotAvailable
	ErrEmailTaken    = authstate.ErrEmailTaken
	ErrIdentityTaken = authstate.ErrIdentityTaken
	ErrInvalidRole   = authstate.ErrInvalidRole
	ErrInvalidEmail  = authstate.ErrInvalidEmail
	ErrLastOwner     = authstate.ErrLastOwner
	ErrThrottled     = authstate.ErrThrottled
	ErrRefused       = authstate.ErrRefused
)

// RegisterProvider adds a sign-in provider. Call it from an init function.
// id must match ^[a-z][a-z0-9-]{0,31}$ and name is 1-64 characters. It panics
// after app.Main started, on a duplicate id, and with a nil provider.
func RegisterProvider(id, name string, p Provider) { authstate.RegisterProvider(id, name, p) }

// RegisterSignInPolicy installs the sign-in policy. Call it from an init
// function. It panics after app.Main started, when called twice, and with a
// nil policy.
func RegisterSignInPolicy(p SignInPolicy) { authstate.RegisterSignInPolicy(p) }

// Providers lists the registered providers in registration order.
func Providers() []ProviderInfo { return authstate.Providers() }

// Provisioning returns core's user provisioning API. It returns
// ErrNotAvailable outside "hatamoto serve".
func Provisioning() (Provisioner, error) { return authstate.Provisioning() }
