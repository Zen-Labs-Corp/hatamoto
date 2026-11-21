package admin

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/twofactor"
)

// passkeyCeremonyParam carries the single-use ceremony id from begin to finish
// (the finish body is the browser's assertion, which the library parses).
const passkeyCeremonyParam = "c"

type passkeyBeginReply struct {
	Ceremony string         `json:"ceremony"`
	Options  jsontext.Value `json:"options"`
}

// passkeyMode reads the sign-in mode: with a valid pending cookie the passkey
// is the second factor of that user, without one it is a discoverable sign-in.
func (a *Admin) passkeyMode(r *http.Request) (uid *uuid.UUID, next string, via authstate.Via) {
	if p, ok := a.readPending(r); ok {
		return &p.UserID, p.Next, authstate.ParseVia(p.Via)
	}
	return nil, a.nextParam(r), authstate.Via{Method: authstate.MethodPasskey}
}

func (a *Admin) passkeyReady(w http.ResponseWriter, r *http.Request) (PasswordStep, bool) {
	ps, ok := a.deps.Auth.(PasswordStep)
	if !ok || a.deps.TwoFactor == nil || a.deps.Passkeys == nil {
		http.NotFound(w, r)
		return nil, false
	}
	return ps, true
}

// passkeyError answers a refused or failed ceremony. Every refusal is the same
// 401 text, so it never says which part failed or whether the account exists.
func (a *Admin) passkeyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, twofactor.ErrNoRelyingParty):
		a.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Set the site address first."})
	case errors.Is(err, twofactor.ErrPasskeyFailed), errors.Is(err, twofactor.ErrNotFound),
		errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrAccountLocked):
		a.logger().Info("passkey sign-in refused", "reason", "refused")
		a.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": sudoMessage})
	default:
		a.logger().Error("passkey sign-in failed", "err", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": sudoUnavailableMessage})
	}
}

// auditPasskeyRefused records a refused passkey sign-in. Faults (a missing
// relying party, a store error) are not sign-in outcomes and write nothing.
// actorID is the user the ceremony is known to be for, or "".
func (a *Admin) auditPasskeyRefused(r *http.Request, actorID string, err error) {
	if !errors.Is(err, twofactor.ErrPasskeyFailed) && !errors.Is(err, twofactor.ErrNotFound) && !errors.Is(err, auth.ErrInvalidCredentials) && !errors.Is(err, auth.ErrAccountLocked) {
		return
	}
	reason := ReasonCredentials
	if errors.Is(err, auth.ErrAccountLocked) {
		reason = ReasonLocked
	}
	actor := actorID
	if actor == "" {
		actor = "unknown"
	}
	a.auditSignInFailed(r, actorID, actor, reason, map[string]any{"method": SignInPasskey})
}

func (a *Admin) handlePasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.passkeyReady(w, r); !ok {
		return
	}
	uid, _, _ := a.passkeyMode(r)
	opts, id, err := a.deps.Passkeys.BeginLogin(r.Context(), uid)
	if err != nil {
		a.passkeyError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, passkeyBeginReply{Ceremony: id, Options: opts})
}

func (a *Admin) handlePasskeyFinish(w http.ResponseWriter, r *http.Request) {
	ps, ok := a.passkeyReady(w, r)
	if !ok {
		return
	}
	uid, next, via := a.passkeyMode(r)
	got, err := a.deps.Passkeys.FinishLogin(r.Context(), r.URL.Query().Get(passkeyCeremonyParam), uid, r)
	if err != nil {
		a.auditPasskeyRefused(r, uuidString(uid), err)
		a.passkeyError(w, r, err)
		return
	}
	// Second-factor mode: the passkey must belong to the user the password proved.
	if uid != nil && got != *uid {
		a.auditPasskeyRefused(r, uuidString(uid), twofactor.ErrPasskeyFailed)
		a.passkeyError(w, r, twofactor.ErrPasskeyFailed)
		return
	}
	token, err := ps.IssueVerifiedSession(r.Context(), got, via, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.auditPasskeyRefused(r, got.String(), err)
		a.passkeyError(w, r, err)
		return
	}
	step := "first_factor"
	if uid != nil {
		step = "second_factor"
	}
	a.auditSignIn(r, got.String(), a.signInActor(r, got), SignInPasskey, map[string]any{"step": step})
	a.clearPending(w, r)
	a.setSession(w, r, token)
	if next = SafeNextFor(a.adminPath, next); next == "" {
		next = a.landing()
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

// uuidString is the text of id, or "" when it is nil.
func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// passkeysOn says passkey sign-in is wired, so the sign-in page offers it.
func (a *Admin) passkeysOn() bool {
	_, ok := a.deps.Auth.(PasswordStep)
	return ok && a.deps.TwoFactor != nil && a.deps.Passkeys != nil
}
