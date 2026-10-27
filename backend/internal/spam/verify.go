package spam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/core/internal/safefetch"
)

const (
	verifyTimeout   = 5 * time.Second
	maxVerifyBody   = 64 << 10
	recaptchaMin    = 0.5
	RecaptchaAction = "hm_form"
	testToken       = "hatamoto-test-connection"
)

// DefaultEndpoints are the provider siteverify URLs.
var DefaultEndpoints = map[string]string{
	ProviderTurnstile: "https://challenges.cloudflare.com/turnstile/v0/siteverify",
	ProviderRecaptcha: "https://www.google.com/recaptcha/api/siteverify",
	ProviderHCaptcha:  "https://api.hcaptcha.com/siteverify",
}

// Result is a provider's verdict on one token.
type Result struct {
	OK    bool
	Codes []string // provider error codes, safe to show an admin
	Score float64  // reCAPTCHA v3 score, 0 for other providers
}

// Verifier checks a captcha token with the provider.
type Verifier interface {
	Verify(ctx context.Context, provider, secret, siteKey, token, remoteIP string) (Result, error)
}

// HTTPVerifier verifies tokens over HTTPS.
type HTTPVerifier struct {
	client    *http.Client
	endpoints map[string]string
}

// NewClient returns the HTTP client verification uses: SSRF-safe, 5s timeout, no redirects.
func NewClient() *http.Client {
	c := safefetch.NewClient(safefetch.ClientOptions{Timeout: verifyTimeout})
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// NewHTTPVerifier returns a verifier using c. endpoints overrides the provider
// URLs (tests); nil means DefaultEndpoints.
func NewHTTPVerifier(c *http.Client, endpoints map[string]string) *HTTPVerifier {
	if endpoints == nil {
		endpoints = DefaultEndpoints
	}
	return &HTTPVerifier{client: c, endpoints: endpoints}
}

type siteverifyReply struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Score      float64  `json:"score"`
	Action     string   `json:"action"`
}

// Verify posts the token to the provider. A transport error, a non-200 reply
// or an unreadable body is an error; a reply that rejects the token is a
// Result with OK false. Neither the secret nor the token is ever included in
// an error.
func (v *HTTPVerifier) Verify(ctx context.Context, provider, secret, siteKey, token, remoteIP string) (Result, error) {
	endpoint, ok := v.endpoints[provider]
	if !ok || endpoint == "" {
		return Result{}, fmt.Errorf("no verifier for provider %q", provider)
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	if provider == ProviderHCaptcha && siteKey != "" {
		// hCaptcha secrets are account-wide; sitekey ties the token to this site.
		form.Set("sitekey", siteKey)
	}
	if net.ParseIP(remoteIP) != nil {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, errors.New("build captcha request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		// The form body (secret, token) is not in err, but keep it out anyway.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return Result{}, errors.New("captcha provider timed out")
		}
		return Result{}, errors.New("captcha provider unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("captcha provider returned HTTP %d", resp.StatusCode)
	}
	var reply siteverifyReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxVerifyBody)).Decode(&reply); err != nil {
		return Result{}, errors.New("captcha provider reply unreadable")
	}
	res := Result{OK: reply.Success, Codes: reply.ErrorCodes, Score: reply.Score}
	if provider == ProviderRecaptcha && res.OK {
		res.OK = reply.Score >= recaptchaMin && reply.Action == RecaptchaAction
	}
	return res, nil
}

// TestSecret asks the provider about a dummy token, which tells a wrong secret
// (rejected as such) from a right one (only the token is rejected). For hCaptcha
// the site key is sent too, so a key and secret from different accounts show up. valid is
// false with a plain-sentence detail when the secret is wrong or the provider's
// reply cannot tell.
func (v *HTTPVerifier) TestSecret(ctx context.Context, provider, secret, siteKey string) (valid bool, detail string, err error) {
	res, err := v.Verify(ctx, provider, secret, siteKey, testToken, "")
	if err != nil {
		return false, "", err
	}
	if res.OK {
		return true, "The provider accepted the secret.", nil
	}
	for _, c := range res.Codes {
		if c == "sitekey-secret-mismatch" {
			return false, "The site key and the secret key belong to different accounts.", nil
		}
	}
	for _, c := range res.Codes {
		switch c {
		case "invalid-input-secret", "missing-input-secret", "invalid-or-missing-secret":
			return false, "The provider says this secret key is not valid.", nil
		}
	}
	for _, c := range res.Codes {
		switch c {
		case "invalid-input-response", "missing-input-response", "timeout-or-duplicate":
			return true, "The provider accepted the secret.", nil
		}
	}
	// Anything else is not conclusive.
	return false, "Could not verify the secret with the provider.", nil
}
