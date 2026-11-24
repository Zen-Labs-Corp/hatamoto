package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
)

const (
	postSecret = "post-secret"
	postPath   = "/blog/hello"
	postIP     = "203.0.113.7"
)

type postComments struct {
	set    comment.Settings
	mode   comment.Mode
	policy comment.Policy
	known  *comment.Commenter
	used   map[string]bool
	stored []comment.Draft
	lastID uuid.UUID
	// parents are the approved comments a reply may target.
	parents map[uuid.UUID]bool
	keys    comment.Keys
}

func (f *postComments) Settings(context.Context) (comment.Settings, error) { return f.set, nil }
func (f *postComments) EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error) {
	return comment.EntityConfig{SchemaSlug: "story", Mode: f.mode, Policy: f.policy}, nil
}
func (f *postComments) Store(_ context.Context, d comment.Draft) (comment.Comment, error) {
	f.stored = append(f.stored, d)
	f.lastID = uuid.New()
	return comment.Comment{
		ID: f.lastID, EntityID: d.EntityID, Status: d.Status, AuthorName: d.AuthorName, AuthorUserID: d.AuthorUserID,
		BodyHTML: d.BodyHTML, CreatedAt: time.Now(), Depth: 1,
	}, nil
}
func (f *postComments) Commenter(context.Context, string) (comment.Commenter, error) {
	if f.known == nil {
		return comment.Commenter{}, comment.ErrNotFound
	}
	return *f.known, nil
}
func (f *postComments) MatchBlocks(context.Context, comment.Probe) ([]comment.Block, error) {
	return nil, nil
}
func (f *postComments) DuplicateSince(context.Context, string, time.Time) (int, error) { return 0, nil }
func (f *postComments) ConsumeFormToken(_ context.Context, id string, _ time.Time) (bool, error) {
	if f.used[id] {
		return false, nil
	}
	f.used[id] = true
	return true, nil
}
func (f *postComments) ReplyParent(_ context.Context, _, parent uuid.UUID) (bool, error) {
	return f.parents[parent], nil
}
func (f *postComments) Keys() comment.Keys { return f.keys }

type postIdentity struct{ mails int }

func (f *postIdentity) CheckEmail(context.Context, string) error { return nil }
func (f *postIdentity) RequestConfirmation(context.Context, identity.ConfirmRequest) (identity.Outcome, error) {
	f.mails++
	return identity.OutcomeCheckEmail, nil
}

type postRig struct {
	t      *testing.T
	srv    *Server
	c      *postComments
	ident  *postIdentity
	entity uuid.UUID
	device uuid.UUID
	admin  uuid.UUID
}

func newPostRig(t *testing.T) *postRig {
	t.Helper()
	set := comment.Defaults()
	set.Enabled = true
	set.SlowModeSeconds = 0
	keys := comment.NewKeys(postSecret)
	c := &postComments{set: set, mode: comment.ModeOpen, used: map[string]bool{}, keys: keys}
	ident := &postIdentity{}
	limits := commentsubmit.NewLimits(time.Now)
	t.Cleanup(limits.Stop)
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := &postRig{t: t, c: c, ident: ident, entity: uuid.New(), device: uuid.New(), admin: uuid.New()}
	r.srv = &Server{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		renderEngine: engine,
		commentSlot: &commentSlot{
			tokenKey: spam.CommentTokenKey(postSecret), nameKey: spam.FieldNameKey(postSecret),
			hashIP: keys.HashIP, now: time.Now, on: func() bool { return true },
		},
		commentSubmit: &commentsubmit.Pipeline{
			Comments: c, Identity: ident,
			Spam:     &spam.Pipeline{Settings: fixedSettings{spam.Defaults()}, SecretKey: spam.SecretKey(postSecret)},
			TokenKey: spam.CommentTokenKey(postSecret), NameKey: spam.FieldNameKey(postSecret),
			Limits: limits,
		},
		commentPost: commentPostHooks{
			entity: func(context.Context, uuid.UUID) (string, string, bool) { return "Hello", postPath, true },
			admin: func(req *http.Request) (uuid.UUID, string, bool) {
				return r.admin, "Site", req.Header.Get("X-Test-Admin") != ""
			},
			device: func(_ context.Context, val string) (uuid.UUID, bool) { return r.device, val == "good" },
		},
	}
	return r
}

