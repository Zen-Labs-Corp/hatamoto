package twofactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

const (
	ceremonyTTL      = 2 * time.Minute
	purposeRegister  = "register"
	purposeLogin     = "login"
	maxPasskeyName   = 80
	defaultKeyName   = "Passkey"
	passkeyDisplayRP = "Hatamoto"
)

// ErrPasskeyFailed covers every refused passkey ceremony: unknown, spent or
// expired challenge, bad signature, wrong origin or RP ID, missing user
// verification, cloned counter, unknown credential, locked account. Callers
// show one neutral message.
var ErrPasskeyFailed = errors.New("twofactor: passkey ceremony failed")

// errLocked is ErrPasskeyFailed for a locked account (no further lock penalty).
var errLocked = fmt.Errorf("%w: account locked", ErrPasskeyFailed)

// ErrNoRelyingParty is returned when the site address is not configured, so
// there is no RP ID to bind passkeys to.
var ErrNoRelyingParty = errors.New("twofactor: no relying party configured")

// RP is the relying party passkeys are bound to: the host passkeys are scoped
// to and the origins pages may run on.
type RP struct {
	ID      string
	Name    string
	Origins []string
}

// Locker is the per-user lock Passkeys consults. Service implements it.
type Locker interface {
	Locked(ctx context.Context, uid uuid.UUID) (bool, error)
	Fail(ctx context.Context, uid uuid.UUID)
	Succeed(ctx context.Context, uid uuid.UUID)
}

// Passkeys runs WebAuthn registration and sign-in ceremonies on top of
// go-webauthn. Every challenge is a single-use row in the repository, so it
// works across replicas and a replayed response finds nothing.
type Passkeys struct {
	rp   func(context.Context) (RP, error)
	repo Repository
	lock Locker
	now  func() time.Time
}

// NewPasskeys builds the wrapper. rp is read on every ceremony, so a changed
// site address takes effect without a restart. now may be nil.
func NewPasskeys(rp func(context.Context) (RP, error), repo Repository, lock Locker, now func() time.Time) *Passkeys {
	if now == nil {
		now = time.Now
	}
	return &Passkeys{rp: rp, repo: repo, lock: lock, now: now}
}

func (p *Passkeys) webauthn(ctx context.Context) (*webauthn.WebAuthn, error) {
	rp, err := p.rp(ctx)
	if err != nil {
		return nil, err
	}
	if rp.ID == "" || len(rp.Origins) == 0 {
		return nil, ErrNoRelyingParty
	}
	name := rp.Name
	if name == "" {
		name = passkeyDisplayRP
	}
	yes := true
	return webauthn.New(&webauthn.Config{
		RPID:                  rp.ID,
		RPDisplayName:         name,
		RPOrigins:             rp.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: &yes,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
		},
	})
}

// waUser is the go-webauthn view of a user: the handle is the user id bytes.
type waUser struct {
	id          uuid.UUID
	name        string
	displayName string
	creds       []webauthn.Credential
}

func (u waUser) WebAuthnID() []byte                         { return u.id[:] }
func (u waUser) WebAuthnName() string                       { return u.name }
func (u waUser) WebAuthnDisplayName() string                { return u.displayName }
func (u waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (p *Passkeys) user(ctx context.Context, uid uuid.UUID, name, display string) (waUser, error) {
	list, err := p.repo.ListPasskeys(ctx, uid)
	if err != nil {
		return waUser{}, err
	}
	u := waUser{id: uid, name: name, displayName: display}
	for _, k := range list {
		var c webauthn.Credential
		if err := json.Unmarshal(k.Credential, &c); err != nil {
			return waUser{}, err
		}
		u.creds = append(u.creds, c)
	}
	return u, nil
}

func (p *Passkeys) store(ctx context.Context, uid *uuid.UUID, purpose string, s *webauthn.SessionData) (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	id := uuid.New()
	if err := p.repo.PutCeremony(ctx, Ceremony{ID: id, UserID: uid, Purpose: purpose, Data: data, Expires: p.now().Add(ceremonyTTL)}); err != nil {
		return "", err
	}
	return id.String(), nil
}

// take spends the ceremony: a second call with the same id fails.
func (p *Passkeys) take(ctx context.Context, ceremonyID, purpose string) (*Ceremony, webauthn.SessionData, error) {
	var s webauthn.SessionData
	id, err := uuid.Parse(ceremonyID)
	if err != nil {
		return nil, s, ErrPasskeyFailed
	}
	c, err := p.repo.TakeCeremony(ctx, id, purpose, p.now())
	if errors.Is(err, ErrNotFound) {
		return nil, s, ErrPasskeyFailed
	}
	if err != nil {
		return nil, s, err
	}
	if err := json.Unmarshal(c.Data, &s); err != nil {
		return nil, s, ErrPasskeyFailed
	}
	if s.UserVerification != protocol.VerificationRequired {
		return nil, s, ErrPasskeyFailed
	}
	return c, s, nil
}

func descriptors(u waUser) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for _, c := range u.creds {
		out = append(out, c.Descriptor())
	}
	return out
}

// BeginRegistration starts adding a passkey for uid. It returns the options
// for navigator.credentials.create and the id of the single-use ceremony.
func (p *Passkeys) BeginRegistration(ctx context.Context, uid uuid.UUID, name, displayName string) (json.RawMessage, string, error) {
	wa, err := p.webauthn(ctx)
	if err != nil {
		return nil, "", err
	}
	u, err := p.user(ctx, uid, name, displayName)
	if err != nil {
		return nil, "", err
	}
	creation, session, err := wa.BeginRegistration(u, webauthn.WithExclusions(descriptors(u)))
	if err != nil {
		return nil, "", err
	}
	id, err := p.store(ctx, &uid, purposeRegister, session)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(creation)
	return opts, id, err
}

