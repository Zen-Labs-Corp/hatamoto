package commentsubmit

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/pluginkey"
	"hatamoto.dev/core/internal/spam"
)

var t0 = time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)

type fakeComments struct {
	set       comment.Settings
	mode      comment.Mode
	policy    comment.Policy
	schema    string
	modeErr   error
	commenter *comment.Commenter
	blocks    []comment.Block
	dups      int
	used      map[string]bool
	stored    []comment.Draft
	keys      comment.Keys
}

func (f *fakeComments) Settings(context.Context) (comment.Settings, error) { return f.set, nil }
func (f *fakeComments) EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error) {
	return comment.EntityConfig{SchemaSlug: f.schema, Mode: f.mode, Policy: f.policy}, f.modeErr
}
func (f *fakeComments) Store(_ context.Context, d comment.Draft) (comment.Comment, error) {
	f.stored = append(f.stored, d)
	cid := uuid.New()
	return comment.Comment{ID: uuid.New(), Status: d.Status, CommenterID: &cid}, nil
}
func (f *fakeComments) Commenter(context.Context, string) (comment.Commenter, error) {
	if f.commenter == nil {
		return comment.Commenter{}, comment.ErrNotFound
	}
	return *f.commenter, nil
}
func (f *fakeComments) MatchBlocks(context.Context, comment.Probe) ([]comment.Block, error) {
	return f.blocks, nil
}
func (f *fakeComments) DuplicateSince(context.Context, string, time.Time) (int, error) {
	return f.dups, nil
}
func (f *fakeComments) ConsumeFormToken(_ context.Context, id string, _ time.Time) (bool, error) {
	if f.used[id] {
		return false, nil
	}
	f.used[id] = true
	return true, nil
}
func (f *fakeComments) ReplyParent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeComments) Keys() comment.Keys { return f.keys }

type fakeIdentity struct {
	checkErr error
	confirms int
}

func (f *fakeIdentity) CheckEmail(context.Context, string) error { return f.checkErr }
func (f *fakeIdentity) RequestConfirmation(context.Context, identity.ConfirmRequest) (identity.Outcome, error) {
	f.confirms++
	return identity.OutcomeCheckEmail, nil
}

type spamSettings struct{ s spam.Settings }

func (s spamSettings) Get(context.Context) (spam.Settings, error) { return s.s, nil }

type fakeVerifier struct{ ok bool }

func (v fakeVerifier) Verify(context.Context, string, string, string, string, string) (spam.Result, error) {
	return spam.Result{OK: v.ok}, nil
}

type scoreChecker struct {
	score int
	err   error
}

func (c scoreChecker) Check(context.Context, comment.CheckInput) (int, []string, error) {
	return c.score, nil, c.err
}

type harness struct {
	p    *Pipeline
	c    *fakeComments
	id   *fakeIdentity
	sp   *spam.Settings
	req  Request
	post url.Values
	tok  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	set := comment.Defaults()
	set.Enabled = true
	set.SlowModeSeconds = 0
	c := &fakeComments{set: set, mode: comment.ModeOpen, used: map[string]bool{}, keys: comment.NewKeys("test-secret")}
	id := &fakeIdentity{}
	sp := spam.Defaults()
	l := NewLimits(func() time.Time { return t0 })
	t.Cleanup(l.Stop)
	h := &harness{c: c, id: id, sp: &sp}
	h.p = &Pipeline{
		Comments: c, Identity: id,
		Spam:     &spam.Pipeline{Settings: spamSettings{sp}, SecretKey: spam.SecretKey("test-secret")},
		TokenKey: spam.CommentTokenKey("test-secret"), NameKey: spam.FieldNameKey("test-secret"),
		Limits: l,
	}
	h.req = Request{EntityID: uuid.New(), EntityTitle: "Hello", IP: "203.0.113.7", Now: t0}
	h.fresh(t0.Add(-time.Minute))
	return h
}

// fresh issues a token at issuedAt and fills a valid form under its field names.
func (h *harness) fresh(issuedAt time.Time) {
	_, netHash := h.c.keys.HashIP(h.req.IP)
	h.tok = spam.IssueCommentToken(h.p.TokenKey, spam.CommentSubject(h.req.EntityID.String()), netHash, issuedAt)
	n := spam.FieldNames(h.p.NameKey, h.tok)
	h.post = url.Values{
		TokenField: {h.tok}, n.Body: {"A thoughtful remark."}, n.Name: {"Ada"}, n.Email: {"ada@example.test"},
	}
	h.req.Post = h.post
}

