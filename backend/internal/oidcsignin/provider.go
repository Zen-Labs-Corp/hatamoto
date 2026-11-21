package oidcsignin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/jsonx"
)

const (
	settingsTTL  = 15 * time.Second
	discoveryTTL = time.Hour
	fetchTimeout = 10 * time.Second
)

// SettingsSource reads the saved settings (a *Store fits).
type SettingsSource interface {
	Get(ctx context.Context) (Settings, bool, error)
}

// Runtime is what the provider needs at serve time. Until SetRuntime is
// called the provider is not available.
type Runtime struct {
	Settings  SettingsSource
	SecretKey []byte
	Client    *http.Client                                   // outbound client for discovery, keys and token calls
	Audit     func(ctx context.Context, ev auditstate.Event) // nil = no audit
	Now       func() time.Time                               // nil = time.Now
}

func (rt *Runtime) now() time.Time {
	if rt.Now != nil {
		return rt.Now()
	}
	return time.Now()
}

type settingsEntry struct {
	s     Settings
	found bool
	err   error
	at    time.Time
}

type discEntry struct {
	p  *oidc.Provider
	at time.Time
}

// Provider is the OIDC sign-in provider. It keeps no per-sign-in state: state,
// nonce and PKCE verifier live in the data core seals into its cookie.
type Provider struct {
	rt atomic.Pointer[Runtime]

	setMu sync.Mutex
	set   *settingsEntry

	discMu sync.Mutex
	disc   map[string]discEntry
}

var def = &Provider{}

// Default is the process-wide provider that the app registers.
func Default() *Provider { return def }

// SetRuntime makes the provider usable and drops its caches.
func (p *Provider) SetRuntime(rt Runtime) {
	p.rt.Store(&rt)
	p.Invalidate()
}

// Invalidate drops the settings and discovery caches (call after a save).
func (p *Provider) Invalidate() {
	p.setMu.Lock()
	p.set = nil
	p.setMu.Unlock()
	p.discMu.Lock()
	p.disc = nil
	p.discMu.Unlock()
}

// settings returns the settings through a 15 s cache.
func (p *Provider) settings(ctx context.Context, rt *Runtime) (Settings, bool, error) {
	p.setMu.Lock()
	defer p.setMu.Unlock()
	if e := p.set; e != nil && rt.now().Sub(e.at) < settingsTTL {
		return e.s, e.found, e.err
	}
	s, found, err := rt.Settings.Get(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return s, found, err // one visitor's disconnect must not hide sign-in for others
	}
	p.set = &settingsEntry{s: s, found: found, err: err, at: rt.now()}
	return s, found, err
}

// active returns the settings when sign-in is usable: enabled, valid and with
// a readable secret. Any error fails closed.
func (p *Provider) active(ctx context.Context) (*Runtime, Settings, bool) {
	rt := p.rt.Load()
	if rt == nil || rt.Settings == nil {
		return nil, Settings{}, false
	}
	s, found, err := p.settings(ctx, rt)
	if err != nil || !found || !s.Enabled || s.Validate() != nil {
		return nil, Settings{}, false
	}
	if _, ok := s.Secret(rt.SecretKey); !ok {
		return nil, Settings{}, false
	}
	return rt, s, true
}

// Available reports whether the login page shows the provider, and its label.
// It never does network I/O and fails closed.
func (p *Provider) Available(ctx context.Context) (label string, ok bool) {
	_, s, ok := p.active(ctx)
	if !ok {
		return "", false
	}
	return s.DisplayLabel(), true
}

// startData is what Start hands to core and Finish gets back.
type startData struct {
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
	Issuer   string `json:"i"`
	ClientID string `json:"c"`
}

