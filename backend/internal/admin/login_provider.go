package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
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

// providerFor returns the provider sign-in when id is registered and available.
func (a *Admin) providerFor(ctx context.Context, id string) (ProviderSignIn, bool) {
	ps := a.deps.ProviderSignIn
	if ps == nil {
		return nil, false
	}
	for _, p := range ps.Providers(ctx) {
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
	ps, ok := a.providerFor(r.Context(), id)
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
	value := base64.RawURLEncoding.EncodeToString(sealed)
	// What counts is the stored size: JSON escaping, sealing and base64 grow
	// the data, and a browser drops a cookie over its limit without a word.
	if len(value) > maxProviderCookieValue {
		a.providerFailed(w, r, next, errors.New("provider state does not fit in a cookie"))
		return
	}
	http.SetCookie(w, a.providerCookie(r, id, value, int(providerStateTTL/time.Second)))
	http.Redirect(w, r, redirect, http.StatusFound)
}

// maxProviderCookieValue keeps the state cookie (name, path and attributes
// included) under the 4096 bytes browsers keep.
const maxProviderCookieValue = 3800

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
	ps, ok := a.providerFor(r.Context(), id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The state is single use: the cookie goes whatever happens next.
	http.SetCookie(w, a.providerCookie(r, id, "", -1))
	st, ok := a.openProviderState(r, id)
	if !ok {
		a.callbackFailed(w, r, id, "", "state", audit.ResultDenied, errors.New("missing, forged, expired or reused state"))
		return
	}
	a.spentProvider.add(st.I, st.E, time.Now())
	next := SafeNextFor(a.adminPath, st.N)
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.S)) != 1 {
		a.callbackFailed(w, r, id, next, "state", audit.ResultDenied, errors.New("state mismatch"))
		return
	}
	uid, mfa, err := ps.Finish(r.Context(), id, authstate.FinishRequest{
		CallbackURL: a.providerCallbackURL(r, id), State: st.S, Data: st.D, Query: q,
	})
	if err != nil {
		reason, result := providerReason("provider", err)
		a.callbackFailed(w, r, id, next, reason, result, err)
		return
	}
	step, ok := a.deps.Auth.(PasswordStep)
	if !ok {
		a.callbackFailed(w, r, id, next, "session", audit.ResultError, errors.New("sessions are not available"))
		return
	}
	via := authstate.Via{Method: authstate.MethodProvider, Provider: id}
	// Refuse an ended, locked or policy-refused user now, before any prompt
	// or pending cookie: a refused user gets no code attempts.
	if err := step.Admit(r.Context(), uid, via); err != nil {
		a.callbackFailed(w, r, id, next, "access", audit.ResultDenied, err)
		return
	}
	if !mfa {
		// Without a way to ask for the second step there is no session: we
		// cannot tell whether the user has a factor (as the password path).
		if a.deps.TwoFactor == nil || len(a.deps.PendingKey) == 0 {
			a.callbackFailed(w, r, id, next, "second_step_unavailable", audit.ResultError, errors.New("the second step is not available"))
			return
		}
		has, err := a.deps.TwoFactor.HasAnyFactor(r.Context(), uid)
		if err != nil {
			// Never a session when we cannot tell.
			a.callbackFailed(w, r, id, next, "second_step_unavailable", audit.ResultError, err)
			return
		}
		if has {
			p, err := twofactor.NewPending(uid, next, time.Now())
			if err != nil {
				a.callbackFailed(w, r, id, next, "second_step_unavailable", audit.ResultError, err)
				return
			}
			p.Via = via.String()
			http.SetCookie(w, a.pendingCookie(r, a.deps.PendingKey.Seal(p), int(twofactor.PendingTTL/time.Second)))
			a.auditProvider(r, "session.sign_in_second_step", id, audit.ResultOK, "", uid)
			a.renderTwoStep(w, r, http.StatusOK, p, false, nil)
			return
		}
	}
	token, _, err := step.IssueVerifiedSession(r.Context(), uid, via, r.UserAgent(), middleware.GetClientIP(r))
	if err != nil {
		a.callbackFailed(w, r, id, next, "session", audit.ResultError, err)
		return
	}
	a.auditProvider(r, "session.sign_in", id, audit.ResultOK, "", uid)
	a.continueSignIn(w, r, token, next)
}

// continueSignIn sets the session cookie and answers 200 with a page that
// moves on by itself, instead of redirecting. The provider's redirect back
// starts a cross-site navigation, and a redirect inside it is still cross-site,
// so the browser would not send the SameSite=Strict session cookie on it. The
// page's own navigation (meta refresh or the link) is same-site.
func (a *Admin) continueSignIn(w http.ResponseWriter, r *http.Request, token, next string) {
	a.setSession(w, r, token)
	if next == "" {
		next = a.landing()
	}
	v := a.NewView(w, r, "Signed in")
	v.ContinueTo = next
	v.Data = struct{ Next string }{next}
	a.Render(w, r, http.StatusOK, "auth/continue", v)
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
	v.Data = loginData{Next: next, DemoLogin: a.demoLoginData(), Passkeys: a.passkeysOn(), Providers: a.providerButtons(r.Context(), next),
		Status: &Flash{Kind: "error", Message: providerMessage}}
	a.Render(w, r, http.StatusUnprocessableEntity, "auth/login", v)
}

// auditReasoner is implemented by a provider error that carries a stable,
// non-identifying reason code for the audit log.
type auditReasoner interface{ AuditReason() string }

var reasonRE = regexp.MustCompile(`^[a-z_]{1,32}$`)

// providerReason is the audit reason of a Finish error and its result: the
// error's own code (a refusal, "denied") when it has a valid one, else stage
// ("error": not a refusal by the provider).
func providerReason(stage string, err error) (reason, result string) {
	var r auditReasoner
	if errors.As(err, &r) && reasonRE.MatchString(r.AuditReason()) {
		return r.AuditReason(), audit.ResultDenied
	}
	return stage, audit.ResultError
}

// auditProvider records one provider sign-in outcome. The detail holds only
// the method and a reason code, never an error text, address or token. The
// actor is set only for a successful step (uid is not Nil).
func (a *Admin) auditProvider(r *http.Request, action, id, result, reason string, uid uuid.UUID) {
	if a.deps.Audit == nil {
		return
	}
	detail := map[string]any{"method": "provider:" + id}
	if reason != "" {
		detail["reason"] = reason
	}
	e := AuditEvent{
		Action: action, Target: "session", Detail: detail, Result: result,
		IP: middleware.GetClientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-ID"),
	}
	if uid != uuid.Nil {
		e.ActorID = uid.String()
		if a.deps.Users != nil {
			if u, err := a.deps.Users.GetByID(r.Context(), uid); err == nil && u != nil {
				e.Actor = u.Email
			}
		}
	}
	a.deps.Audit(r.Context(), e)
}

// callbackFailed audits a refused callback once, then answers neutrally.
func (a *Admin) callbackFailed(w http.ResponseWriter, r *http.Request, id, next, reason, result string, err error) {
	a.auditProvider(r, "session.sign_in_failed", id, result, reason, uuid.Nil)
	a.providerFailed(w, r, next, err)
}