func (h *harness) names() spam.Names { return spam.FieldNames(h.p.NameKey, h.tok) }

func (h *harness) submit(t *testing.T) Outcome {
	t.Helper()
	h.p.Spam.Settings = spamSettings{*h.sp}
	out, err := h.p.Submit(context.Background(), h.req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSubmitBranches(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(h *harness)
		kind    OutcomeKind
		code    string
		stored  int
		status  comment.Status
		verdict string
		reason  string
	}{
		{name: "new commenter gets a draft and a mail", kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "allow"},
		{name: "comments off", setup: func(h *harness) { h.c.set.Enabled = false }, kind: NotFound},
		{name: "entity hidden", setup: func(h *harness) { h.c.mode = comment.ModeHidden }, kind: NotFound},
		{name: "entity unknown", setup: func(h *harness) { h.c.modeErr = comment.ErrNotFound }, kind: NotFound},
		{name: "entity closed", setup: func(h *harness) { h.c.mode = comment.ModeClosed }, kind: Closed},
		{name: "bad token", setup: func(h *harness) { h.post.Set(TokenField, "v1.1.x.y") }, kind: FakeSuccess},
		{name: "replayed token", setup: func(h *harness) {
			_, netHash := h.c.keys.HashIP(h.req.IP)
			_, _ = spam.CheckCommentToken(context.Background(), h.p.TokenKey, h.c, spam.CommentSubject(h.req.EntityID.String()), netHash, h.tok, t0)
		}, kind: FakeSuccess},
		{name: "token of another address", setup: func(h *harness) { h.req.IP = "198.51.100.9" }, kind: FakeSuccess},
		{name: "same /24, other address", setup: func(h *harness) { h.req.IP = "203.0.113.99" }, kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "allow"},
		{name: "honeypot filled", setup: func(h *harness) { h.post.Set(h.names().Honeypot, "x") }, kind: FakeSuccess},
		{name: "cross site", setup: func(h *harness) { h.req.CrossSite = true }, kind: FakeSuccess},
		{name: "fixed field names are ignored", setup: func(h *harness) {
			n := h.names()
			h.post.Set("body", h.post.Get(n.Body))
			h.post.Del(n.Body)
		}, kind: Invalid, code: CodeBodyEmpty},
		{name: "empty body", setup: func(h *harness) { h.post.Set(h.names().Body, "  \n ") }, kind: Invalid, code: CodeBodyEmpty},
		{name: "long body", setup: func(h *harness) {
			b := make([]rune, MaxBodyRunes+1)
			for i := range b {
				b[i] = 'a'
			}
			h.post.Set(h.names().Body, string(b))
		}, kind: Invalid, code: CodeBodyLong},
		{name: "no name", setup: func(h *harness) { h.post.Set(h.names().Name, " ") }, kind: Invalid, code: CodeName},
		{name: "bad email", setup: func(h *harness) { h.id.checkErr = identity.ErrEmailInvalid }, kind: Invalid, code: CodeEmail},
		{name: "disposable email", setup: func(h *harness) { h.id.checkErr = identity.ErrEmailNotAccepted }, kind: Invalid, code: CodeEmailDomain},
		{name: "too fast is stored as spam reason", setup: func(h *harness) { h.fresh(t0.Add(-time.Second)); h.sp.MinSeconds = 3 },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "spam", reason: spam.ReasonTooFast},
		{name: "blocklist hit scores spam", setup: func(h *harness) { h.c.blocks = []comment.Block{{Value: "example.test"}} },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "spam", reason: spam.ReasonBlocklist},
		{name: "duplicate body holds", setup: func(h *harness) { h.c.dups = 1 },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: spam.ReasonDuplicate},
		{name: "failing checker holds", setup: func(h *harness) { h.p.Checkers = []comment.Checker{scoreChecker{err: errors.New("boom")}} },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: comment.ReasonCheckFailed},
		{name: "checker score is a signal", setup: func(h *harness) { h.p.Checkers = []comment.Checker{scoreChecker{score: 4}} },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: "comment.check"},
		{name: "surge draft is held", setup: func(h *harness) {
			for i := 0; i < defaultSurgeLimit; i++ {
				h.p.Limits.Surge().Note()
			}
		}, kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: comment.ReasonSurge},
		{name: "known commenter on a confirmed device is approved", setup: func(h *harness) { h.known(true) },
			kind: Posted, stored: 1, status: comment.StatusApproved, verdict: "allow"},
		{name: "known commenter with a link is pending", setup: func(h *harness) {
			h.known(true)
			h.post.Set(h.names().Body, "see https://example.test/x")
		}, kind: Posted, stored: 1, status: comment.StatusPending, verdict: "allow", reason: comment.ReasonLink},
		{name: "known commenter without a device is mailed", setup: func(h *harness) { h.known(false) },
			kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "allow"},
		{name: "slow mode holds a known commenter", setup: func(h *harness) {
			h.known(true)
			h.c.set.SlowModeSeconds = 120
			h.p.Limits.MarkPosted(h.c.keys.HashEmail("ada@example.test"), h.req.EntityID.String())
		}, kind: Posted, stored: 1, status: comment.StatusPending, verdict: "allow", reason: ReasonSlowMode},
		{name: "policy type posts a new commenter with a link at once", setup: func(h *harness) {
			h.c.policy = comment.Policy{Enabled: true}
			h.known(true)
			h.c.commenter.Trust = comment.TrustNew
			h.post.Set(h.names().Body, "see https://example.test/x")
		}, kind: Posted, stored: 1, status: comment.StatusApproved, verdict: "allow"},
		{name: "policy type still holds on surge", setup: func(h *harness) {
			h.c.policy = comment.Policy{Enabled: true}
			for i := 0; i < defaultSurgeLimit; i++ {
				h.p.Limits.Surge().Note()
			}
		}, kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: comment.ReasonSurge},
		{name: "policy type still holds a duplicate body", setup: func(h *harness) {
			h.c.policy = comment.Policy{Enabled: true}
			h.c.dups = 1
		}, kind: CheckEmail, stored: 1, status: comment.StatusDraft, verdict: "hold", reason: spam.ReasonDuplicate},
		{name: "policy slow mode replaces the site value", setup: func(h *harness) {
			h.known(true)
			h.c.set.SlowModeSeconds = 120
			h.c.policy = comment.Policy{Enabled: true, SlowModeSeconds: 0}
			h.p.Limits.MarkPosted(h.c.keys.HashEmail("ada@example.test"), h.req.EntityID.String())
		}, kind: Posted, stored: 1, status: comment.StatusApproved, verdict: "allow"},
		{name: "policy slow mode holds a known commenter", setup: func(h *harness) {
			h.known(true)
			h.c.policy = comment.Policy{Enabled: true, SlowModeSeconds: 120}
			h.p.Limits.MarkPosted(h.c.keys.HashEmail("ada@example.test"), h.req.EntityID.String())
		}, kind: Posted, stored: 1, status: comment.StatusPending, verdict: "allow", reason: ReasonSlowMode},
		{name: "blocked commenter is a shadow spam", setup: func(h *harness) { h.known(true); h.c.commenter.Trust = comment.TrustBlocked },
			kind: Posted, stored: 1, status: comment.StatusSpam, verdict: "allow", reason: comment.ReasonBlocked},
		{name: "admin posts approved without email", setup: func(h *harness) {
			u := uuid.New()
			h.req.AdminUserID, h.req.AdminName = &u, "Site"
			h.post.Del(h.names().Email)
			h.post.Del(h.names().Name)
			h.id.checkErr = errors.New("must not be called")
		}, kind: Posted, stored: 1, status: comment.StatusApproved, verdict: "allow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.setup != nil {
				tc.setup(h)
			}
			out := h.submit(t)
			if out.Kind != tc.kind || out.Code != tc.code {
				t.Fatalf("outcome = %d/%q, want %d/%q", out.Kind, out.Code, tc.kind, tc.code)
			}
			if len(h.c.stored) != tc.stored {
				t.Fatalf("stored %d drafts, want %d", len(h.c.stored), tc.stored)
			}
			if tc.stored == 0 {
				return
			}
			d := h.c.stored[0]
			if d.Status != tc.status || d.SpamVerdict != tc.verdict {
				t.Errorf("draft = %s/%s, want %s/%s", d.Status, d.SpamVerdict, tc.status, tc.verdict)
			}
			if tc.reason != "" && !contains(d.SpamReasons, tc.reason) {
				t.Errorf("reasons %v lack %q", d.SpamReasons, tc.reason)
			}
			wantMail := 0
			if tc.kind == CheckEmail {
				wantMail = 1
			}
			if h.id.confirms != wantMail {
				t.Errorf("confirmations = %d, want %d", h.id.confirms, wantMail)
			}
		})
	}
}