// form returns a valid post issued issuedAgo ago for the rig's address.
func (r *postRig) form(issuedAgo time.Duration) url.Values {
	_, netHash := r.c.keys.HashIP(postIP)
	tok := spam.IssueCommentToken(spam.CommentTokenKey(postSecret), spam.CommentSubject(r.entity.String()), netHash, time.Now().Add(-issuedAgo))
	n := spam.FieldNames(spam.FieldNameKey(postSecret), tok)
	return url.Values{
		commentsubmit.TokenField: {tok}, n.Body: {"A thoughtful remark."}, n.Name: {"Ada"}, n.Email: {"ada@example.test"},
	}
}

func (r *postRig) names(v url.Values) spam.Names {
	return spam.FieldNames(spam.FieldNameKey(postSecret), v.Get(commentsubmit.TokenField))
}

func (r *postRig) do(v url.Values, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.doTo(rec, v, mut...)
	return rec
}

func (r *postRig) doTo(rec *httptest.ResponseRecorder, v url.Values, mut ...func(*http.Request)) {
	req := httptest.NewRequest(http.MethodPost, "/-/comments/"+r.entity.String(), strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = postIP + ":4711"
	req.SetPathValue("entityID", r.entity.String())
	for _, m := range mut {
		m(req)
	}
	r.srv.handleCommentPost(rec, req)
}

func header(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }

func (r *postRig) knownDevice() func(*http.Request) {
	first := time.Now().Add(-60 * 24 * time.Hour)
	r.c.known = &comment.Commenter{ID: r.device, Trust: comment.TrustTrusted, ApprovedCount: 30, FirstApprovedAt: &first}
	return func(req *http.Request) { req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"}) }
}

const postFake = postPath + "?cnotice=posted#hm-comments"

func TestCommentPostSwitchOffIs404(t *testing.T) {
	r := newPostRig(t)
	r.c.set.Enabled = false
	rec := r.do(r.form(time.Minute))
	if rec.Code != http.StatusNotFound || len(r.c.stored) != 0 {
		t.Fatalf("status %d stored %d", rec.Code, len(r.c.stored))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	// Not wired, or a bad id: inert 404.
	r = newPostRig(t)
	r.srv.commentSubmit = nil
	if rec := r.do(r.form(time.Minute)); rec.Code != http.StatusNotFound {
		t.Fatalf("unwired status %d", rec.Code)
	}
	r = newPostRig(t)
	if rec := r.do(r.form(time.Minute), func(q *http.Request) { q.SetPathValue("entityID", "nope") }); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id status %d", rec.Code)
	}
}

// Every way a bot is turned away answers the same bytes, and stores nothing.
func TestCommentPostBotRejectsAreIdentical(t *testing.T) {
	cases := map[string]func(r *postRig) (url.Values, []func(*http.Request)){
		"honeypot": func(r *postRig) (url.Values, []func(*http.Request)) {
			v := r.form(time.Minute)
			v.Set(r.names(v).Honeypot, "x")
			return v, nil
		},
		"expired": func(r *postRig) (url.Values, []func(*http.Request)) { return r.form(5 * time.Hour), nil },
		"bad token": func(r *postRig) (url.Values, []func(*http.Request)) {
			v := r.form(time.Minute)
			v.Set(commentsubmit.TokenField, "v1.1.x.y")
			return v, nil
		},
		"sec-fetch-site cross-site": func(r *postRig) (url.Values, []func(*http.Request)) {
			return r.form(time.Minute), []func(*http.Request){header("Sec-Fetch-Site", "cross-site")}
		},
		"sec-fetch-site same-site": func(r *postRig) (url.Values, []func(*http.Request)) {
			return r.form(time.Minute), []func(*http.Request){header("Sec-Fetch-Site", "same-site")}
		},
		"foreign origin": func(r *postRig) (url.Values, []func(*http.Request)) {
			return r.form(time.Minute), []func(*http.Request){header("Origin", "https://evil.example")}
		},
		"origin null": func(r *postRig) (url.Values, []func(*http.Request)) {
			return r.form(time.Minute), []func(*http.Request){header("Origin", "null")}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			r := newPostRig(t)
			v, mut := mk(r)
			rec := r.do(v, mut...)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != postFake {
				t.Fatalf("status %d Location %q, want 303 %q", rec.Code, rec.Header().Get("Location"), postFake)
			}
			if len(r.c.stored) != 0 {
				t.Fatalf("stored %d", len(r.c.stored))
			}
		})
	}
}

func TestCommentPostReplayedTokenStoresOnce(t *testing.T) {
	r := newPostRig(t)
	v := r.form(time.Minute)
	first := r.do(v)
	second := r.do(v)
	if first.Header().Get("Location") != postPath+"?cnotice=check_email#hm-comments" {
		t.Fatalf("first Location = %q", first.Header().Get("Location"))
	}
	if second.Code != http.StatusSeeOther || second.Header().Get("Location") != postFake {
		t.Fatalf("replay = %d %q", second.Code, second.Header().Get("Location"))
	}
	if len(r.c.stored) != 1 || r.ident.mails != 1 {
		t.Fatalf("stored %d mails %d, want 1 each", len(r.c.stored), r.ident.mails)
	}
}

func TestCommentPostTokenFollowsNetwork(t *testing.T) {
	r := newPostRig(t)
	from := func(addr string) func(*http.Request) {
		return func(q *http.Request) { q.RemoteAddr = addr }
	}
	rec := r.do(r.form(time.Minute), from("203.0.113.99:4711"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != postPath+"?cnotice=check_email#hm-comments" || len(r.c.stored) != 1 {
		t.Fatalf("same /24: %d %q stored %d", rec.Code, rec.Header().Get("Location"), len(r.c.stored))
	}
	rec = r.do(r.form(time.Minute), from("198.51.100.9:4711"))
	if rec.Header().Get("Location") != postFake || len(r.c.stored) != 1 {
		t.Fatalf("other network: %q stored %d", rec.Header().Get("Location"), len(r.c.stored))
	}
}

func TestCommentPostSameOriginPasses(t *testing.T) {
	r := newPostRig(t)
	rec := r.do(r.form(time.Minute), header("Sec-Fetch-Site", "same-origin"), header("Origin", "http://example.com"))
	if rec.Header().Get("Location") != postPath+"?cnotice=check_email#hm-comments" || len(r.c.stored) != 1 {
		t.Fatalf("Location %q stored %d", rec.Header().Get("Location"), len(r.c.stored))
	}
	if r.c.stored[0].Status != comment.StatusDraft {
		t.Fatalf("status = %s", r.c.stored[0].Status)
	}
}

func TestCommentPostReplyParentValidated(t *testing.T) {
	r := newPostRig(t)
	good := uuid.New()
	r.c.parents = map[uuid.UUID]bool{good: true}
	for name, tc := range map[string]struct {
		parent string
		want   *uuid.UUID
	}{
		"approved parent": {good.String(), &good},
		"unknown parent":  {uuid.NewString(), nil},
	} {
		r.c.stored = nil
		v := r.form(time.Minute)
		v.Set(parentField, tc.parent)
		rec := r.do(v)
		if rec.Code != http.StatusSeeOther || len(r.c.stored) != 1 {
			t.Fatalf("%s: status %d stored %d", name, rec.Code, len(r.c.stored))
		}
		got := r.c.stored[0].ParentID
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: stored parent %v, want %v", name, got, tc.want)
		}
	}
}

// A same-origin post under Referrer-Policy no-referrer sends Origin: null.
func TestCommentPostOriginNullSameOriginPasses(t *testing.T) {
	r := newPostRig(t)
	rec := r.do(r.form(time.Minute), header("Sec-Fetch-Site", "same-origin"), header("Origin", "null"))
	if rec.Header().Get("Location") != postPath+"?cnotice=check_email#hm-comments" || len(r.c.stored) != 1 {
		t.Fatalf("Location %q stored %d", rec.Header().Get("Location"), len(r.c.stored))
	}
}

func TestCommentPostTrustedWithLinkIsPendingAdminIsApproved(t *testing.T) {
	r := newPostRig(t)
	dev := r.knownDevice()
	v := r.form(time.Minute)
	v.Set(r.names(v).Body, "see https://example.test/x")
	rec := r.do(v, dev)
	if loc := rec.Header().Get("Location"); loc != postPath+"#comment-"+r.c.lastID.String() {
		t.Fatalf("Location = %q", loc)
	}
	if d := r.c.stored[0]; d.Status != comment.StatusPending || d.AuthorUserID != nil {
		t.Fatalf("trusted link draft = %s", d.Status)
	}

	r = newPostRig(t)
	v = r.form(time.Minute)
	n := r.names(v)
	v.Set(n.Body, "see https://example.test/x")
	v.Del(n.Email)
	v.Del(n.Name)
	rec = r.do(v, header("X-Test-Admin", "1"))
	if rec.Code != http.StatusSeeOther || len(r.c.stored) != 1 {
		t.Fatalf("admin status %d stored %d", rec.Code, len(r.c.stored))
	}
	d := r.c.stored[0]
	if d.Status != comment.StatusApproved || d.AuthorUserID == nil || *d.AuthorUserID != r.admin || d.Email != "" {
		t.Fatalf("admin draft = %s author %v email %q", d.Status, d.AuthorUserID, d.Email)
	}
}

func TestCommentPostLimits(t *testing.T) {
	t.Run("4th rapid post from one address is dropped", func(t *testing.T) {
		r := newPostRig(t)
		for i := range 3 {
			r.do(r.form(time.Minute))
			if len(r.c.stored) != i+1 {
				t.Fatalf("post %d stored %d", i+1, len(r.c.stored))
			}
		}
		rec := r.do(r.form(time.Minute))
		if len(r.c.stored) != 3 || rec.Header().Get("Location") != postFake {
			t.Fatalf("4th: stored %d Location %q", len(r.c.stored), rec.Header().Get("Location"))
		}
	})
	t.Run("slow mode holds a known commenter", func(t *testing.T) {
		r := newPostRig(t)
		r.c.set.SlowModeSeconds = 120
		r.c.known = &comment.Commenter{ID: r.device, Trust: comment.TrustKnown, ApprovedCount: 5, FirstApprovedAt: new(time.Time)}
		dev := func(q *http.Request) { q.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"}) }
		r.do(r.form(time.Minute), dev)
		r.do(r.form(time.Minute), dev)
		if len(r.c.stored) != 2 || r.c.stored[1].Status != comment.StatusPending ||
			!hasReason(r.c.stored[1].SpamReasons, commentsubmit.ReasonSlowMode) {
			t.Fatalf("stored %d, second = %+v", len(r.c.stored), r.c.stored[len(r.c.stored)-1])
		}
	})
	t.Run("surge holds a trusted commenter", func(t *testing.T) {
		r := newPostRig(t)
		dev := r.knownDevice()
		for range 30 {
			r.srv.commentSubmit.Limits.Surge().Note()
		}
		r.do(r.form(time.Minute), dev)
		if len(r.c.stored) != 1 || r.c.stored[0].Status != comment.StatusPending ||
			!hasReason(r.c.stored[0].SpamReasons, comment.ReasonSurge) {
			t.Fatalf("stored %+v", r.c.stored)
		}
	})
}

func hasReason(rs []string, want string) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

func TestCommentPostClosedStoresNothing(t *testing.T) {
	r := newPostRig(t)
	r.c.mode = comment.ModeClosed
	rec := r.do(r.form(time.Minute))
	if rec.Header().Get("Location") != postPath+"?cerror=closed#hm-comments" || len(r.c.stored) != 0 {
		t.Fatalf("Location %q stored %d", rec.Header().Get("Location"), len(r.c.stored))
	}
}

// Without the page handlers there is nothing to re-render: the 303 with the
// fixed code stays, and it holds nothing the sender typed.
func TestCommentPostInvalidFallsBackToRedirect(t *testing.T) {
	r := newPostRig(t)
	v := r.form(time.Minute)
	v.Set(r.names(v).Body, "   ")
	rec := r.do(v)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != postPath+"?cerror=body_empty#hm-comment-form" || len(r.c.stored) != 0 {
		t.Fatalf("%d Location %q stored %d", rec.Code, rec.Header().Get("Location"), len(r.c.stored))
	}
	v = r.form(time.Minute)
	v.Set(r.names(v).Body, strings.Repeat("a", commentsubmit.MaxBodyRunes+1))
	if loc := r.do(v).Header().Get("Location"); strings.Contains(loc, "aaaa") || strings.Contains(loc, "Ada") || strings.Contains(loc, "ada") {
		t.Fatalf("typed text in Location %q", loc)
	}
}

// echoEngine renders the real comments fragment inside a page, so the no-JS
// re-render shows the same form markup a theme does.
type echoEngine struct {
	pageRenderer
	frag *render.Engine
}

func (e echoEngine) RenderPage(w io.Writer, v *render.PageView) error {
	if _, err := io.WriteString(w, "<html><body>"); err != nil {
		return err
	}
	if err := e.frag.RenderCommentsFragment(w, v, nil); err != nil {
		return err
	}
	_, err := io.WriteString(w, "</body></html>")
	return err
}

// mountPage wires the rig's server to render handlers that serve the rig's
// entity at postPath through the real commentView.
func (r *postRig) mountPage(t *testing.T) *renderFixture {
	t.Helper()
	f := newRenderFixture(t)
	f.h.slot = r.srv.commentSlot
	f.next = func() (*routeResult, pageOutcome, error) {
		p := pageResponse("Hello")
		p.Entity.ID = r.entity
		return &routeResult{Page: p, Kind: resolveKindEntity}, pageOutcome{Status: 200}, nil
	}
	f.h.engine = echoEngine{pageRenderer: f.h.engine, frag: r.srv.renderEngine}
	cv := &commentView{
		reader: &fakeCommentReader{
			set: comment.Settings{MaxDepth: 2, PageSize: 50}, mode: comment.ModeOpen,
			thread: comment.ThreadPage{Page: 1},
		},
		now: time.Now, logf: func(context.Context, string, error) {},
	}
	build := f.h.buildView
	f.h.buildView = func(ctx context.Context, req *http.Request, rr *routeResult) (*render.PageView, error) {
		v, err := build(ctx, req, rr)
		if err != nil {
			return nil, err
		}
		v.Uncacheable = new(bool)
		cv.fill(ctx, req, v)
		return v, nil
	}
	r.srv.renderHandlers = f.h
	return f
}

// longPost is a post the sender can fix: a body over the limit.
func (r *postRig) longPost() (url.Values, string) {
	v := r.form(time.Minute)
	long := strings.Repeat("a", commentsubmit.MaxBodyRunes+1)
	v.Set(r.names(v).Body, long)
	v.Set(r.names(v).Extra, "on")
	return v, long
}

func (r *postRig) checkRerender(t *testing.T, body, long, sentTok string) {
	t.Helper()
	if !strings.Contains(body, "hm-form__error") {
		t.Errorf("no error shown")
	}
	for _, want := range []string{`value="Ada"`, `value="ada@example.test"`, ">" + long + "</textarea>", " checked"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %.40q", want)
		}
	}
	if strings.Contains(body, "__hmcf_") {
		t.Errorf("placeholders left")
	}
	if strings.Count(body, "autofocus") != 1 || !regexp.MustCompile(`<textarea[^>]* autofocus[ >]`).MatchString(body) {
		t.Errorf("want autofocus on the body field only")
	}
	m := slotTokRe.FindStringSubmatch(body)
	if m == nil || m[1] == sentTok {
		t.Fatalf("token %v, want a fresh one", m)
	}
	_, netHash := r.c.keys.HashIP(postIP)
	if _, res := spam.CheckCommentToken(context.Background(), spam.CommentTokenKey(postSecret), r.c, spam.CommentSubject(r.entity.String()), netHash, m[1], time.Now().Add(time.Minute)); res != spam.TokenOK {
		t.Errorf("fresh token = %v", res)
	}
	if !strings.Contains(body, `name="`+spam.FieldNames(spam.FieldNameKey(postSecret), m[1]).Body+`"`) {
		t.Errorf("field names do not derive from the fresh token")
	}
}

func TestCommentPostNoJSInvalidRerendersPage(t *testing.T) {
	r := newPostRig(t)
	f := r.mountPage(t)
	v, long := r.longPost()
	rec := r.do(v)
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Fatalf("status %d Location %q, want a 200 page", rec.Code, rec.Header().Get("Location"))
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "private") || !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if !strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("not the full page: %.200s", rec.Body)
	}
	r.checkRerender(t, rec.Body.String(), long, v.Get(commentsubmit.TokenField))
	if len(f.cache.sets) != 0 || len(r.c.stored) != 0 {
		t.Fatalf("cache sets %v stored %d, want none", f.cache.sets, len(r.c.stored))
	}
	if len(f.notFound) != 0 {
		t.Fatalf("404 rows: %v", f.notFound)
	}
}