// FinishRegistration verifies the authenticator's response and stores the new
// passkey under keyName (trimmed, at most 80 characters). It returns its id.
func (p *Passkeys) FinishRegistration(ctx context.Context, ceremonyID string, uid uuid.UUID, keyName string, r *http.Request) (uuid.UUID, error) {
	c, session, err := p.take(ctx, ceremonyID, purposeRegister)
	if err != nil {
		return uuid.Nil, err
	}
	if c.UserID == nil || *c.UserID != uid {
		return uuid.Nil, ErrPasskeyFailed
	}
	wa, err := p.webauthn(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	u, err := p.user(ctx, uid, "", "")
	if err != nil {
		return uuid.Nil, err
	}
	cred, err := wa.FinishRegistration(u, session, r)
	if err != nil || !cred.Flags.UserVerified {
		return uuid.Nil, ErrPasskeyFailed
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return uuid.Nil, err
	}
	return p.repo.AddPasskey(ctx, Passkey{UserID: uid, CredentialID: cred.ID, Credential: raw, Name: CleanPasskeyName(keyName)})
}

// CleanPasskeyName trims a user-entered name and caps it at 80 characters.
func CleanPasskeyName(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxPasskeyName {
		s = string([]rune(s)[:maxPasskeyName])
	}
	if s == "" {
		return defaultKeyName
	}
	return s
}

// BeginLogin starts a passkey sign-in. With a uid it is the second factor for
// that user (allow list); with nil it is a discoverable-credential sign-in.
func (p *Passkeys) BeginLogin(ctx context.Context, uid *uuid.UUID) (json.RawMessage, string, error) {
	wa, err := p.webauthn(ctx)
	if err != nil {
		return nil, "", err
	}
	var assertion *protocol.CredentialAssertion
	var session *webauthn.SessionData
	uv := webauthn.WithUserVerification(protocol.VerificationRequired)
	if uid == nil {
		assertion, session, err = wa.BeginDiscoverableLogin(uv)
	} else {
		u, uerr := p.user(ctx, *uid, "", "")
		if uerr != nil {
			return nil, "", uerr
		}
		if len(u.creds) == 0 {
			return nil, "", ErrPasskeyFailed
		}
		assertion, session, err = wa.BeginLogin(u, uv)
	}
	if err != nil {
		return nil, "", err
	}
	id, err := p.store(ctx, uid, purposeLogin, session)
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	return opts, id, err
}

// FinishLogin verifies an assertion and returns the user it proves. With a uid
// (second factor) the ceremony must belong to that user and wrong attempts
// count against the account lock; with nil (discoverable) the user comes from
// the credential. User verification is required, a non-increasing sign counter
// is refused, and a locked account is refused.
func (p *Passkeys) FinishLogin(ctx context.Context, ceremonyID string, uid *uuid.UUID, r *http.Request) (uuid.UUID, error) {
	got, err := p.finishLogin(ctx, ceremonyID, uid, r)
	if err != nil {
		// Only a failed attempt behind a proven password counts; a locked
		// account must not have its lock extended by more attempts.
		if uid != nil && errors.Is(err, ErrPasskeyFailed) && !errors.Is(err, errLocked) {
			p.lock.Fail(ctx, *uid)
		}
		return uuid.Nil, err
	}
	locked, lerr := p.lock.Locked(ctx, got)
	if lerr != nil {
		return uuid.Nil, lerr
	}
	if locked {
		return uuid.Nil, errLocked
	}
	p.lock.Succeed(ctx, got)
	return got, nil
}

func (p *Passkeys) finishLogin(ctx context.Context, ceremonyID string, uid *uuid.UUID, r *http.Request) (uuid.UUID, error) {
	if uid != nil {
		// A locked account is refused before spending the ceremony's crypto.
		if locked, err := p.lock.Locked(ctx, *uid); err != nil {
			return uuid.Nil, err
		} else if locked {
			return uuid.Nil, errLocked
		}
	}
	c, session, err := p.take(ctx, ceremonyID, purposeLogin)
	if err != nil {
		return uuid.Nil, err
	}
	if (uid == nil) != (c.UserID == nil) || (uid != nil && *c.UserID != *uid) {
		return uuid.Nil, ErrPasskeyFailed
	}
	wa, err := p.webauthn(ctx)
	if err != nil {
		return uuid.Nil, err
	}

	var cred *webauthn.Credential
	var owner waUser
	if uid != nil {
		owner, err = p.user(ctx, *uid, "", "")
		if err != nil {
			return uuid.Nil, err
		}
		cred, err = wa.FinishLogin(owner, session, r)
	} else {
		handler := func(rawID, userHandle []byte) (webauthn.User, error) {
			id, perr := uuid.FromBytes(userHandle)
			if perr != nil {
				return nil, ErrPasskeyFailed
			}
			k, kerr := p.repo.PasskeyByCredentialID(ctx, rawID)
			if kerr != nil || k.UserID != id {
				return nil, ErrPasskeyFailed
			}
			owner, kerr = p.user(ctx, id, "", "")
			return owner, kerr
		}
		_, cred, err = wa.FinishPasskeyLogin(handler, session, r)
	}
	if err != nil || cred == nil || !cred.Flags.UserVerified || cred.Authenticator.CloneWarning {
		return uuid.Nil, ErrPasskeyFailed
	}

	k, err := p.repo.PasskeyByCredentialID(ctx, cred.ID)
	if err != nil || k.UserID != owner.id {
		return uuid.Nil, ErrPasskeyFailed
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return uuid.Nil, err
	}
	if err := p.repo.UpdatePasskey(ctx, k.ID, raw, p.now()); err != nil {
		return uuid.Nil, err
	}
	return owner.id, nil
}