func (h *harness) known(device bool) {
	first := t0.Add(-60 * 24 * time.Hour)
	cm := &comment.Commenter{ID: uuid.New(), Trust: comment.TrustKnown, ApprovedCount: 5, FirstApprovedAt: &first}
	h.c.commenter = cm
	if device {
		h.req.DeviceCommenter = &cm.ID
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestSubmitDraftFields(t *testing.T) {
	h := newHarness(t)
	h.req.UserAgent, h.req.Locale = "ua", "en"
	if out := h.submit(t); out.Kind != CheckEmail {
		t.Fatalf("kind = %d", out.Kind)
	}
	d := h.c.stored[0]
	if d.Email != "ada@example.test" || d.IP != "203.0.113.7" {
		t.Errorf("draft must carry the raw email and ip for Store to hash, got %q %q", d.Email, d.IP)
	}
	if d.BodyRaw != "A thoughtful remark." || d.BodyHTML != "<p>A thoughtful remark.</p>" || d.BodyHash == "" {
		t.Errorf("body fields = %q %q %q", d.BodyRaw, d.BodyHTML, d.BodyHash)
	}
	if d.AuthorName != "Ada" || d.UserAgent != "ua" || d.Locale != "en" {
		t.Errorf("author fields = %q %q %q", d.AuthorName, d.UserAgent, d.Locale)
	}
}

func TestSubmitNotifyRepliesOptIn(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness)
		want  bool
	}{
		{"rotated name opts in", func(h *harness) { h.post.Set(h.names().Extra, "1") }, true},
		{"no box means no", func(h *harness) {}, false},
		{"fixed field name is ignored", func(h *harness) { h.post.Set("extra", "1") }, false},
		{"admin post never subscribes", func(h *harness) {
			u := uuid.New()
			h.req.AdminUserID, h.req.AdminName = &u, "Site"
			h.post.Set(h.names().Extra, "1")
		}, false},
		{"device-confirmed path keeps the flag", func(h *harness) {
			h.known(true)
			h.post.Set(h.names().Extra, "1")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			h.submit(t)
			if len(h.c.stored) != 1 || h.c.stored[0].NotifyReplies != tc.want {
				t.Fatalf("stored = %+v, want NotifyReplies %v", h.c.stored, tc.want)
			}
		})
	}
}

