package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/render"
)

type fakePolicy struct{ enabled bool }

func (f fakePolicy) PolicyInfo(context.Context, uuid.UUID) (comment.PolicyInfo, error) {
	return comment.PolicyInfo{Schema: "story", Enabled: f.enabled}, nil
}
func (f fakePolicy) CommenterEmail(context.Context, uuid.UUID) (comment.Commenter, string, error) {
	return comment.Commenter{}, "ada@example.test", nil
}

type fakeDrafter struct {
	name string
	err  error
}

func (f fakeDrafter) Name(context.Context, uuid.UUID, string) (string, error) { return f.name, f.err }

// policyRig is a post rig whose entity belongs to a schema with the sign-in policy.
func policyRig(t *testing.T, d commentsubmit.DraftNamer) *postRig {
	t.Helper()
	r := newPostRig(t)
	r.srv.commentSubmit.Policy = fakePolicy{enabled: true}
	r.srv.commentSubmit.Drafter = d
	r.srv.commentPost.policy = fakePolicy{enabled: true}.PolicyInfo
	return r
}

func TestPolicyPostStoresTheHookName(t *testing.T) {
	r := policyRig(t, fakeDrafter{name: "story_author"})
	rec := r.do(r.form(time.Minute), r.knownDevice()) // the form posts "Ada"
	if rec.Code != http.StatusSeeOther || len(r.c.stored) != 1 {
		t.Fatalf("status %d stored %d (Location %q)", rec.Code, len(r.c.stored), rec.Header().Get("Location"))
	}
	if got := r.c.stored[0].AuthorName; got != "story_author" {
		t.Errorf("stored name = %q, want the hook's", got)
	}
}

func TestPolicyPostNeedsSignIn(t *testing.T) {
	r := policyRig(t, fakeDrafter{name: "story_author"})
	rec := r.do(r.form(time.Minute)) // no device cookie
	if got := rec.Header().Get("Location"); !strings.Contains(got, "cerror=sign_in") || len(r.c.stored) != 0 {
		t.Fatalf("Location %q stored %d", got, len(r.c.stored))
	}
}

func TestPolicyPostDrafterFailureFailsClosed(t *testing.T) {
	for name, d := range map[string]commentsubmit.DraftNamer{
		"error":      fakeDrafter{err: errors.New("boom")},
		"no drafter": nil,
	} {
		t.Run(name, func(t *testing.T) {
			r := policyRig(t, d)
			rec := r.do(r.form(time.Minute), r.knownDevice())
			if got := rec.Header().Get("Location"); !strings.Contains(got, "cerror=draft") || len(r.c.stored) != 0 {
				t.Fatalf("Location %q stored %d", got, len(r.c.stored))
			}
		})
	}
}

const rejectText = "Pick a name first <b>"

func TestPolicyRejectTextShownEscapedNeverInAURL(t *testing.T) {
	r := policyRig(t, fakeDrafter{err: &commentsubmit.DraftRejected{Message: rejectText}})

	// The htmx fragment answers the POST with the text, escaped.
	rec := r.do(r.form(time.Minute), r.knownDevice(), header("HX-Request", "true"))
	if rec.Code != http.StatusOK || len(r.c.stored) != 0 {
		t.Fatalf("status %d stored %d", rec.Code, len(r.c.stored))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pick a name first &lt;b&gt;") || strings.Contains(body, rejectText) {
		t.Errorf("reject text not shown escaped: %s", body)
	}

	// The redirect fallback carries the code only.
	rec = r.do(r.form(time.Minute), r.knownDevice())
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "cerror=draft") {
		t.Fatalf("fallback: %d %q", rec.Code, loc)
	}
	for _, bad := range []string{"Pick", url.QueryEscape("Pick a name first"), "%3Cb"} {
		if strings.Contains(loc, bad) {
			t.Errorf("Location %q carries the plugin text", loc)
		}
	}

	// A GET of ?cerror=draft shows the generic text only.
	f := newViewFixture()
	v := f.run("/post?cerror=draft")
	if v.Comments == nil || v.Comments.Error != "draft" || v.Comments.ErrorText != "" {
		t.Errorf("GET fallback: %+v", v.Comments)
	}
}

func TestPolicyLockedFormHasNoNameOrEmailInput(t *testing.T) {
	r := policyRig(t, fakeDrafter{name: "story_author"})
	v := r.srv.commentFormView(context.Background(), r.entity, postPath, "", nil, "", "", nil)
	if v.CommentForm == nil || !v.CommentForm.Locked || v.CommentForm.SignInURL != commentSignInPath {
		t.Fatalf("form = %+v", v.CommentForm)
	}
	body := renderFilled(t, r.srv, v, r.entity)
	for _, bad := range []string{`autocomplete="name"`, `autocomplete="email"`, `type="email"`} {
		if strings.Contains(body, bad) {
			t.Errorf("a locked form still has %s", bad)
		}
	}

	// A policy-off entity gets today's form.
	r.srv.commentPost.policy = fakePolicy{enabled: false}.PolicyInfo
	v = r.srv.commentFormView(context.Background(), r.entity, postPath, "", nil, "", "", nil)
	if v.CommentForm.Locked || v.CommentForm.SignInURL != "" {
		t.Errorf("policy off: %+v", v.CommentForm)
	}
	if body := renderFilled(t, r.srv, v, r.entity); !strings.Contains(body, `autocomplete="email"`) {
		t.Error("the unlocked form lost its email input")
	}
}