// The security middleware sets the global policy before the handler runs; the
// re-rendered page must carry the page policy, as a normal GET does.
func TestCommentPostNoJSRerenderCarriesPageCSP(t *testing.T) {
	r := newPostRig(t)
	r.mountPage(t)
	v, _ := r.longPost()
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	r.doTo(rec, v)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self' https:") {
		t.Fatalf("CSP = %q, want the page policy", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("headers = %v", rec.Header())
	}
}

func TestCommentFocus(t *testing.T) {
	for code, want := range map[string]string{
		commentsubmit.CodeName:        "name",
		commentsubmit.CodeEmail:       "email",
		commentsubmit.CodeEmailDomain: "email",
		commentsubmit.CodeBodyLong:    "body",
	} {
		if got := commentFocus(code); got != want {
			t.Errorf("commentFocus(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestCommentPostNoJSNon200PageFallsBackTo303(t *testing.T) {
	for name, set := range map[string]func(f *renderFixture){
		"failure": func(f *renderFixture) {
			f.next = func() (*routeResult, pageOutcome, error) { return nil, pageOutcome{}, errors.New("boom") }
		},
		"not found": func(f *renderFixture) {
			f.next = func() (*routeResult, pageOutcome, error) {
				return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPostRig(t)
			set(r.mountPage(t))
			v, long := r.longPost()
			rec := r.do(v)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "?cerror=body_long#hm-comment-form") {
				t.Fatalf("status %d Location %q, want the 303 ?cerror= fallback", rec.Code, loc)
			}
			if strings.Contains(loc+rec.Body.String(), long[:50]) || strings.Contains(rec.Header().Get("Cache-Control"), "public") {
				t.Fatalf("typed text or a public cache header leaked: %v", rec.Header())
			}
		})
	}
}

func TestCommentPostHTMXInvalidKeepsValues(t *testing.T) {
	r := newPostRig(t)
	hx := header("HX-Request", "true")
	v, long := r.longPost()
	rec := r.do(v, hx)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("%d Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), `id="hm-comment-form"`) {
		t.Fatalf("not the fragment: %.200s", rec.Body)
	}
	r.checkRerender(t, rec.Body.String(), long, v.Get(commentsubmit.TokenField))

	// A reply keeps its target.
	pid := uuid.New()
	r.c.parents = map[uuid.UUID]bool{pid: true}
	v, _ = r.longPost()
	v.Set("parent", pid.String())
	if body := r.do(v, hx).Body.String(); !strings.Contains(body, `name="parent" value="`+pid.String()+`"`) {
		t.Fatalf("reply target lost: %s", body)
	}

	// Typed text that looks like a placeholder is shown literally.
	v = r.form(time.Minute)
	v.Set(r.names(v).Name, "")
	v.Set(r.names(v).Body, "keep __hmcf_tok__ __hmcf_vbody__ literally")
	body := r.do(v, hx).Body.String()
	if !strings.Contains(body, ">keep __hmcf_tok__ __hmcf_vbody__ literally</textarea>") {
		t.Fatalf("typed placeholder rewritten: %s", body)
	}
	if m := slotTokRe.FindStringSubmatch(body); m == nil || strings.Contains(m[1], "__hmcf_") {
		t.Fatalf("token %v", m)
	}

	// The same through the no-JS page (a fresh rig: the limits are spent).
	r = newPostRig(t)
	r.mountPage(t)
	v = r.form(time.Minute)
	v.Set(r.names(v).Name, "")
	v.Set(r.names(v).Body, "keep __hmcf_tok__ literally")
	rec = r.do(v)
	if body := rec.Body.String(); !strings.Contains(body, ">keep __hmcf_tok__ literally</textarea>") {
		t.Fatalf("page: typed placeholder rewritten: %d %s", rec.Code, body)
	}
}

// Only a fixable error comes back with values: a bot or a closed thread never does.
func TestCommentPostNoValuesOnBotOrClosed(t *testing.T) {
	const typed = "TYPED-SECRET-BODY"
	cases := map[string]func(r *postRig, v url.Values){
		"honeypot": func(r *postRig, v url.Values) { v.Set(r.names(v).Honeypot, "x") },
		"closed":   func(r *postRig, v url.Values) { r.c.mode = comment.ModeClosed },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			r := newPostRig(t)
			r.mountPage(t)
			v := r.form(time.Minute)
			v.Set(r.names(v).Body, typed)
			mut(r, v)
			rec := r.do(v, header("HX-Request", "true"))
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), typed) {
				t.Fatalf("htmx: %d, typed text in the fragment", rec.Code)
			}
			v = r.form(time.Minute)
			v.Set(r.names(v).Body, typed)
			mut(r, v)
			rec = r.do(v)
			if rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), typed) || strings.Contains(rec.Body.String(), typed) {
				t.Fatalf("plain: %d Location %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestCommentPostIgnoresClientRedirect(t *testing.T) {
	r := newPostRig(t)
	v := r.form(time.Minute)
	v.Set("_redirect", "https://evil.example/")
	if loc := r.do(v).Header().Get("Location"); strings.Contains(loc, "evil") || !strings.HasPrefix(loc, postPath) {
		t.Fatalf("Location = %q", loc)
	}
}

var postNoHomepage = regexp.MustCompile(`(?i)website|homepage|hm_website|name="url"`)

func TestCommentPostHTMXFragment(t *testing.T) {
	r := newPostRig(t)
	dev := r.knownDevice()
	hx := header("HX-Request", "true")
	rec := r.do(r.form(time.Minute), dev, hx)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d Cache-Control %q body %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="hm-comment-form"`) {
		t.Fatalf("no form in %s", body)
	}
	if !strings.Contains(body, `hx-swap-oob="afterbegin:#hm-comments-list"`) || !strings.Contains(body, `id="comment-`+r.c.lastID.String()+`"`) {
		t.Fatalf("no new item in %s", body)
	}
	if strings.Contains(body, "__hmcf_") {
		t.Fatal("placeholders left in the fragment")
	}
	m := slotTokRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token in %s", body)
	}
	sent := r.form(time.Minute).Get(commentsubmit.TokenField)
	if m[1] == sent {
		t.Fatal("fragment reused the consumed token")
	}
	if postNoHomepage.MatchString(body) {
		t.Fatalf("homepage-like field in %s", body)
	}

	// A fresh form from the fragment is accepted once the dwell has passed:
	// its token verifies for this address and entity.
	_, netHash := r.c.keys.HashIP(postIP)
	if _, res := spam.CheckCommentToken(context.Background(), spam.CommentTokenKey(postSecret), r.c, spam.CommentSubject(r.entity.String()), netHash, m[1], time.Now().Add(time.Minute)); res != spam.TokenOK {
		t.Fatalf("fragment token = %v", res)
	}
}

func TestCommentPostHTMXNoticesAndErrors(t *testing.T) {
	r := newPostRig(t)
	hx := header("HX-Request", "true")

	rec := r.do(r.form(time.Minute), hx) // new commenter: check your mail
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hm-form__success") || strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Fatalf("check_email fragment: %d %s", rec.Code, rec.Body)
	}
	v := r.form(time.Minute)
	v.Set(r.names(v).Body, "")
	rec = r.do(v, hx)
	if !strings.Contains(rec.Body.String(), "hm-form__error") || !strings.Contains(rec.Body.String(), `id="hm-comment-form"`) {
		t.Fatalf("invalid fragment: %s", rec.Body)
	}
	if postNoHomepage.MatchString(rec.Body.String()) {
		t.Fatal("homepage-like field in the error fragment")
	}
	r.c.mode = comment.ModeClosed
	rec = r.do(r.form(time.Minute), hx)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "<form") || !strings.Contains(rec.Body.String(), "hm-form__error") {
		t.Fatalf("closed fragment: %d %s", rec.Code, rec.Body)
	}
	r.c.set.Enabled = false
	if rec := r.do(r.form(time.Minute), hx); rec.Code != http.StatusNotFound {
		t.Fatalf("off + htmx = %d, want 404", rec.Code)
	}
}

func TestCommentPostFallsBackToRedirectWithoutFragment(t *testing.T) {
	r := newPostRig(t)
	r.srv.commentSlot = nil
	rec := r.do(r.form(time.Minute), header("HX-Request", "true"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want the plain redirect", rec.Code)
	}
}
