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

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/pluginkey"
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
	CodeSignIn      = "sign_in" // policy schema: no signed-in commenter with an address
	CodeDraft       = "draft"   // policy schema: the comment.draft chain gave no name
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
	Typed   *Typed          // Invalid only
	// Message is a plugin's public reject text: Invalid with CodeDraft only.
	// It is shown on the POST response and never put in a URL.
	Message string
}

// Typed is what the sender typed. Submit returns it only with Invalid (an
// error a person can fix) so the form can come back filled; never on a drop.
type Typed struct {
	Body, Name, Email string
	Notify            bool
}

// Comments is the part of *comment.Service the pipeline uses.
type Comments interface {
	Settings(context.Context) (comment.Settings, error)
	EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error)
	Store(context.Context, comment.Draft) (comment.Comment, error)
	Commenter(context.Context, string) (comment.Commenter, error)
	MatchBlocks(context.Context, comment.Probe) ([]comment.Block, error)
	DuplicateSince(context.Context, string, time.Time) (int, error)
	ConsumeFormToken(context.Context, string, time.Time) (bool, error)
	ReplyParent(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	Keys() comment.Keys
}

// PolicyReader is optional: nil means no schema has the community policy. Which
// schema has it comes from Comments.EntityConfig (Policy.Enabled), the flag the
// Settings > Comments panel writes; the reader only opens the signed-in email.
type PolicyReader interface {
	CommenterEmail(context.Context, uuid.UUID) (comment.Commenter, string, error)
}