func TestSubmitRateLimitDropsWithoutStoring(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		h.fresh(t0.Add(-time.Minute))
		if out := h.submit(t); out.Kind != CheckEmail {
			t.Fatalf("submit %d = %d", i, out.Kind)
		}
	}
	h.fresh(t0.Add(-time.Minute))
	before := len(h.c.stored)
	if out := h.submit(t); out.Kind != FakeSuccess || len(h.c.stored) != before {
		t.Fatalf("4th submit = %d, stored %d -> %d", out.Kind, before, len(h.c.stored))
	}
}

func TestSubmitCaptcha(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		ok    bool
		kind  OutcomeKind
	}{
		{"passed", "cf-turnstile-response", true, CheckEmail},
		{"hm.js field", "hm_captcha", true, CheckEmail},
		{"rejected", "cf-turnstile-response", false, FakeSuccess},
		{"missing", "", true, FakeSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.c.set.Captcha = true
			sealed, err := spam.SealSecret(h.p.Spam.SecretKey, "s3cret")
			if err != nil {
				t.Fatal(err)
			}
			h.sp.Provider, h.sp.SiteKey, h.sp.SecretSealed = spam.ProviderTurnstile, "site", sealed
			h.p.Spam.Verifier = fakeVerifier{ok: tc.ok}
			if tc.field != "" {
				h.post.Set(tc.field, "resp")
			}
			out := h.submit(t)
			if out.Kind != tc.kind || (tc.kind == FakeSuccess && len(h.c.stored) != 0) {
				t.Fatalf("kind = %d stored %d, want %d", out.Kind, len(h.c.stored), tc.kind)
			}
		})
	}
}

