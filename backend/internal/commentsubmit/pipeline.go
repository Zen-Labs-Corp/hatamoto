package commentsubmit

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/spam"
)

// Form limits, in runes.
const (
	MaxBodyRunes = 5000
	MaxNameRunes = 80
)

// TokenField is the static form field that carries the comment token. Every
// other field name is derived from the token (spam.FieldNames).
const TokenField = "hm_ct"

// Invalid codes in Outcome.Code.
const (
	CodeBodyEmpty   = "body_empty"
	CodeBodyLong    = "body_long"
	CodeName        = "name"
	CodeEmail       = "email"
	CodeEmailDomain = "email_domain"
)

// duplicateWindow is how far back an identical body counts as a duplicate.
const duplicateWindow = 24 * time.Hour

// providerCaptchaFields are the response fields the captcha widgets post. hm_captcha
// comes first: hm.js writes the token there (the only field a reCAPTCHA v3 form has).
var providerCaptchaFields = []string{"hm_captcha", "cf-turnstile-response", "g-recaptcha-response", "h-captcha-response"}

// Request is one comment submit as the handler parsed it.
type Request struct {
	EntityID    uuid.UUID
	EntityTitle string
	ParentID    *uuid.UUID
	Post        url.Values
	IP          string
	UserAgent   string
	Locale      string
	// DeviceCommenter is the commenter the device cookie vouches for
	// (identity.Service.Device checks the token version); nil if none.
	DeviceCommenter *uuid.UUID
	AdminUserID     *uuid.UUID // set for an owner or editor posting as the site
	AdminName       string
	CrossSite       bool // the handler's origin check failed
	Now             time.Time
}

// OutcomeKind says what the handler answers.
type OutcomeKind int

const (
	// Posted: stored, visible to the sender (a spam verdict is a shadow hold).
	Posted OutcomeKind = iota
	// CheckEmail: stored as a draft, a confirmation mail was requested.
	CheckEmail
	// FakeSuccess: dropped, nothing stored; answer as if it worked.
	FakeSuccess
	// Invalid: the sender can fix it; Outcome.Code says what.
	Invalid
	Closed
	NotFound
)

// Outcome is the result of Submit.
type Outcome struct {
	Kind    OutcomeKind
	Comment comment.Comment // Posted and CheckEmail
	Code    string          // Invalid
}

// Comments is the part of *comment.Service the pipeline uses.
type Comments interface {
	Settings(context.Context) (comment.Settings, error)
	EntityMode(context.Context, uuid.UUID) (comment.Mode, error)
	Store(context.Context, comment.Draft) (comment.Comment, error)
	Commenter(context.Context, string) (comment.Commenter, error)
	MatchBlocks(context.Context, comment.Probe) ([]comment.Block, error)
	DuplicateSince(context.Context, string, time.Time) (int, error)
	ConsumeFormToken(context.Context, string, time.Time) (bool, error)
	Keys() comment.Keys
}

// Identity is the part of *identity.Service the pipeline uses.
type Identity interface {
	CheckEmail(context.Context, string) error
	RequestConfirmation(context.Context, identity.ConfirmRequest) (identity.Outcome, error)
}

