// Package oidctest is a fake OpenID Connect provider for tests. Tests only:
// nothing in the server imports it.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

const authCode = "c1"

// Server is a TLS fake identity provider. Set the exported hooks before the
// sign-in under test starts; read LastAuth and LastToken after it ends.
type Server struct {
	*httptest.Server // TLS; Issuer() is its URL
	Key              *rsa.PrivateKey
	KID              string

	// Claims edits the ID token claims (iss, aud, exp, iat, nonce, azp, sub,
	// email, email_verified, name) before signing. Nil keeps the defaults.
	Claims func(base map[string]any) map[string]any
	// Sign signs header.payload (both base64url); nil signs RS256 with Key.
	Sign func(signingInput []byte) []byte
	// OmitIDToken makes the token endpoint answer without an id_token.
	OmitIDToken bool
	// Now is the clock for iat/exp; nil is time.Now.
	Now func() time.Time

	LastAuth  url.Values // query of the last /authorize
	LastToken url.Values // form of the last /token

	clientID, clientSecret string

	mu        sync.Mutex
	challenge string
	nonce     string
}

// New starts a fake provider that accepts one client.
func New(t testing.TB, clientID, clientSecret string) *Server {
	t.Helper()
	s := &Server{clientID: clientID, clientSecret: clientSecret}
	s.RotateKey()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("/jwks", s.jwks)
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/token", s.token)
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Server.Close)
	return s
}

// Issuer is the issuer identifier, equal to the server URL.
func (s *Server) Issuer() string { return s.URL }

// Client returns an HTTP client that trusts the server's certificate.
func (s *Server) Client() *http.Client { return s.Server.Client() }

// RotateKey replaces the signing key and its key ID.
func (s *Server) RotateKey() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	s.mu.Lock()
	s.Key, s.KID = key, "kid-"+b64(b[:])
	s.mu.Unlock()
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write(b)
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                s.URL,
		"authorization_endpoint":                s.URL + "/authorize",
		"token_endpoint":                        s.URL + "/token",
		"jwks_uri":                              s.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	pub, kid := s.Key.PublicKey, s.KID
	s.mu.Unlock()
	writeJSON(w, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.LastAuth = q
	s.challenge = q.Get("code_challenge")
	s.nonce = q.Get("nonce")
	s.mu.Unlock()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	rq := redirect.Query()
	rq.Set("code", authCode)
	rq.Set("state", q.Get("state"))
	redirect.RawQuery = rq.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	s.mu.Lock()
	s.LastToken = r.PostForm
	challenge, nonce := s.challenge, s.nonce
	s.mu.Unlock()

	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	} else if u, err := url.QueryUnescape(id); err == nil {
		id = u
		if p, err := url.QueryUnescape(secret); err == nil {
			secret = p
		}
	}
	if id != s.clientID || subtle.ConstantTimeCompare([]byte(secret), []byte(s.clientSecret)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		tokenError(w, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != authCode {
		tokenError(w, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if challenge == "" || b64(sum[:]) != challenge {
		tokenError(w, "invalid_grant")
		return
	}
	resp := map[string]any{"access_token": "access-token", "token_type": "Bearer"}
	if !s.OmitIDToken {
		resp["id_token"] = s.idToken(nonce)
	}
	writeJSON(w, resp)
}

func tokenError(w http.ResponseWriter, code string) {
	if w.Header().Get("Content-Type") == "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	writeJSON(w, map[string]string{"error": code})
}

// idToken builds and signs the ID token for nonce.
func (s *Server) idToken(nonce string) string {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	claims := map[string]any{
		"iss": s.URL, "aud": s.clientID, "sub": "sub-1",
		"email": "ana@example.com", "email_verified": true,
		"iat": now().Unix(), "exp": now().Add(5 * time.Minute).Unix(), "nonce": nonce,
	}
	if s.Claims != nil {
		claims = s.Claims(claims)
	}
	s.mu.Lock()
	key, kid := s.Key, s.KID
	s.mu.Unlock()
	header, _ := jsonx.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload, _ := jsonx.Marshal(claims)
	input := b64(header) + "." + b64(payload)
	if s.Sign != nil {
		return input + "." + b64(s.Sign([]byte(input)))
	}
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return input + "." + b64(sig)
}
