package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/core/internal/statichost"
)

func (r *postRig) liveForm(query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, commentFormLivePrefix+r.entity.String()+query, nil)
	req.RemoteAddr = postIP + ":4711"
	req.SetPathValue("entityID", r.entity.String())
	rec := httptest.NewRecorder()
	r.srv.handleCommentFormLive(rec, req)
	return rec
}

func (r *postRig) goStatic() {
	r.srv.setStaticHosting(statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite})
	// Issue tokens a minute ago so the dwell time is not scored as a bot.
	past := time.Now().Add(-time.Minute)
	r.srv.commentSlot.now = func() time.Time { return past }
}

var liveTokenRE = regexp.MustCompile(`name="` + commentsubmit.TokenField + `" value="([^"]+)"`)

// liveFormPost turns the fragment body into the form a browser would post.
func liveFormPost(t *testing.T, body string) url.Values {
	t.Helper()
	m := liveTokenRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token in fragment:\n%s", body)
	}
	n := spam.FieldNames(spam.FieldNameKey(postSecret), m[1])
	return url.Values{commentsubmit.TokenField: {m[1]}, n.Body: {"A thoughtful remark."}, n.Name: {"Ada"}, n.Email: {"ada@example.test"}}
}

func TestCommentFormLiveServedModeIs404(t *testing.T) {
	r := newPostRig(t)
	if rec := r.liveForm(""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestCommentFormLiveFragment(t *testing.T) {
	r := newPostRig(t)
	r.goStatic()
	rec := r.liveForm("")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `action="`+testStaticLive+`/-/comments/`+r.entity.String()+`"`) {
		t.Errorf("action not on the live origin:\n%s", body)
	}
	if strings.Contains(body, " hx-post=") || !strings.Contains(body, " data-hm-hxpost=") {
		t.Errorf("hx-post not renamed:\n%s", body)
	}
	p := render.CommentFormPlaceholders
	for _, ph := range []string{p.Token, p.Body, p.Name, p.Email, p.Honeypot, p.Extra} {
		if strings.Contains(body, ph) {
			t.Errorf("placeholder %q shipped", ph)
		}
	}
	if !liveTokenRE.MatchString(body) {
		t.Errorf("no token in fragment")
	}
}

func TestCommentFormLiveTokenIsSingleUseAndIPBound(t *testing.T) {
	site := []func(*http.Request){header("Origin", testStaticSite), header("Sec-Fetch-Site", "cross-site")}

	r := newPostRig(t)
	r.goStatic()
	post := liveFormPost(t, r.liveForm("").Body.String())
	if rec := r.do(post, site...); rec.Code != http.StatusSeeOther || len(r.c.stored) != 1 {
		t.Fatalf("first post: %d stored %d", rec.Code, len(r.c.stored))
	}
	r.do(post, site...)
	if len(r.c.stored) != 1 {
		t.Errorf("replayed token stored a second comment (%d)", len(r.c.stored))
	}

	r = newPostRig(t)
	r.goStatic()
	post = liveFormPost(t, r.liveForm("").Body.String())
	elsewhere := func(req *http.Request) { req.RemoteAddr = "198.51.100.9:4711" }
	r.do(post, append(site, elsewhere)...)
	if len(r.c.stored) != 0 {
		t.Errorf("token used from another network was accepted")
	}
}

func TestCommentFormLiveEdges(t *testing.T) {
	r := newPostRig(t)
	r.goStatic()

	r.srv.commentPost.entity = func(context.Context, uuid.UUID) (string, string, bool) { return "", "", false }
	if rec := r.liveForm(""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown entity: %d", rec.Code)
	}
	r.srv.commentPost.entity = func(context.Context, uuid.UUID) (string, string, bool) { return "Hello", postPath, true }

	r.c.mode = comment.ModeClosed
	if rec := r.liveForm(""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "<form") {
		t.Errorf("closed entity: %d, form present: %v", rec.Code, strings.Contains(rec.Body.String(), "<form"))
	}
	r.c.mode = comment.ModeOpen

	parent := uuid.New()
	r.c.parents = map[uuid.UUID]bool{parent: true}
	if body := r.liveForm("?creply=" + parent.String()).Body.String(); !strings.Contains(body, parent.String()) {
		t.Errorf("approved parent not set:\n%s", body)
	}
	if body := r.liveForm("?creply=" + uuid.NewString()).Body.String(); strings.Contains(body, `name="parent" value="`+parent.String()) {
		t.Errorf("unknown parent set")
	}
	if body := r.liveForm("?cnotice=bogus&cerror=bogus").Body.String(); strings.Contains(body, "bogus") {
		t.Errorf("bogus notice echoed:\n%s", body)
	}
}