func TestSubmitCaptchaOffWhenUnusable(t *testing.T) {
	h := newHarness(t)
	h.c.set.Captcha = true // no provider set up: the check is skipped, not failed
	if out := h.submit(t); out.Kind != CheckEmail {
		t.Fatalf("kind = %d", out.Kind)
	}
}

func TestSubmitSurgeCallsOnSurge(t *testing.T) {
	for _, surge := range []bool{false, true} {
		h := newHarness(t)
		now := t0
		h.p.Limits = NewLimits(func() time.Time { return now }) // explicit clock, moved by hand below
		calls := 0
		h.p.OnSurge = func(context.Context) { calls++ }
		if surge {
			for i := 0; i < defaultSurgeLimit; i++ {
				h.p.Limits.Surge().Note()
			}
		}
		submit := func() {
			h.fresh(now.Add(-time.Minute))
			h.req.Now = now
			h.submit(t)
		}
		submit()
		submit() // same instant: throttled to one report
		want := 0
		if surge {
			want = 1
		}
		if calls != want {
			t.Fatalf("surge=%v: OnSurge calls = %d, want %d", surge, calls, want)
		}
		now = now.Add(SurgeBeat)
		submit() // a beat later the surge reports again
		if surge {
			want = 2
		}
		if calls != want {
			t.Fatalf("surge=%v after a beat: OnSurge calls = %d, want %d", surge, calls, want)
		}
	}
}

func TestSubmitSurgeIgnoresInvalidTokens(t *testing.T) {
	h := newHarness(t)
	calls := 0
	h.p.OnSurge = func(context.Context) { calls++ }
	for i := 0; i < defaultSurgeLimit+5; i++ {
		h.req.Post = url.Values{TokenField: {"forged"}}
		if out := h.submit(t); out.Kind != FakeSuccess {
			t.Fatalf("forged submit %d = %d", i, out.Kind)
		}
	}
	if calls != 0 {
		t.Fatalf("OnSurge called %d times for forged posts", calls)
	}
	if active, _ := h.p.Limits.Surge().Note(); active {
		t.Fatal("forged posts started surge mode")
	}
}

