package spam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

func testVerifier(t *testing.T, h http.HandlerFunc, timeout time.Duration) *HTTPVerifier {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := safefetch.NewClient(safefetch.ClientOptions{Timeout: timeout, AllowPrivate: true})
	return NewHTTPVerifier(c, map[string]string{
		ProviderTurnstile: srv.URL, ProviderRecaptcha: srv.URL, ProviderHCaptcha: srv.URL,
	})
}

func reply(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(v) }
}

func TestVerifySendsFormAndAccepts(t *testing.T) {
	var got map[string]string
	v := testVerifier(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = map[string]string{"secret": r.Form.Get("secret"), "response": r.Form.Get("response"), "remoteip": r.Form.Get("remoteip")}
		w.Write([]byte(`{"success":true}`))
	}, time.Second)
	res, err := v.Verify(context.Background(), ProviderTurnstile, "sec", "", "tok", "203.0.113.9")
	if err != nil || !res.OK {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got["secret"] != "sec" || got["response"] != "tok" || got["remoteip"] != "203.0.113.9" {
		t.Fatalf("form = %v", got)
	}
	v.Verify(context.Background(), ProviderTurnstile, "sec", "", "tok", "not-an-ip")
	if got["remoteip"] != "" {
		t.Fatal("invalid ip was forwarded")
	}
}

func TestVerifyFailCodes(t *testing.T) {
	v := testVerifier(t, reply(map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}), time.Second)
	res, err := v.Verify(context.Background(), ProviderHCaptcha, "s", "", "t", "")
	if err != nil || res.OK || len(res.Codes) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestVerifyRecaptchaScoreAndAction(t *testing.T) {
	cases := []struct {
		score  float64
		action string
		ok     bool
	}{{0.9, "hm_form", true}, {0.5, "hm_form", true}, {0.4, "hm_form", false}, {0.9, "other", false}}
	for _, c := range cases {
		v := testVerifier(t, reply(map[string]any{"success": true, "score": c.score, "action": c.action}), time.Second)
		res, err := v.Verify(context.Background(), ProviderRecaptcha, "s", "", "t", "")
		if err != nil || res.OK != c.ok {
			t.Errorf("score %v action %q: ok=%v err=%v", c.score, c.action, res.OK, err)
		}
	}
	// The score rule is reCAPTCHA only.
	v := testVerifier(t, reply(map[string]any{"success": true, "score": 0.1}), time.Second)
	if res, _ := v.Verify(context.Background(), ProviderTurnstile, "s", "", "t", ""); !res.OK {
		t.Fatal("turnstile should ignore score")
	}
}

func TestVerifyErrors(t *testing.T) {
	const secret = "TOPSECRET-VALUE"
	cases := map[string]http.HandlerFunc{
		"http 500": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"bad json": func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) },
		"timeout":  func(http.ResponseWriter, *http.Request) { time.Sleep(300 * time.Millisecond) },
	}
	for name, h := range cases {
		v := testVerifier(t, h, 100*time.Millisecond)
		_, err := v.Verify(context.Background(), ProviderTurnstile, secret, "", "tok", "")
		if err == nil {
			t.Errorf("%s: no error", name)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error leaks secret: %v", name, err)
		}
	}
	if _, err := testVerifier(t, reply(nil), time.Second).Verify(context.Background(), "none", "s", "", "t", ""); err == nil {
		t.Error("unknown provider accepted")
	}
}

func TestVerifyBodyCapped(t *testing.T) {
	v := testVerifier(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":true,"pad":"` + strings.Repeat("a", 70<<10) + `"}`))
	}, time.Second)
	if _, err := v.Verify(context.Background(), ProviderTurnstile, "s", "", "t", ""); err == nil {
		t.Fatal("oversized body was accepted")
	}
}

func TestTestSecret(t *testing.T) {
	cases := []struct {
		name  string
		body  map[string]any
		valid bool
		want  string
	}{
		{"bad secret", map[string]any{"success": false, "error-codes": []string{"invalid-input-secret"}}, false, "not valid"},
		{"missing secret", map[string]any{"success": false, "error-codes": []string{"missing-input-secret"}}, false, "not valid"},
		{"good secret bad token", map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}, true, "accepted"},
		{"always pass key", map[string]any{"success": true}, true, "accepted"},
		{"inconclusive", map[string]any{"success": false, "error-codes": []string{"something-else"}}, false, "Could not verify"},
	}
	for _, c := range cases {
		v := testVerifier(t, reply(c.body), time.Second)
		valid, detail, err := v.TestSecret(context.Background(), ProviderTurnstile, "s", "")
		if err != nil || valid != c.valid || !strings.Contains(detail, c.want) {
			t.Errorf("%s: valid=%v detail=%q err=%v", c.name, valid, detail, err)
		}
	}
	v := testVerifier(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, time.Second)
	if _, _, err := v.TestSecret(context.Background(), ProviderTurnstile, "s", ""); err == nil {
		t.Error("outage should return an error")
	}
}

func TestVerifyHCaptchaSendsSiteKey(t *testing.T) {
	var got map[string]string
	v := testVerifier(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = map[string]string{"sitekey": r.Form.Get("sitekey")}
		w.Write([]byte(`{"success":true}`))
	}, time.Second)
	if _, err := v.Verify(context.Background(), ProviderHCaptcha, "s", "site-123", "t", ""); err != nil {
		t.Fatal(err)
	}
	if got["sitekey"] != "site-123" {
		t.Fatalf("hCaptcha sitekey = %q", got["sitekey"])
	}
	v.Verify(context.Background(), ProviderTurnstile, "s", "site-123", "t", "")
	if got["sitekey"] != "" {
		t.Fatal("sitekey sent to a provider that does not take it")
	}
}

func TestTestSecretHCaptchaSiteKeyMismatch(t *testing.T) {
	var sent string
	v := testVerifier(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sent = r.Form.Get("sitekey")
		w.Write([]byte(`{"success":false,"error-codes":["sitekey-secret-mismatch"]}`))
	}, time.Second)
	valid, detail, err := v.TestSecret(context.Background(), ProviderHCaptcha, "s", "site-123")
	if err != nil || valid || !strings.Contains(detail, "different accounts") {
		t.Fatalf("valid=%v detail=%q err=%v", valid, detail, err)
	}
	if sent != "site-123" {
		t.Fatalf("sitekey sent = %q", sent)
	}
}