func oauthConfig(op *oidc.Provider, s Settings, secret, callbackURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.ClientID,
		ClientSecret: secret,
		Endpoint:     op.Endpoint(),
		RedirectURL:  callbackURL,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

// Start builds the authorization redirect.
func (p *Provider) Start(ctx context.Context, req authstate.StartRequest) (authstate.StartResult, error) {
	rt, s, ok := p.active(ctx)
	if !ok {
		return authstate.StartResult{}, refuse(reasonDisabled, nil)
	}
	secret, _ := s.Secret(rt.SecretKey)
	op, err := p.discover(ctx, rt, s.Issuer)
	if err != nil {
		return authstate.StartResult{}, refuse(reasonDiscoveryFailed, err)
	}
	nonce, err := randomToken()
	if err != nil {
		return authstate.StartResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	data, err := jsonx.Marshal(startData{Verifier: verifier, Nonce: nonce, Issuer: s.Issuer, ClientID: s.ClientID})
	if err != nil {
		return authstate.StartResult{}, err
	}
	cfg := oauthConfig(op, s, secret, req.CallbackURL)
	return authstate.StartResult{
		RedirectURL: cfg.AuthCodeURL(req.State, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)),
		Data:        string(data),
	}, nil
}

// Finish exchanges the code and verifies the ID token. Core has already
// checked the state.
func (p *Provider) Finish(ctx context.Context, req authstate.FinishRequest) (authstate.Identity, error) {
	rt, s, ok := p.active(ctx)
	if !ok {
		return authstate.Identity{}, refuse(reasonDisabled, nil)
	}
	var d startData
	if err := jsonx.Unmarshal([]byte(req.Data), &d); err != nil || d.Verifier == "" || d.Nonce == "" ||
		d.Issuer != s.Issuer || d.ClientID != s.ClientID {
		return authstate.Identity{}, refuse(reasonConfigChanged, nil)
	}
	if req.Query.Get("error") != "" {
		return authstate.Identity{}, refuse(reasonIDPError, nil)
	}
	code := req.Query.Get("code")
	if code == "" {
		return authstate.Identity{}, refuse(reasonNoCode, nil)
	}
	secret, _ := s.Secret(rt.SecretKey)
	op, err := p.discover(ctx, rt, s.Issuer)
	if err != nil {
		return authstate.Identity{}, refuse(reasonDiscoveryFailed, err)
	}

	xctx := context.WithValue(ctx, oauth2.HTTPClient, rt.Client)
	tok, err := oauthConfig(op, s, secret, req.CallbackURL).Exchange(xctx, code, oauth2.VerifierOption(d.Verifier))
	if err != nil {
		return authstate.Identity{}, refuse(reasonExchangeFailed, nil)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return authstate.Identity{}, refuse(reasonNoIDToken, nil)
	}
	idt, err := op.Verifier(&oidc.Config{ClientID: s.ClientID, Now: rt.now}).Verify(xctx, raw)
	if err != nil {
		var expired *oidc.TokenExpiredError
		if errors.As(err, &expired) {
			return authstate.Identity{}, refuse(reasonTokenExpired, nil)
		}
		return authstate.Identity{}, refuse(reasonTokenInvalid, nil)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(d.Nonce)) != 1 {
		return authstate.Identity{}, refuse(reasonNonceMismatch, nil)
	}
	var c struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		EntraVerified any    `json:"xms_edov"`
		Name          string `json:"name"`
		Azp           string `json:"azp"`
		HD            string `json:"hd"`
	}
	if err := idt.Claims(&c); err != nil {
		return authstate.Identity{}, refuse(reasonTokenInvalid, nil)
	}
	if (c.Azp != "" || len(idt.Audience) > 1) && c.Azp != s.ClientID {
		return authstate.Identity{}, refuse(reasonAzpMismatch, nil)
	}
	if !googleHostedDomainOK(idt.Issuer, c.HD, c.Email, s.AllowedDomains) {
		return authstate.Identity{}, refuse(reasonDomainNotAllowed, nil)
	}
	return authstate.Identity{
		Issuer:        idt.Issuer,
		Subject:       idt.Subject,
		Email:         strings.ToLower(strings.TrimSpace(c.Email)),
		EmailVerified: claimTrue(c.EmailVerified) || claimTrue(c.EntraVerified),
		Name:          strings.TrimSpace(c.Name),
		MFA:           false,
	}, nil
}

const googleIssuer = "https://accounts.google.com"