// Pipeline decides what happens to one comment submit. All spam and trust
// decisions live here; comment.Service.Store only saves.
type Pipeline struct {
	Comments Comments
	Identity Identity
	Spam     *spam.Pipeline // Settings, Verifier and SecretKey are used
	TokenKey []byte
	NameKey  []byte
	Limits   *Limits
	Checkers []comment.Checker
	Log      *slog.Logger
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// drop logs the rule that dropped a submit (never user data) and answers with
// a fake success.
func (p *Pipeline) drop(ctx context.Context, reason string) (Outcome, error) {
	p.log().DebugContext(ctx, "comment submit dropped", "reason", reason)
	return Outcome{Kind: FakeSuccess}, nil
}

func invalid(code string) (Outcome, error) { return Outcome{Kind: Invalid, Code: code}, nil }

// Submit runs the checks cheapest first. Nothing is stored for FakeSuccess,
// Invalid, Closed and NotFound.
func (p *Pipeline) Submit(ctx context.Context, req Request) (Outcome, error) {
	now := req.Now.UTC()
	admin := req.AdminUserID != nil

	// 1. Comments on, entity open.
	set, err := p.Comments.Settings(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if !set.Enabled {
		return Outcome{Kind: NotFound}, nil
	}
	mode, err := p.Comments.EntityMode(ctx, req.EntityID)
	if errors.Is(err, comment.ErrNotFound) {
		return Outcome{Kind: NotFound}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	switch mode {
	case comment.ModeOpen:
	case comment.ModeClosed:
		return Outcome{Kind: Closed}, nil
	default:
		return Outcome{Kind: NotFound}, nil
	}

	// 2. Surge window.
	surge, started := p.Limits.Surge().Note()
	if started {
		p.log().WarnContext(ctx, "comment surge mode started", "reason", "surge")
	}

	// 3. Single-use token. The form field names derive from it, so the email
	// (and its limit bucket) is only known after this step.
	keys := p.Comments.Keys()
	ipHash, netHash := keys.HashIP(req.IP)
	entityID := req.EntityID.String()
	tok := req.Post.Get(TokenField)
	issued, res := spam.CheckCommentToken(ctx, p.TokenKey, p.Comments, spam.CommentSubject(entityID), ipHash, tok, now)
	if res != spam.TokenOK {
		return p.drop(ctx, "token")
	}

	// 4. Fields, only under the rotated names.
	names := spam.FieldNames(p.NameKey, tok)
	body := req.Post.Get(names.Body)
	name := strings.TrimSpace(req.Post.Get(names.Name))
	email := strings.TrimSpace(req.Post.Get(names.Email))
	if admin {
		name, email = strings.TrimSpace(req.AdminName), ""
	}

	// 5. Limits. An admin is keyed by account, not by an email.
	var commenterKey string
	if admin {
		commenterKey = "admin|" + req.AdminUserID.String()
	} else {
		commenterKey = keys.HashEmail(email)
	}
	if !p.Limits.Allow(ipHash, netHash, commenterKey, entityID) {
		return p.drop(ctx, "rate limit")
	}

	// 6. Honeypot and origin.
	if req.Post.Get(names.Honeypot) != "" {
		return p.drop(ctx, "honeypot")
	}
	if req.CrossSite {
		return p.drop(ctx, "cross site")
	}

	// 7. Captcha, only when the site asks for it and it can work.
	spamSet, err := p.Spam.Settings.Get(ctx)
	if err != nil {
		spamSet = spam.Defaults()
	}
	if set.Captcha && p.Spam.CaptchaUsable(spamSet) && p.Spam.Verifier != nil {
		secret, _ := spamSet.Secret(p.Spam.SecretKey)
		captcha := ""
		for _, f := range providerCaptchaFields {
			if captcha = req.Post.Get(f); captcha != "" {
				break
			}
		}
		if captcha == "" {
			return p.drop(ctx, "captcha missing")
		}
		vr, err := p.Spam.Verifier.Verify(ctx, spamSet.Provider, secret, spamSet.SiteKey, captcha, req.IP)
		if err != nil {
			return p.drop(ctx, "captcha unavailable")
		}
		if !vr.OK {
			return p.drop(ctx, "captcha rejected")
		}
	}

	// 8. Field limits and the address.
	switch n := utf8.RuneCountInString(strings.TrimSpace(body)); {
	case n == 0:
		return invalid(CodeBodyEmpty)
	case n > MaxBodyRunes:
		return invalid(CodeBodyLong)
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		return invalid(CodeName)
	}
	if !admin {
		switch err := p.Identity.CheckEmail(ctx, email); {
		case errors.Is(err, identity.ErrEmailInvalid):
			return invalid(CodeEmail)
		case errors.Is(err, identity.ErrEmailNotAccepted):
			return invalid(CodeEmailDomain)
		case err != nil:
			return Outcome{}, err
		}
	}

	// 9. Format, then the signals.
	rendered, err := commentfmt.Render(body)
	switch {
	case errors.Is(err, commentfmt.ErrEmpty):
		return invalid(CodeBodyEmpty)
	case errors.Is(err, commentfmt.ErrTooLong):
		return invalid(CodeBodyLong)
	case err != nil:
		return Outcome{}, err
	}
	var signals []spam.Signal
	hits, err := p.Comments.MatchBlocks(ctx, comment.Probe{
		EmailDomain: comment.EmailDomain(email), LinkDomains: rendered.LinkDomains,
		IPHash: ipHash, NetHash: netHash, BodyHash: rendered.BodyHash,
	})
	if err != nil {
		return Outcome{}, err
	}
	if len(hits) > 0 {
		signals = append(signals, spam.Signal{Reason: spam.ReasonBlocklist, Points: spam.PointsBlocklist})
	}
	dups, err := p.Comments.DuplicateSince(ctx, rendered.BodyHash, now.Add(-duplicateWindow))
	if err != nil {
		return Outcome{}, err
	}
	if dups > 0 {
		signals = append(signals, spam.Signal{Reason: spam.ReasonDuplicate, Points: spam.PointsDuplicate})
	}

	// 10. Plugin checks, then the score.
	score, checkReasons, hold := comment.RunCheckers(ctx, p.Checkers, comment.CheckInput{
		EntityID: req.EntityID, Body: body, LinkDomains: rendered.LinkDomains, BodyHash: rendered.BodyHash,
		IPHash: ipHash, NetHash: netHash,
	}, comment.DefaultCheckTimeout)
	if score != 0 || len(checkReasons) > 0 {
		signals = append(signals, spam.Signal{Reason: "comment.check", Points: score})
		for _, r := range checkReasons {
			signals = append(signals, spam.Signal{Reason: r})
		}
	}
	sr := spam.ScoreWith(spamSet, spam.ScoreInput{
		Subject: spam.CommentSubject(entityID), Texts: []string{name, body}, Signals: signals,
	}, issued, true, now)
	verdict := "allow"
	switch sr.Verdict {
	case spam.Spam:
		verdict = "spam"
	case spam.Hold:
		verdict = "hold"
	}
	if hold && verdict == "allow" {
		verdict = "hold"
	}

	// 11. Trust and status.
	trust := comment.TrustNew
	confirmed := false
	if !admin {
		cm, err := p.Comments.Commenter(ctx, commenterKey)
		switch {
		case errors.Is(err, comment.ErrNotFound):
		case err != nil:
			return Outcome{}, err
		default:
			trust = comment.EvaluateTrust(comment.TrustStats{
				Current: cm.Trust, ApprovedCount: cm.ApprovedCount,
				FirstApprovedAt: cm.FirstApprovedAt, LastSpamAt: cm.LastSpamAt,
			}, set.Thresholds, now)
			confirmed = req.DeviceCommenter != nil && cm.ID == *req.DeviceCommenter
		}
	}
	status, why := comment.Decide(comment.DecideInput{
		Verdict: verdict, HasLinks: rendered.HasLinks, Trust: trust, AdminAuthor: admin, Surge: surge,
	})
	slow := !admin && trust != comment.TrustTrusted &&
		p.Limits.InSlowMode(commenterKey, entityID, time.Duration(set.SlowModeSeconds)*time.Second)
	if slow {
		if status == comment.StatusApproved {
			status = comment.StatusPending
		}
		why = append(why, ReasonSlowMode)
	}

	draft := comment.Draft{
		EntityID: req.EntityID, ParentID: req.ParentID, Email: email, AuthorName: name,
		AuthorUserID: req.AdminUserID,
		BodyRaw:      body, BodyHTML: rendered.HTML, HasLinks: rendered.HasLinks,
		LinkDomains: rendered.LinkDomains, BodyHash: rendered.BodyHash,
		IP: req.IP, UserAgent: req.UserAgent,
		SpamScore: sr.Score, SpamVerdict: verdict,
		SpamReasons: append(append([]string{}, sr.Reasons...), why...),
		Locale:      req.Locale,
	}

	// 12. Store. An unconfirmed address waits as a draft; the verdict is held
	// at least when a hold applied, so Service.Confirm's own Decide keeps it.
	if !confirmed && !admin {
		draft.Status = comment.StatusDraft
		if (surge || slow || hold) && draft.SpamVerdict == "allow" {
			draft.SpamVerdict = "hold"
		}
		c, err := p.Comments.Store(ctx, draft)
		if err != nil {
			return Outcome{}, err
		}
		if _, err := p.Identity.RequestConfirmation(ctx, identity.ConfirmRequest{
			Comment: c, Email: email, EntityTitle: req.EntityTitle,
		}); err != nil {
			return Outcome{}, err
		}
		return Outcome{Kind: CheckEmail, Comment: c}, nil
	}
	draft.Status = status
	c, err := p.Comments.Store(ctx, draft)
	if err != nil {
		return Outcome{}, err
	}
	p.Limits.MarkPosted(commenterKey, entityID)
	return Outcome{Kind: Posted, Comment: c}, nil
}