// DraftNamer asks the comment.draft hook for the locked name. The contract
// sends author_name empty, so there is no typed-name argument.
type DraftNamer interface {
	Name(ctx context.Context, commenter uuid.UUID, schema string) (string, error)
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
	Policy   PolicyReader // nil: no schema has the community policy
	Drafter  DraftNamer
	Log      *slog.Logger
	// OnSurge, if set, is called while surge mode is active: when it starts and at
	// most once a minute after. It must return at once (the caller sends mail elsewhere).
	OnSurge func(ctx context.Context)
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
	cfg, err := p.Comments.EntityConfig(ctx, req.EntityID)
	if errors.Is(err, comment.ErrNotFound) {
		return Outcome{Kind: NotFound}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	switch cfg.Mode {
	case comment.ModeOpen:
	case comment.ModeClosed:
		return Outcome{Kind: Closed}, nil
	default:
		return Outcome{Kind: NotFound}, nil
	}

	signIn := cfg.Policy.Enabled && p.Policy != nil

	// 2. Single-use token. The form field names derive from it, so the email
	// (and its limit bucket) is only known after this step.
	keys := p.Comments.Keys()
	ipHash, netHash := keys.HashIP(req.IP)
	entityID := req.EntityID.String()
	tok := req.Post.Get(TokenField)
	issued, res := spam.CheckCommentToken(ctx, p.TokenKey, p.Comments, spam.CommentSubject(entityID), netHash, tok, now)
	if res != spam.TokenOK {
		return p.drop(ctx, "token")
	}

	// 3. Surge window. Only a submit with a valid token counts, so forged posts
	// cannot push the site into surge mode.
	surge, started := p.Limits.Surge().Note()
	if started {
		p.log().WarnContext(ctx, "comment surge mode started", "reason", "surge")
	}
	if surge && p.OnSurge != nil && p.Limits.Surge().Beat(started) {
		p.OnSurge(ctx)
	}

	// 4. Fields, only under the rotated names.
	names := spam.FieldNames(p.NameKey, tok)
	body := req.Post.Get(names.Body)
	name := strings.TrimSpace(req.Post.Get(names.Name))
	email := strings.TrimSpace(req.Post.Get(names.Email))
	notify := req.Post.Get(names.Extra) != ""
	if admin {
		name, email = strings.TrimSpace(req.AdminName), ""
	}
	typedEmail := req.Post.Get(names.Email)
	if admin {
		typedEmail = ""
	}
	fixable := func(code string) (Outcome, error) {
		return Outcome{Kind: Invalid, Code: code, Typed: &Typed{
			Body: body, Name: req.Post.Get(names.Name), Email: typedEmail, Notify: notify,
		}}, nil
	}
	var vouched *comment.Commenter // a plugin-vouched commenter with no readable email
	if signIn && !admin {
		// Policy schema: the identity is the signed-in commenter, never the post.
		if req.DeviceCommenter == nil {
			return fixable(CodeSignIn)
		}
		cm, opened, err := p.Policy.CommenterEmail(ctx, *req.DeviceCommenter)
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return fixable(CodeSignIn)
		case err != nil:
			return Outcome{}, err
		case opened == "" && cm.OriginPlugin == "":
			return fixable(CodeSignIn)
		case opened == "":
			vouched = &cm
		}
		email = opened
		if p.Drafter == nil {
			return fixable(CodeDraft)
		}
		drafted, err := p.Drafter.Name(ctx, *req.DeviceCommenter, cfg.SchemaSlug)
		if err != nil {
			out, _ := fixable(CodeDraft)
			var dr *DraftRejected
			if errors.As(err, &dr) {
				out.Message = dr.Message
			}
			return out, nil
		}
		name = drafted
	}

	// 5. Limits. An admin is keyed by account, not by an email.
	var commenterKey string
	if admin {
		commenterKey = "admin|" + req.AdminUserID.String()
	} else if vouched != nil {
		commenterKey = "commenter|" + vouched.ID.String()
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
		return fixable(CodeBodyEmpty)
	case n > MaxBodyRunes:
		return fixable(CodeBodyLong)
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		return fixable(CodeName)
	}
	if !admin && vouched == nil {
		switch err := p.Identity.CheckEmail(ctx, email); {
		case errors.Is(err, identity.ErrEmailInvalid):
			return fixable(CodeEmail)
		case errors.Is(err, identity.ErrEmailNotAccepted):
			return fixable(CodeEmailDomain)
		case err != nil:
			return Outcome{}, err
		}
	}

	// 9. Format, then the signals.
	rendered, err := commentfmt.Render(body)
	switch {
	case errors.Is(err, commentfmt.ErrEmpty):
		return fixable(CodeBodyEmpty)
	case errors.Is(err, commentfmt.ErrTooLong):
		return fixable(CodeBodyLong)
	case err != nil:
		return Outcome{}, err
	}
	// The commenter lookup comes before the checks; step 11 reuses the result.
	var known *comment.Commenter
	if vouched != nil {
		known = vouched
	} else if !admin {
		cm, err := p.Comments.Commenter(ctx, commenterKey)
		switch {
		case errors.Is(err, comment.ErrNotFound):
		case err != nil:
			return Outcome{}, err
		default:
			known = &cm
		}
	}
	// The key comes only from the verified device: an address someone typed
	// proves nothing, so it never names a commenter to the checks.
	checkCtx := ctx
	if req.DeviceCommenter != nil && !admin {
		checkCtx = pluginkey.WithCommenter(ctx, *req.DeviceCommenter)
	}
	sr, verdict, hold, err := p.assess(checkCtx, spamSet, assessInput{
		Schema: cfg.SchemaSlug, EntityID: req.EntityID, Name: name, Body: body, Email: email, Rendered: rendered,
		IPHash: ipHash, NetHash: netHash, Issued: issued, IssuedOK: true, Now: now, IP: req.IP,
	})
	if err != nil {
		return Outcome{}, err
	}

	// 11. Trust and status.
	trust := comment.TrustNew
	confirmed := false
	if known != nil {
		trust = comment.EvaluateTrust(comment.TrustStats{
			Current: known.Trust, ApprovedCount: known.ApprovedCount,
			FirstApprovedAt: known.FirstApprovedAt, LastSpamAt: known.LastSpamAt,
		}, set.Thresholds, now)
		confirmed = req.DeviceCommenter != nil && known.ID == *req.DeviceCommenter
	}
	status, why := comment.Decide(comment.DecideInput{
		Verdict: verdict, HasLinks: rendered.HasLinks, Trust: trust, AdminAuthor: admin, Surge: surge,
		Community: cfg.Policy.Enabled,
	})
	slow := !admin && trust != comment.TrustTrusted &&
		p.Limits.InSlowMode(commenterKey, entityID, cfg.Policy.SlowMode(set.SlowModeSeconds))
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
		Locale:      req.Locale, NotifyReplies: notify && !admin,
	}

	if vouched != nil {
		draft.CommenterID = &vouched.ID
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

// assessInput is what the content checks look at, for a new comment or an edit.
type assessInput struct {
	EntityID       uuid.UUID
	Schema         string
	Name, Body     string
	Email          string
	Rendered       commentfmt.Result
	IPHash         string
	NetHash        string
	Issued         time.Time
	IssuedOK       bool
	Now            time.Time
	SkipDuplicates bool
	IP             string // raw client address: in memory only, never stored or logged
}

// assess runs the content checks (blocklists, duplicates, plugin checks, score)
// and returns the score, its verdict ("allow", "hold" or "spam") and whether a
// plugin check asked for a hold.
func (p *Pipeline) assess(ctx context.Context, spamSet spam.Settings, in assessInput) (spam.ScoreResult, string, bool, error) {
	entityID := in.EntityID.String()
	rendered, body := in.Rendered, in.Body
	var signals []spam.Signal
	hits, err := p.Comments.MatchBlocks(ctx, comment.Probe{
		EmailDomain: comment.EmailDomain(in.Email), LinkDomains: rendered.LinkDomains,
		IPHash: in.IPHash, NetHash: in.NetHash, BodyHash: rendered.BodyHash,
	})
	if err != nil {
		return spam.ScoreResult{}, "", false, err
	}
	if len(hits) > 0 {
		signals = append(signals, spam.Signal{Reason: spam.ReasonBlocklist, Points: spam.PointsBlocklist})
	}
	if !in.SkipDuplicates {
		dups, err := p.Comments.DuplicateSince(ctx, rendered.BodyHash, in.Now.Add(-duplicateWindow))
		if err != nil {
			return spam.ScoreResult{}, "", false, err
		}
		if dups > 0 {
			signals = append(signals, spam.Signal{Reason: spam.ReasonDuplicate, Points: spam.PointsDuplicate})
		}
	}

	// Plugin checks, then the score. The address rides in the context so the
	// plugin host can derive keys for plugins holding client.key; CheckInput never has it.
	ctx = clientkey.WithIP(ctx, in.IP)
	score, checkReasons, hold := comment.RunCheckers(ctx, p.Checkers, comment.CheckInput{
		EntityID: in.EntityID, Schema: in.Schema, Body: body, LinkDomains: rendered.LinkDomains, BodyHash: rendered.BodyHash,
		IPHash: in.IPHash, NetHash: in.NetHash,
	}, comment.DefaultCheckTimeout)
	if score != 0 || len(checkReasons) > 0 {
		signals = append(signals, spam.Signal{Reason: "comment.check", Points: score})
		for _, r := range checkReasons {
			signals = append(signals, spam.Signal{Reason: r})
		}
	}
	sr := spam.ScoreWith(spamSet, spam.ScoreInput{
		Subject: spam.CommentSubject(entityID), Texts: []string{in.Name, body}, Signals: signals,
	}, in.Issued, in.IssuedOK, in.Now)
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
	return sr, verdict, hold, nil
}

// EditCheck is an edit of a comment, as the handler parsed it.
type EditCheck struct {
	EntityID uuid.UUID
	Schema   string // the entity's content type slug, for comment.check
	Name     string
	Body     string
	Rendered commentfmt.Result
	// BodyChanged is false when the new body hashes like the stored one (the
	// stored comment would otherwise count as its own duplicate).
	BodyChanged bool
	IP          string
	Now         time.Time
	// Commenter is the verified device commenter (the editor); comment.check
	// gets its key, as on a new post.
	Commenter *uuid.UUID
}

// HoldEdit runs the content checks of a new post on an edited body and reports
// whether the comment must go back to moderation. The timing, honeypot, token,
// captcha and rate checks belong to the form and do not apply.
func (p *Pipeline) HoldEdit(ctx context.Context, e EditCheck) (bool, error) {
	spamSet, err := p.Spam.Settings.Get(ctx)
	if err != nil {
		spamSet = spam.Defaults()
	}
	if e.Commenter != nil {
		ctx = pluginkey.WithCommenter(ctx, *e.Commenter)
	}
	ipHash, netHash := p.Comments.Keys().HashIP(e.IP)
	_, verdict, _, err := p.assess(ctx, spamSet, assessInput{
		Schema: e.Schema, EntityID: e.EntityID, Name: e.Name, Body: e.Body, Rendered: e.Rendered,
		IPHash: ipHash, NetHash: netHash, Now: e.Now.UTC(), SkipDuplicates: !e.BodyChanged, IP: e.IP,
	})
	return verdict != "allow", err
}
