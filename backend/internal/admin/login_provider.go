package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/secretbox"
	"hatamoto.dev/core/internal/twofactor"
)

// providerCookie carries the sealed state of one provider sign-in from the
// start route to the callback. It grants nothing by itself.
const providerCookie = "hm_prov"

// providerStateTTL is how long a started provider sign-in may take.
const providerStateTTL = 10 * time.Minute

// providerMessage is the only thing a failed provider sign-in says.
const providerMessage = "We could not sign you in with that provider. Try again."

// providerState is the sealed cookie payload.
type providerState struct {
	P string    `json:"p"` // provider id
	S string    `json:"s"` // state the provider must echo
	D string    `json:"d"` // provider's opaque data (PKCE verifier, nonce)
	N string    `json:"n"` // where to go after sign-in
	E time.Time `json:"e"` // expiry
	I string    `json:"i"` // single-use id
}

func providerAAD(id string) []byte { return []byte("sign-in-provider|" + id) }

func (a *Admin) providerCookie(r *http.Request, id, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: providerCookie, Value: value, Path: a.url("login/provider/" + id), MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secure(r)}
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// providerFor returns the provider sign-in when id is registered.
func (a *Admin) providerFor(id string) (ProviderSignIn, bool) {
	ps := a.deps.ProviderSignIn
	if ps == nil {
		return nil, false
	}
	for _, p := range ps.Providers() {
		if p.ID == id {
			return ps, true
		}
	}
	return nil, false
}

func (a *Admin) providerCallbackURL(r *http.Request, id string) string {
	base := a.siteAddress(r)
	if base == "" {
		return ""
	}
	return base + a.url("login/provider/"+id+"/callback")
}

// handleProviderStart sends the browser to the provider with a fresh state.
func (a *Admin) handleProviderStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps, ok := a.providerFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	next := a.nextParam(r)
	if len(a.deps.ProviderKey) == 0 {
		a.logger().Error("provider sign-in has no state key")
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "We could not sign you in. Try again in a moment.")
		return
	}
	callback := a.providerCallbackURL(r, id)
	if callback == "" {
		a.providerFailed(w, r, next, errors.New("the site address is not set"))
		return
	}
	state, err := randomToken(32)
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	spentID, err := randomToken(12)
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	redirect, data, err := ps.Start(r.Context(), id, callback, state)
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	if len(data) > authstate.MaxDataLen {
		a.providerFailed(w, r, next, errors.New("provider data is too long"))
		return
	}
	if u, err := url.Parse(redirect); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		a.providerFailed(w, r, next, errors.New("provider redirect is not an absolute http(s) address"))
		return
	}
	st := providerState{P: id, S: state, D: data, N: next, E: time.Now().Add(providerStateTTL), I: spentID}
	raw, err := jsonx.Marshal(st)
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	sealed, err := secretbox.Seal(a.deps.ProviderKey, providerAAD(id), raw)
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	http.SetCookie(w, a.providerCookie(r, id, base64.RawURLEncoding.EncodeToString(sealed), int(providerStateTTL/time.Second)))
	http.Redirect(w, r, redirect, http.StatusFound)
}

// openProviderState reads the valid, unspent state of this sign-in.
func (a *Admin) openProviderState(r *http.Request, id string) (providerState, bool) {
	var st providerState
	c, err := r.Cookie(providerCookie)
	if err != nil || c.Value == "" || len(a.deps.ProviderKey) == 0 {
		return st, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return st, false
	}
	raw, err := secretbox.Open(a.deps.ProviderKey, providerAAD(id), sealed)
	if err != nil || jsonx.Unmarshal(raw, &st) != nil {
		return providerState{}, false
	}
	now := time.Now()
	if st.P != id || !now.Before(st.E) || st.I == "" || a.spentProvider.has(st.I) {
		return providerState{}, false
	}
	return st, true
}

// handleProviderCallback finishes a provider sign-in: the sealed state must
// match, then core hands the callback to the provider and signs the mapped
// user in (through the second step unless the provider asserted one).
func (a *Admin) handleProviderCallback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps, ok := a.providerFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The state is single use: the cookie goes whatever happens next.
	http.SetCookie(w, a.providerCookie(r, id, "", -1))
	st, ok := a.openProviderState(r, id)
	if !ok {
		a.providerFailed(w, r, "", errors.New("missing, forged, expired or reused state"))
		return
	}
	a.spentProvider.add(st.I, st.E, time.Now())
	next := SafeNextFor(a.adminPath, st.N)
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.S)) != 1 {
		a.providerFailed(w, r, next, errors.New("state mismatch"))
		return
	}
	uid, mfa, err := ps.Finish(r.Context(), id, authstate.FinishRequest{
		CallbackURL: a.providerCallbackURL(r, id), State: st.S, Data: st.D, Query: q,
	})
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	step, ok := a.deps.Auth.(PasswordStep)
	if !ok {
		a.providerFailed(w, r, next, errors.New("sessions are not available"))
		return
	}
	via := authstate.Via{Method: authstate.MethodProvider, Provider: id}
	if !mfa && a.deps.TwoFactor != nil && len(a.deps.PendingKey) != 0 {
		has, err := a.deps.TwoFactor.HasAnyFactor(r.Context(), uid)
		if err != nil {
			// Never a session when we cannot tell.
			a.providerFailed(w, r, next, err)
			return
		}
		if has {
			p, err := twofactor.NewPending(uid, next, time.Now())
			if err != nil {
				a.providerFailed(w, r, next, err)
				return
			}
			p.Via = via.String()
			http.SetCookie(w, a.pendingCookie(r, a.deps.PendingKey.Seal(p), int(twofactor.PendingTTL/time.Second)))
			a.renderTwoStep(w, r, http.StatusOK, p, false, nil)
			return
		}
	}
	token, err := step.IssueVerifiedSession(r.Context(), uid, via, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.providerFailed(w, r, next, err)
		return
	}
	a.finishSignIn(w, r, token, next)
}

// providerFailed answers every provider sign-in problem the same way: the
// neutral message, with the cause only in the log (never an identifier).
func (a *Admin) providerFailed(w http.ResponseWriter, r *http.Request, next string, err error) {
	lvl := a.logger().Info
	if !errors.Is(err, auth.ErrInvalidCredentials) && !errors.Is(err, auth.ErrAccountLocked) {
		lvl = a.logger().Warn
	}
	lvl("provider sign-in refused", "ip", middleware.GetClientIP(r), "reason", err.Error())
	v := a.NewView(w, r, "Sign in")
	v.Data = loginData{Next: next, DemoLogin: a.demoLoginData(), Passkeys: a.passkeysOn(), Providers: a.providerButtons(next),
		Status: &Flash{Kind: "error", Message: providerMessage}}
	a.Render(w, r, http.StatusUnprocessableEntity, "auth/login", v)
}
