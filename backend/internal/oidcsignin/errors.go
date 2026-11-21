package oidcsignin

import "errors"

// Refusal is why a sign-in was not accepted. Reason is a stable code that core
// writes to the audit log; Error never contains an email, subject, token or
// secret.
type Refusal struct {
	Reason string
	Err    error // cause for errors.Is/As; never printed by Error
}

// Reasons.
const (
	reasonDisabled         = "disabled"
	reasonDiscoveryFailed  = "discovery_failed"
	reasonConfigChanged    = "config_changed"
	reasonIDPError         = "idp_error"
	reasonNoCode           = "no_code"
	reasonExchangeFailed   = "exchange_failed"
	reasonNoIDToken        = "no_id_token"
	reasonTokenInvalid     = "token_invalid"
	reasonTokenExpired     = "token_expired"
	reasonNonceMismatch    = "nonce_mismatch"
	reasonAzpMismatch      = "azp_mismatch"
	reasonEmailUnverified  = "email_unverified"
	reasonDomainNotAllowed = "domain_not_allowed"
	reasonAccountExists    = "account_exists"
	reasonOwnerAccount     = "owner_account"
	reasonIdentityTaken    = "identity_taken"
	reasonCreateFailed     = "create_failed"
	reasonLinkFailed       = "link_failed"
)

func (r *Refusal) Error() string { return "oidc: " + r.Reason }

// Unwrap returns the cause, if any.
func (r *Refusal) Unwrap() error { return r.Err }

// AuditReason is the code core records as the reason of the refused sign-in.
func (r *Refusal) AuditReason() string { return r.Reason }

func refuse(reason string, err error) error { return &Refusal{Reason: reason, Err: err} }

// ReasonOf returns the refusal code in err's chain, or "".
func ReasonOf(err error) string {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Reason
	}
	return ""
}
