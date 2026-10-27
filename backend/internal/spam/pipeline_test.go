package spam

import (
	"context"
	"errors"
	"testing"
	"time"
)

type staticSettings struct{ s Settings }

func (r staticSettings) Get(context.Context) (Settings, error) { return r.s, nil }

type stubVerifier struct {
	res   Result
	err   error
	calls int
}

func (v *stubVerifier) Verify(context.Context, string, string, string, string, string) (Result, error) {
	v.calls++
	return v.res, v.err
}

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func newPipe(t *testing.T, s Settings, v Verifier) *Pipeline {
	t.Helper()
	sk := SecretKey("server-secret")
	if s.Provider != ProviderNone {
		sealed, err := SealSecret(sk, "provider-secret")
		if err != nil {
			t.Fatal(err)
		}
		s.SecretSealed = sealed
	}
	now := t0
	return &Pipeline{Settings: staticSettings{s}, Verifier: v, SecretKey: sk,
		TimingKey: TimingKey("server-secret"), Now: func() time.Time { return now }}
}

func TestCheckCaptcha(t *testing.T) {
	t.Parallel()
	set := Defaults()
	set.Provider = ProviderTurnstile
	cases := []struct {
		name string
		v    *stubVerifier
		tok  string
		want Verdict
	}{
		{"no token", &stubVerifier{res: Result{OK: true}}, "", CaptchaFail},
		{"rejected", &stubVerifier{res: Result{OK: false}}, "tok", CaptchaFail},
		{"provider down", &stubVerifier{err: errors.New("down")}, "tok", CaptchaFail},
		{"passed", &stubVerifier{res: Result{OK: true}}, "tok", Allow},
	}
	for _, c := range cases {
		p := newPipe(t, set, c.v)
		got, _ := p.Check(context.Background(), Input{Slug: "c", CaptchaRequired: true, Captcha: c.tok})
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCheckCaptchaSkipped(t *testing.T) {
	t.Parallel()
	v := &stubVerifier{res: Result{OK: false}}
	set := Defaults()
	set.Provider = ProviderTurnstile
	// Switch off.
	if got, _ := newPipe(t, set, v).Check(context.Background(), Input{Slug: "c"}); got != Allow {
		t.Errorf("switch off: %v", got)
	}
	// No provider.
	if got, _ := newPipe(t, Defaults(), v).Check(context.Background(), Input{Slug: "c", CaptchaRequired: true}); got != Allow {
		t.Errorf("no provider: %v", got)
	}
	// Secret that cannot be opened (server secret rotated).
	p := newPipe(t, set, v)
	p.SecretKey = SecretKey("rotated")
	if got, _ := p.Check(context.Background(), Input{Slug: "c", CaptchaRequired: true}); got != Allow {
		t.Errorf("unreadable secret: %v", got)
	}
	if v.calls != 0 {
		t.Errorf("verifier called %d times", v.calls)
	}
}

func TestCheckTiming(t *testing.T) {
	t.Parallel()
	p := newPipe(t, Defaults(), nil)
	fast := IssueToken(p.TimingKey, "c", t0.Add(-time.Second))
	slow := IssueToken(p.TimingKey, "c", t0.Add(-10*time.Second))
	for _, c := range []struct {
		name string
		tok  string
		want Verdict
	}{
		{"fast", fast, Spam},
		{"slow", slow, Allow},
		{"missing", "", Allow},
		{"forged", "v1.1.AAAA", Allow},
		{"other form", IssueToken(p.TimingKey, "other", t0.Add(-time.Second)), Allow},
	} {
		if got, _ := p.Check(context.Background(), Input{Slug: "c", Timing: c.tok}); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestCheckRulesAndAction(t *testing.T) {
	t.Parallel()
	in := Input{Slug: "c", Data: map[string]interface{}{"m": "http://a http://b http://c"}}
	if got, why := newPipe(t, Defaults(), nil).Check(context.Background(), in); got != Spam || why != ReasonLinks {
		t.Errorf("spam mode: %v %q", got, why)
	}
	set := Defaults()
	set.Action = ActionRefuse
	if got, _ := newPipe(t, set, nil).Check(context.Background(), in); got != Refuse {
		t.Errorf("refuse mode: %v", got)
	}
	fast := Input{Slug: "c", Timing: IssueToken(TimingKey("server-secret"), "c", t0)}
	if got, _ := newPipe(t, set, nil).Check(context.Background(), fast); got != Refuse {
		t.Errorf("refuse on timing: %v", got)
	}
}
