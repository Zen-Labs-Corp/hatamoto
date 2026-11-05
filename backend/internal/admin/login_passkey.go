package admin

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"

	"github.com/google/uuid"

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
func (a *Admin) passkeyMode(r *http.Request) (uid *uuid.UUID, next string) {
	if p, ok := a.readPending(r); ok {
		return &p.UserID, p.Next
	}
	return nil, a.nextParam(r)
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
	case errors.Is(err, twofactor.ErrPasskeyFailed), errors.Is(err, twofactor.ErrNotFound):
		a.logger().Info("passkey sign-in refused", "reason", "refused")
		a.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": sudoMessage})
	default:
		a.logger().Error("passkey sign-in failed", "err", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": sudoUnavailableMessage})
	}
}

func (a *Admin) handlePasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.passkeyReady(w, r); !ok {
		return
	}
	uid, _ := a.passkeyMode(r)
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
	uid, next := a.passkeyMode(r)
	got, err := a.deps.Passkeys.FinishLogin(r.Context(), r.URL.Query().Get(passkeyCeremonyParam), uid, r)
	if err != nil {
		a.passkeyError(w, r, err)
		return
	}
	// Second-factor mode: the passkey must belong to the user the password proved.
	if uid != nil && got != *uid {
		a.passkeyError(w, r, twofactor.ErrPasskeyFailed)
		return
	}
	token, err := ps.IssueVerifiedSession(r.Context(), got, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.passkeyError(w, r, err)
		return
	}
	a.clearPending(w, r)
	a.setSession(w, r, token)
	if next = SafeNextFor(a.adminPath, next); next == "" {
		next = a.landing()
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

// passkeysOn says passkey sign-in is wired, so the sign-in page offers it.
func (a *Admin) passkeysOn() bool {
	_, ok := a.deps.Auth.(PasswordStep)
	return ok && a.deps.TwoFactor != nil && a.deps.Passkeys != nil
}
