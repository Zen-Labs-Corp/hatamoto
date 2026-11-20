package spam

import (
	"context"
	"testing"
	"time"
)

// TestFormsSpamUnchanged pins the form outcomes across the subject-key and
// three-bucket change: Check returns exactly what it returned before and never Hold.
func TestFormsSpamUnchanged(t *testing.T) {
	t.Parallel()
	if Allow != 0 || Hold != CaptchaFail+1 {
		t.Fatalf("verdict values renumbered: Allow=%d Hold=%d CaptchaFail=%d", Allow, Hold, CaptchaFail)
	}

	base := Defaults()
	base.MinSeconds = 3
	base.MaxLinks = 2
	base.BlockedWords = []string{"viagra"}
	refuse := base
	refuse.Action = ActionRefuse
	captcha := base
	captcha.Provider = ProviderTurnstile

	fresh := func(p *Pipeline) string { return IssueToken(p.TimingKey, "contact", t0.Add(-time.Second)) }
	cases := []struct {
		name   string
		set    Settings
		in     func(p *Pipeline) Input
		want   Verdict
		reason string
	}{
		{"clean", base, func(*Pipeline) Input {
			return Input{Slug: "contact", Data: map[string]interface{}{"m": "hello"}}
		}, Allow, ""},
		{"too many links", base, func(*Pipeline) Input {
			return Input{Slug: "contact", Data: map[string]interface{}{"m": "http://a.example.com http://b.example.com http://c.example.com"}}
		}, Spam, ReasonLinks},
		{"refuse blocked word", refuse, func(*Pipeline) Input {
			return Input{Slug: "contact", Data: map[string]interface{}{"m": "buy viagra"}}
		}, Refuse, ReasonWord},
		{"too fast", base, func(p *Pipeline) Input {
			return Input{Slug: "contact", Timing: fresh(p), Data: map[string]interface{}{"m": "hello"}}
		}, Spam, "submitted too fast"},
		{"forged token", base, func(*Pipeline) Input {
			return Input{Slug: "contact", Timing: "v1.1.forged", Data: map[string]interface{}{"m": "hello"}}
		}, Allow, ""},
		{"captcha missing", captcha, func(*Pipeline) Input {
			return Input{Slug: "contact", CaptchaRequired: true}
		}, CaptchaFail, "captcha missing"},
	}
	for _, c := range cases {
		p := newPipe(t, c.set, &stubVerifier{res: Result{OK: true}})
		got, reason := p.Check(context.Background(), c.in(p))
		if got != c.want || reason != c.reason {
			t.Errorf("%s: got %v %q want %v %q", c.name, got, reason, c.want, c.reason)
		}
		if got == Hold {
			t.Errorf("%s: Check returned Hold", c.name)
		}
	}
}
