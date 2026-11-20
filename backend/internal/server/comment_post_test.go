package server

import (
	"context"
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
	known  *comment.Commenter
	used   map[string]bool
	stored []comment.Draft
	lastID uuid.UUID
	keys   comment.Keys
}

func (f *postComments) Settings(context.Context) (comment.Settings, error) { return f.set, nil }
func (f *postComments) EntityMode(context.Context, uuid.UUID) (comment.Mode, error) {
	return f.mode, nil
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
	ipHash, _ := r.c.keys.HashIP(postIP)
	tok := spam.IssueCommentToken(spam.CommentTokenKey(postSecret), spam.CommentSubject(r.entity.String()), ipHash, time.Now().Add(-issuedAgo))
	n := spam.FieldNames(spam.FieldNameKey(postSecret), tok)
	return url.Values{
		commentsubmit.TokenField: {tok}, n.Body: {"A thoughtful remark."}, n.Name: {"Ada"}, n.Email: {"ada@example.test"},
	}
}

func (r *postRig) names(v url.Values) spam.Names {
	return spam.FieldNames(spam.FieldNameKey(postSecret), v.Get(commentsubmit.TokenField))
}

func (r *postRig) do(v url.Values, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/-/comments/"+r.entity.String(), strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = postIP + ":4711"
	req.SetPathValue("entityID", r.entity.String())
	for _, m := range mut {
		m(req)
	}
	rec := httptest.NewRecorder()
	r.srv.handleCommentPost(rec, req)
	return rec
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

func TestCommentPostInvalidRedirectsWithCode(t *testing.T) {
	r := newPostRig(t)
	v := r.form(time.Minute)
	v.Set(r.names(v).Body, "   ")
	rec := r.do(v)
	if rec.Header().Get("Location") != postPath+"?cerror=body_empty#hm-comment-form" || len(r.c.stored) != 0 {
		t.Fatalf("Location %q stored %d", rec.Header().Get("Location"), len(r.c.stored))
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
	ipHash, _ := r.c.keys.HashIP(postIP)
	if _, res := spam.CheckCommentToken(context.Background(), spam.CommentTokenKey(postSecret), r.c, spam.CommentSubject(r.entity.String()), ipHash, m[1], time.Now().Add(time.Minute)); res != spam.TokenOK {
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
