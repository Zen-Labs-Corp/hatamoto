package oidcsignin

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Wrong STATE is checked by core before Finish (the callback route and the
// bridge tests cover it), so it has no row here.
func TestFinishRefuses(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	claims := func(k string, v any) func(map[string]any) map[string]any {
		return func(c map[string]any) map[string]any { c[k] = v; return c }
	}
	tests := []struct {
		name   string
		setup  func(r *rig)
		mutate func(r *rig, q url.Values, data *string)
		want   string
	}{
		{name: "wrong nonce", setup: func(r *rig) { r.idp.Claims = claims("nonce", "x") }, want: reasonNonceMismatch},
		{name: "wrong issuer", setup: func(r *rig) { r.idp.Claims = claims("iss", "https://other.example") }, want: reasonTokenInvalid},
		{name: "wrong audience", setup: func(r *rig) { r.idp.Claims = claims("aud", "someone-else") }, want: reasonTokenInvalid},
		{name: "expired", setup: func(r *rig) {
			r.idp.Claims = claims("exp", time.Now().Add(-10*time.Minute).Unix())
		}, want: reasonTokenExpired},
		{name: "bad signature", setup: func(r *rig) {
			r.idp.Sign = func(in []byte) []byte {
				sum := sha256.Sum256(in)
				sig, _ := rsa.SignPKCS1v15(rand.Reader, otherKey, crypto.SHA256, sum[:])
				return sig
			}
		}, want: reasonTokenInvalid},
		{name: "azp mismatch", setup: func(r *rig) {
			r.idp.Claims = func(c map[string]any) map[string]any {
				c["aud"] = []string{testClientID, "other"}
				c["azp"] = "other"
				return c
			}
		}, want: reasonAzpMismatch},
		{name: "several audiences without azp", setup: func(r *rig) {
			r.idp.Claims = claims("aud", []string{testClientID, "other"})
		}, want: reasonAzpMismatch},
		{name: "idp error", mutate: func(_ *rig, q url.Values, _ *string) {
			q.Del("code")
			q.Set("error", "access_denied")
			q.Set("error_description", "ana@example.com said no")
		}, want: reasonIDPError},
		{name: "no code", mutate: func(_ *rig, q url.Values, _ *string) { q.Del("code") }, want: reasonNoCode},
		{name: "wrong verifier", mutate: func(_ *rig, _ url.Values, data *string) {
			var d startData
			_ = json.Unmarshal([]byte(*data), &d)
			d.Verifier = "another-verifier-another-verifier-another-verifier"
			b, _ := json.Marshal(d)
			*data = string(b)
		}, want: reasonExchangeFailed},
		{name: "unreadable data", mutate: func(_ *rig, _ url.Values, data *string) { *data = "not json" }, want: reasonConfigChanged},
		{name: "config changed", mutate: func(r *rig, _ url.Values, _ *string) {
			r.src.s.ClientID = "client-2"
			r.p.Invalidate()
		}, want: reasonConfigChanged},
		{name: "missing id_token", setup: func(r *rig) { r.idp.OmitIDToken = true }, want: reasonNoIDToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			if tt.setup != nil {
				tt.setup(r)
			}
			_, fin := r.start(t)
			if tt.mutate != nil {
				tt.mutate(r, fin.Query, &fin.Data)
			}
			_, err := r.p.Finish(context.Background(), fin)
			if got := ReasonOf(err); got != tt.want {
				t.Fatalf("reason = %q (%v), want %q", got, err, tt.want)
			}
			if _, ok := err.(*Refusal); !ok {
				t.Fatalf("error is %T, want *Refusal", err)
			}
			text := err.Error()
			for _, leak := range []string{"@", "ana", testSecret, "access-token", "eyJ"} {
				if strings.Contains(text, leak) {
					t.Errorf("error %q contains %q", text, leak)
				}
			}
		})
	}
}
