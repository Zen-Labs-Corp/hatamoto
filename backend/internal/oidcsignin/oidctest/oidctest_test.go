package oidctest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

const verifier = "v-0123456789012345678901234567890123456789012"

func get(t *testing.T, s *Server, path string, v any) {
	t.Helper()
	resp, err := s.Client().Get(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := jsonx.UnmarshalRead(resp.Body, v); err != nil {
		t.Fatal(err)
	}
}

// authorize walks /authorize and returns the code the redirect carries.
func authorize(t *testing.T, s *Server) string {
	t.Helper()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id": {"cid"}, "redirect_uri": {"https://rp.example.test/cb"}, "state": {"st"},
		"nonce": {"n1"}, "code_challenge": {b64(sum[:])}, "code_challenge_method": {"S256"},
	}
	c := *s.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(s.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("state") != "st" {
		t.Fatalf("redirect = %q, %v", resp.Header.Get("Location"), err)
	}
	return loc.Query().Get("code")
}

func exchange(t *testing.T, s *Server, secret, code, ver string) (int, map[string]any) {
	t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {ver},
		"client_id": {"cid"}, "client_secret": {secret}, "redirect_uri": {"https://rp.example.test/cb"},
	}
	resp, err := s.Client().PostForm(s.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = jsonx.UnmarshalRead(resp.Body, &out)
	return resp.StatusCode, out
}

func TestDiscovery(t *testing.T) {
	s := New(t, "cid", "secret")
	var d map[string]any
	get(t, s, "/.well-known/openid-configuration", &d)
	for k, want := range map[string]string{
		"issuer": s.URL, "authorization_endpoint": s.URL + "/authorize",
		"token_endpoint": s.URL + "/token", "jwks_uri": s.URL + "/jwks",
	} {
		if d[k] != want {
			t.Errorf("%s = %v, want %s", k, d[k], want)
		}
	}
	if algs, _ := d["id_token_signing_alg_values_supported"].([]any); len(algs) != 1 || algs[0] != "RS256" {
		t.Errorf("algs = %v", algs)
	}
}

func TestTokenRefusals(t *testing.T) {
	s := New(t, "cid", "secret")
	code := authorize(t, s)
	if status, _ := exchange(t, s, "wrong", code, verifier); status != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d", status)
	}
	if status, _ := exchange(t, s, "secret", code, "another-verifier"); status != http.StatusBadRequest {
		t.Errorf("wrong verifier = %d", status)
	}
	if status, _ := exchange(t, s, "secret", "nope", verifier); status != http.StatusBadRequest {
		t.Errorf("wrong code = %d", status)
	}
	if status, out := exchange(t, s, "secret", code, verifier); status != http.StatusOK || out["id_token"] == nil {
		t.Errorf("good exchange = %d %v", status, out)
	}
	s.OmitIDToken = true
	if _, out := exchange(t, s, "secret", code, verifier); out["id_token"] != nil {
		t.Error("id_token present despite OmitIDToken")
	}
}

func TestJWTVerifiesWithPublishedKey(t *testing.T) {
	s := New(t, "cid", "secret")
	code := authorize(t, s)
	_, out := exchange(t, s, "secret", code, verifier)
	parts := strings.Split(out["id_token"].(string), ".")
	if len(parts) != 3 {
		t.Fatalf("token = %v", out["id_token"])
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	get(t, s, "/jwks", &jwks)
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = jsonx.Unmarshal(raw, &hdr)
	if hdr.Alg != "RS256" || len(jwks.Keys) != 1 || jwks.Keys[0].Kid != hdr.Kid {
		t.Fatalf("header %+v, keys %+v", hdr, jwks.Keys)
	}
	n, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].N)
	e, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].E)
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	raw, _ = base64.RawURLEncoding.DecodeString(parts[1])
	_ = jsonx.Unmarshal(raw, &claims)
	if claims["iss"] != s.URL || claims["aud"] != "cid" || claims["nonce"] != "n1" || claims["sub"] != "sub-1" {
		t.Fatalf("claims = %v", claims)
	}

	s.RotateKey()
	var after struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	get(t, s, "/jwks", &after)
	if after.Keys[0].Kid == hdr.Kid {
		t.Fatal("RotateKey kept the key ID")
	}
}
