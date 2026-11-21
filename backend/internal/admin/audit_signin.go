package admin

import (
	"maps"
	"net/http"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/middleware"
)

// Sign-in methods and failure reasons. They are audit detail values, never
// secrets.
const (
	SignInPassword = "password"
	SignInTOTP     = "totp"
	SignInRecovery = "recovery"
	SignInPasskey  = "passkey"
	SignInProvider = "provider"
	SignInReset    = "reset"
	SignInInvite   = "invite"

	ReasonCredentials  = "credentials"
	ReasonLocked       = "locked"
	ReasonSecondFactor = "second_factor"
)

// signInAuditEvent builds a session event. The sign-in paths run before there
// is a signed-in user, so the actor is passed in and never read from ctx.
func signInAuditEvent(r *http.Request, action, actorID, actor, result string, detail map[string]any) AuditEvent {
	return AuditEvent{
		ActorID: actorID, Actor: actor, ActorKind: audit.KindUser, Result: result,
		Action: action, Target: "session", Detail: detail,
		IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-ID"),
	}
}

func signInDetail(key, value string, extra map[string]any) map[string]any {
	d := map[string]any{key: value, "via": "admin"}
	maps.Copy(d, extra)
	return d
}

// auditSignIn records a completed sign-in.
func (a *Admin) auditSignIn(r *http.Request, actorID, actor, method string, extra map[string]any) {
	if a.deps.Audit == nil {
		return
	}
	a.deps.Audit(r.Context(), signInAuditEvent(r, AuditSignIn, actorID, actor, audit.ResultOK, signInDetail("method", method, extra)))
}

// auditSignInFailed records a refused sign-in. actor must already be safe to
// store (failedActor for a typed email).
func (a *Admin) auditSignInFailed(r *http.Request, actorID, actor, reason string, extra map[string]any) {
	if a.deps.Audit == nil {
		return
	}
	a.deps.Audit(r.Context(), signInAuditEvent(r, AuditSignInFailed, actorID, actor, audit.ResultDenied, signInDetail("reason", reason, extra)))
}

// auditSignOut records a sign-out; all is true when every session of the user ended.
func (a *Admin) auditSignOut(r *http.Request, actorID, actor string, all bool) {
	if a.deps.Audit == nil {
		return
	}
	a.deps.Audit(r.Context(), signInAuditEvent(r, AuditSignOut, actorID, actor, audit.ResultOK, map[string]any{"all": all, "via": "admin"}))
}

// failedActor is the audit actor of a failed sign-in: the keyed hash of the
// typed email, never the address. The same value is used whether or not the
// address belongs to an account, so the log does not reveal which exist.
func (a *Admin) failedActor(email string) string {
	if a.deps.HashEmail == nil || email == "" {
		return "unknown"
	}
	return a.deps.HashEmail(email)
}