// googleHostedDomainOK is true for every issuer but Google. Google verifies
// any address a person registers, so for Google the hosted-domain claim (hd)
// must equal the email's domain and be an allowed domain.
func googleHostedDomainOK(issuer, hd, email string, allowed []string) bool {
	if issuer != googleIssuer {
		return true
	}
	hd = strings.ToLower(strings.TrimSpace(hd))
	at := strings.LastIndexByte(email, '@')
	return hd != "" && at >= 0 && strings.ToLower(email[at+1:]) == hd && domainAllowed("@"+hd, allowed)
}

// claimTrue accepts a JSON true or the string "true".
func claimTrue(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// discover returns the provider for issuer, cached for an hour. It is built
// on a context that outlives the request because go-oidc keeps it for later
// key-set refreshes.
func (p *Provider) discover(ctx context.Context, rt *Runtime, issuer string) (*oidc.Provider, error) {
	p.discMu.Lock()
	defer p.discMu.Unlock()
	if e, ok := p.disc[issuer]; ok && rt.now().Sub(e.at) < discoveryTTL {
		return e.p, nil
	}
	op, err := fetchProvider(ctx, rt, issuer)
	if err != nil {
		return nil, err
	}
	if p.disc == nil {
		p.disc = map[string]discEntry{}
	}
	p.disc[issuer] = discEntry{p: op, at: rt.now()}
	return op, nil
}

var errNoClient = errors.New("no outbound client configured")

// fetchProvider runs discovery, giving up after fetchTimeout or when ctx ends.
func fetchProvider(ctx context.Context, rt *Runtime, issuer string) (*oidc.Provider, error) {
	client := rt.Client
	if client == nil {
		return nil, errNoClient
	}
	type result struct {
		p   *oidc.Provider
		err error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), client), issuer)
		ch <- result{p, err}
	}()
	t := time.NewTimer(fetchTimeout)
	defer t.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.p, secureEndpoints(r.p)
	case <-t.C:
		return nil, errors.New("discovery timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// secureEndpoints refuses a discovery document whose authorization, token or
// key-set address is not https: the token call carries the client secret.
func secureEndpoints(op *oidc.Provider) error {
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := op.Claims(&meta); err != nil {
		return err
	}
	ep := op.Endpoint()
	for _, u := range []string{ep.AuthURL, ep.TokenURL, meta.JWKSURI} {
		if u != "" && !strings.HasPrefix(strings.ToLower(u), "https://") {
			return errors.New("discovery advertises an endpoint that is not https")
		}
	}
	return nil
}

// Check runs discovery and fetches the key set for s.Issuer. It is the
// "test" in Settings > Sign-in: it never signs anyone in and never sends the
// client secret. The error is for the owner who typed the issuer.
func (p *Provider) Check(ctx context.Context, s Settings) error {
	rt := p.rt.Load()
	if rt == nil {
		return errors.New("sign-in is not available in this process")
	}
	if s.Issuer == "" {
		return errors.New("issuer is required")
	}
	if err := validIssuer(s.Issuer); err != nil {
		return err
	}
	op, err := fetchProvider(ctx, rt, s.Issuer)
	if err != nil {
		return fmt.Errorf("discovery failed: %w", err)
	}
	ep := op.Endpoint()
	if ep.AuthURL == "" || ep.TokenURL == "" {
		return errors.New("discovery has no authorization or token endpoint")
	}
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := op.Claims(&meta); err != nil || meta.JWKSURI == "" {
		return errors.New("discovery has no key set address")
	}
	if _, err := url.Parse(meta.JWKSURI); err != nil {
		return errors.New("discovery key set address is not a URL")
	}
	client := rt.Client
	if client == nil {
		return errNoClient
	}
	rctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, meta.JWKSURI, nil)
	if err != nil {
		return errors.New("discovery key set address is not a URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("key set fetch failed: %w", err)
	}
	defer resp.Body.Close()
	var keys struct {
		Keys []any `json:"keys"`
	}
	if resp.StatusCode != http.StatusOK || jsonx.UnmarshalRead(io.LimitReader(resp.Body, 1<<20), &keys) != nil || len(keys.Keys) == 0 {
		return errors.New("the key set address did not return any keys")
	}
	return nil
}