func TestLockCommentFormFailsClosed(t *testing.T) {
	id := uuid.New()
	lock := func(p commentPolicyFn) render.CommentForm {
		var f render.CommentForm
		lockCommentForm(context.Background(), p, id, &f, func(context.Context, string, error) {})
		return f
	}
	if f := lock(nil); f.Locked {
		t.Error("nil policy locked the form")
	}
	if f := lock(func(context.Context, uuid.UUID) (comment.PolicyInfo, error) {
		return comment.PolicyInfo{}, errors.New("db")
	}); !f.Locked {
		t.Error("a policy read error must render the locked form")
	}
	if f := lock(func(context.Context, uuid.UUID) (comment.PolicyInfo, error) {
		return comment.PolicyInfo{}, comment.ErrNotFound
	}); f.Locked {
		t.Error("an unknown entity locked the form")
	}
}

// renderFilled renders the comments fragment of v and runs it through the slot.
func renderFilled(t *testing.T, s *Server, v *render.PageView, entity uuid.UUID) string {
	t.Helper()
	var buf strings.Builder
	if err := s.renderEngine.RenderCommentsFragment(&buf, v, nil); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", postPath, nil)
	r.RemoteAddr = postIP + ":1"
	out, ok := s.commentSlot.fill(r, []byte(buf.String()))
	if !ok {
		t.Fatal("slot did not fill")
	}
	return string(out)
}

// lockedPage renders the locked form and fills it for a request with the given cookie.
func lockedPage(t *testing.T, r *postRig, cookie string) string {
	t.Helper()
	v := r.srv.commentFormView(context.Background(), r.entity, postPath, "", nil, "", "", nil)
	var buf strings.Builder
	if err := r.srv.renderEngine.RenderCommentsFragment(&buf, v, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", postPath, nil)
	req.RemoteAddr = postIP + ":1"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: cookie})
	}
	out, ok := r.srv.commentSlot.fill(req, []byte(buf.String()))
	if !ok {
		t.Fatal("slot did not fill")
	}
	return string(out)
}

func lockedSlot(r *postRig, name string, calls *int) {
	r.srv.commentSlot.verify = func(v string, _ time.Time) (identity.DeviceClaims, bool) {
		return identity.DeviceClaims{CommenterID: r.device}, v == "good"
	}
	r.srv.commentSlot.names = newSignedNames(func(context.Context, uuid.UUID, uuid.UUID) (string, error) {
		*calls++
		return name, nil
	}, time.Now)
}

func TestPolicySlotShowsPostingAs(t *testing.T) {
	r := policyRig(t, fakeDrafter{})
	calls := 0
	lockedSlot(r, "story_author", &calls)
	page := lockedPage(t, r, "good")
	if !strings.Contains(page, "<strong>story_author</strong>") {
		t.Errorf("no posting-as line: %s", page)
	}
	if strings.Contains(page, "__hmcf_") {
		t.Errorf("placeholders left: %s", page)
	}
	if strings.Contains(page, "disabled") {
		t.Error("a signed-in visitor has a disabled body")
	}
	if n := strings.Count(page, " hidden>"); n != 2 {
		t.Errorf("hidden lines = %d, want the other two", n)
	}
}

func TestPolicySlotSignedInWithoutAName(t *testing.T) {
	r := policyRig(t, fakeDrafter{})
	calls := 0
	lockedSlot(r, "", &calls)
	page := lockedPage(t, r, "good")
	if !strings.Contains(page, "Signed in on this browser.") || strings.Count(page, " hidden>") != 2 {
		t.Errorf("fallback line not the only one shown: %s", page)
	}
	if strings.Contains(page, "disabled") {
		t.Error("a signed-in visitor has a disabled body")
	}
}

func TestPolicySlotSignedOutNeverLooksUpAName(t *testing.T) {
	r := policyRig(t, fakeDrafter{})
	calls := 0
	lockedSlot(r, "story_author", &calls)
	for _, cookie := range []string{"", "forged"} {
		page := lockedPage(t, r, cookie)
		if calls != 0 {
			t.Fatalf("cookie %q: a name was looked up", cookie)
		}
		if strings.Contains(page, "story_author") || !strings.Contains(page, "disabled") {
			t.Errorf("cookie %q: %s", cookie, page)
		}
	}
}
