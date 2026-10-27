package spam

import (
	"context"
	"time"
)

// SettingsReader supplies the current settings; *Store satisfies it.
type SettingsReader interface {
	Get(ctx context.Context) (Settings, error)
}

// Pipeline runs the checks every public form submission passes through.
type Pipeline struct {
	Settings  SettingsReader
	Verifier  Verifier
	SecretKey []byte // opens the sealed captcha secret
	TimingKey []byte // signs and checks timing tokens
	Now       func() time.Time
}

// Input is one submission as the pipeline sees it. Data holds the validated
// field values only: control fields must already be stripped.
type Input struct {
	Slug            string
	CaptchaRequired bool // the form's "Require the spam check" switch
	Data            map[string]interface{}
	Timing          string // the hm_ts token the page fetched at load
	Captcha         string // the provider's response token
	IP              string
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// IssueToken returns a timing token for the form with this slug.
func (p *Pipeline) IssueToken(slug string) string {
	return IssueToken(p.TimingKey, slug, p.now())
}

// CheckToken reports whether tok is a valid, unexpired timing token for slug.
func (p *Pipeline) CheckToken(slug, tok string) bool {
	_, ok := CheckToken(p.TimingKey, slug, tok, p.now())
	return ok
}

// CaptchaUsable reports whether s names a provider whose secret can be read.
func (p *Pipeline) CaptchaUsable(s Settings) bool {
	if s.Provider == ProviderNone {
		return false
	}
	_, ok := s.Secret(p.SecretKey)
	return ok
}

// Check runs, in order: the captcha (only when the form requires it and a
// provider with a readable secret is set up), the minimum time to submit, and
// the content rules. reason names the rule that tripped, never user content.
func (p *Pipeline) Check(ctx context.Context, in Input) (v Verdict, reason string) {
	set, err := p.Settings.Get(ctx)
	if err != nil {
		// Get returns usable defaults and the failure is already visible in
		// the store; the submission must not be lost over it.
		set = Defaults()
	}

	if in.CaptchaRequired && p.Verifier != nil {
		if secret, ok := set.Secret(p.SecretKey); ok && set.Provider != ProviderNone {
			if in.Captcha == "" {
				return CaptchaFail, "captcha missing"
			}
			res, err := p.Verifier.Verify(ctx, set.Provider, secret, set.SiteKey, in.Captcha, in.IP)
			if err != nil {
				return CaptchaFail, "captcha unavailable"
			}
			if !res.OK {
				return CaptchaFail, "captcha rejected"
			}
		}
	}

	reason, tripped := "", false
	if set.MinSeconds > 0 {
		if issued, ok := CheckToken(p.TimingKey, in.Slug, in.Timing, p.now()); ok &&
			p.now().Sub(issued) < time.Duration(set.MinSeconds)*time.Second {
			reason, tripped = "submitted too fast", true
		}
	}
	if !tripped {
		reason, tripped = EvaluateRules(set, in.Data)
	}
	switch {
	case !tripped:
		return Allow, ""
	case set.Action == ActionRefuse:
		return Refuse, reason
	}
	return Spam, reason
}