func TestHoldEdit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(h *harness)
		body  string
		want  bool
	}{
		{"clean", func(*harness) {}, "A fine edit.", false},
		{"blocked words", func(h *harness) { h.sp.BlockedWords = []string{"viagra"} }, "cheap viagra", true},
		{"body hash block", func(h *harness) { h.c.blocks = []comment.Block{{}} }, "A fine edit.", true},
		{"checker hold", func(h *harness) { h.p.Checkers = []comment.Checker{scoreChecker{err: errors.New("down")}} }, "A fine edit.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			h.p.Spam.Settings = spamSettings{*h.sp}
			res, err := commentfmt.Render(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			got, err := h.p.HoldEdit(context.Background(), EditCheck{
				EntityID: h.req.EntityID, Name: "Ada", Body: tc.body, Rendered: res, BodyChanged: true, IP: h.req.IP, Now: t0,
			})
			if err != nil || got != tc.want {
				t.Fatalf("HoldEdit = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestHoldEditDuplicateOnlyWhenBodyChanged(t *testing.T) {
	h := newHarness(t)
	h.c.dups = 1
	h.p.Spam.Settings = spamSettings{*h.sp}
	res, _ := commentfmt.Render("same text")
	e := EditCheck{EntityID: h.req.EntityID, Name: "Ada", Body: "same text", Rendered: res, IP: h.req.IP, Now: t0}
	if hold, _ := h.p.HoldEdit(context.Background(), e); hold {
		t.Error("an unchanged body counted as a duplicate of itself")
	}
	e.BodyChanged = true
	if hold, _ := h.p.HoldEdit(context.Background(), e); !hold {
		t.Error("a duplicate edit was not held")
	}
}

func TestSubmitTokenFollowsIPv6Prefix(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		kind OutcomeKind
	}{
		{"2001:db8:1:2::ffff", CheckEmail},
		{"2001:db8:1:3::1", FakeSuccess},
	} {
		h := newHarness(t)
		h.req.IP = "2001:db8:1:2::1"
		h.fresh(t0.Add(-time.Minute))
		h.req.IP = tc.ip
		if out := h.submit(t); out.Kind != tc.kind {
			t.Errorf("from %s: kind %v, want %v", tc.ip, out.Kind, tc.kind)
		}
	}
}

func TestSubmitTypedOnlyOnInvalid(t *testing.T) {
	invalid := []struct {
		name  string
		setup func(h *harness)
		code  string
		body  string
		who   string
	}{
		{"body_empty", func(h *harness) { h.post.Set(h.names().Body, "") }, CodeBodyEmpty, "", "Ada"},
		{"body_long", func(h *harness) { h.post.Set(h.names().Body, strings.Repeat("x", MaxBodyRunes+1)) }, CodeBodyLong, strings.Repeat("x", MaxBodyRunes+1), "Ada"},
		{"name", func(h *harness) { h.post.Set(h.names().Name, "") }, CodeName, "A thoughtful remark.", ""},
		{"email", func(h *harness) { h.id.checkErr = identity.ErrEmailInvalid }, CodeEmail, "A thoughtful remark.", "Ada"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.post.Set(h.names().Extra, "1")
			tc.setup(h)
			out := h.submit(t)
			if out.Kind != Invalid || out.Code != tc.code {
				t.Fatalf("got %v %q", out.Kind, out.Code)
			}
			want := Typed{Body: tc.body, Name: tc.who, Email: "ada@example.test", Notify: true}
			if out.Typed == nil || *out.Typed != want {
				t.Fatalf("Typed = %+v, want %+v", out.Typed, want)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		setup func(h *harness)
	}{
		{"honeypot", func(h *harness) { h.post.Set(h.names().Honeypot, "x") }},
		{"bad token", func(h *harness) { h.post.Set(TokenField, "v1.1.x.y") }},
		{"cross site", func(h *harness) { h.req.CrossSite = true }},
		{"closed", func(h *harness) { h.c.mode = comment.ModeClosed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			if out := h.submit(t); out.Typed != nil {
				t.Fatalf("Typed = %+v, want nil", out.Typed)
			}
		})
	}
}

// fakePolicy opens the signed-in email; whether a schema has the policy comes
// from the entity config (fakeComments.policy), as in production.
type fakePolicy struct{ email string }

func (f fakePolicy) CommenterEmail(_ context.Context, id uuid.UUID) (comment.Commenter, string, error) {
	return comment.Commenter{ID: id}, f.email, nil
}

type fakeDrafter struct {
	name string
	err  error
}

func (f fakeDrafter) Name(context.Context, uuid.UUID, string) (string, error) { return f.name, f.err }

type schemaChecker struct{ schema *string }

func (c schemaChecker) Check(_ context.Context, in comment.CheckInput) (int, []string, error) {
	*c.schema = in.Schema
	return 0, nil, nil
}

// policyHarness is a policy-schema submit with a signed-in device.
func policyHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.known(true)
	h.c.schema, h.c.policy = "story", comment.Policy{Enabled: true}
	h.p.Policy = fakePolicy{email: "grace@example.test"}
	h.p.Drafter = fakeDrafter{name: "Grace H."}
	return h
}

func TestPolicySchemaUsesDraftAndOpenedEmail(t *testing.T) {
	h := policyHarness(t)
	var schema string
	h.p.Checkers = []comment.Checker{schemaChecker{&schema}}
	out := h.submit(t) // the post carries the name Ada and ada@example.test
	if out.Kind != Posted || len(h.c.stored) != 1 {
		t.Fatalf("kind = %d stored = %d", out.Kind, len(h.c.stored))
	}
	if d := h.c.stored[0]; d.AuthorName != "Grace H." || d.Email != "grace@example.test" {
		t.Errorf("draft identity = %q %q", d.AuthorName, d.Email)
	}
	if schema != "story" {
		t.Errorf("check schema = %q", schema)
	}
}

func TestPolicySchemaRefusals(t *testing.T) {
	t.Run("no device", func(t *testing.T) {
		h := policyHarness(t)
		h.req.DeviceCommenter = nil
		out := h.submit(t)
		if out.Kind != Invalid || out.Code != CodeSignIn || out.Typed == nil || out.Typed.Body == "" || len(h.c.stored) != 0 {
			t.Fatalf("out = %+v stored = %d", out, len(h.c.stored))
		}
	})
	t.Run("no email", func(t *testing.T) {
		h := policyHarness(t)
		h.p.Policy = fakePolicy{}
		if out := h.submit(t); out.Code != CodeSignIn || len(h.c.stored) != 0 {
			t.Fatalf("out = %+v", out)
		}
	})
	t.Run("draft unavailable", func(t *testing.T) {
		h := policyHarness(t)
		h.p.Drafter = fakeDrafter{err: ErrDraftUnavailable}
		out := h.submit(t)
		if out.Kind != Invalid || out.Code != CodeDraft || out.Message != "" || len(h.c.stored) != 0 {
			t.Fatalf("out = %+v", out)
		}
	})
	t.Run("draft rejected", func(t *testing.T) {
		h := policyHarness(t)
		h.p.Drafter = fakeDrafter{err: &DraftRejected{Message: "Pick a name first"}}
		out := h.submit(t)
		if out.Kind != Invalid || out.Code != CodeDraft || out.Message != "Pick a name first" || out.Typed == nil || len(h.c.stored) != 0 {
			t.Fatalf("out = %+v", out)
		}
	})
	t.Run("no drafter", func(t *testing.T) {
		h := policyHarness(t)
		h.p.Drafter = nil
		if out := h.submit(t); out.Code != CodeDraft {
			t.Fatalf("out = %+v", out)
		}
	})
}

func TestPolicySchemaAdminUnchanged(t *testing.T) {
	h := policyHarness(t)
	admin := uuid.New()
	h.req.AdminUserID, h.req.AdminName, h.req.DeviceCommenter = &admin, "Site", nil
	h.p.Drafter = fakeDrafter{err: ErrDraftUnavailable}
	out := h.submit(t)
	if out.Kind != Posted || h.c.stored[0].AuthorName != "Site" {
		t.Fatalf("out = %+v", out)
	}
}

func TestPolicySchemaOffKeepsPostedIdentity(t *testing.T) {
	h := policyHarness(t)
	h.c.policy.Enabled = false
	h.p.Drafter = fakeDrafter{err: ErrDraftUnavailable}
	h.submit(t)
	if d := h.c.stored[0]; d.AuthorName != "Ada" || d.Email != "ada@example.test" {
		t.Errorf("draft identity = %q %q", d.AuthorName, d.Email)
	}
}

// keyChecker records the commenter the check context names.
type keyChecker struct {
	got *uuid.UUID
	ok  *bool
}

func (c keyChecker) Check(ctx context.Context, _ comment.CheckInput) (int, []string, error) {
	id, ok := pluginkey.CommenterFrom(ctx)
	*c.got, *c.ok = id, ok
	return 0, nil, nil
}

func TestCheckKeyOnlyFromVerifiedDevice(t *testing.T) {
	t.Run("typed email of a known commenter, no device", func(t *testing.T) {
		h := newHarness(t)
		h.known(false)
		var id uuid.UUID
		var ok bool
		h.p.Checkers = []comment.Checker{keyChecker{&id, &ok}}
		h.submit(t)
		if ok {
			t.Fatalf("check saw commenter %s from a typed address", id)
		}
	})
	t.Run("verified device", func(t *testing.T) {
		h := newHarness(t)
		h.known(true)
		var id uuid.UUID
		var ok bool
		h.p.Checkers = []comment.Checker{keyChecker{&id, &ok}}
		h.submit(t)
		if !ok || id != *h.req.DeviceCommenter {
			t.Fatalf("check commenter = %s, %v; want the device commenter", id, ok)
		}
	})
}

func TestHoldEditCheckGetsSchemaAndKey(t *testing.T) {
	h := policyHarness(t)
	var schema string
	var id uuid.UUID
	var ok bool
	h.p.Checkers = []comment.Checker{schemaChecker{&schema}, keyChecker{&id, &ok}}
	h.p.Spam.Settings = spamSettings{*h.sp}
	res, err := commentfmt.Render("A fine edit.")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.p.HoldEdit(context.Background(), EditCheck{
		EntityID: h.req.EntityID, Schema: "story", Name: "Ada", Body: "A fine edit.", Rendered: res, BodyChanged: true,
		IP: h.req.IP, Now: t0, Commenter: h.req.DeviceCommenter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if schema != "story" || !ok || id != *h.req.DeviceCommenter {
		t.Errorf("edit check saw schema %q, key %s (%v)", schema, id, ok)
	}
}
