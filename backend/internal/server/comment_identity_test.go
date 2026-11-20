package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
)

type fakeCommentIdentity struct {
	enabled    bool
	peekErr    error
	confirmErr error
	peeks      int
	confirms   int
	pending    identity.Pending
	confirmed  identity.Confirmed
}

func (f *fakeCommentIdentity) Enabled(context.Context) bool { return f.enabled }
func (f *fakeCommentIdentity) Peek(context.Context, string) (identity.Pending, error) {
	f.peeks++
	return f.pending, f.peekErr
}
func (f *fakeCommentIdentity) Confirm(context.Context, string) (identity.Confirmed, error) {
	f.confirms++
	return f.confirmed, f.confirmErr
}
func (f *fakeCommentIdentity) Cookie(c identity.Confirmed, secure bool) *http.Cookie {
	return &http.Cookie{Name: "hm_c", Value: "v", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: secure, MaxAge: int(c.CookieLifetime / time.Second)}
}

const testConfirmTitle = "Quiet Harbour Notes"

func newCommentFixture(path string) (*fakeCommentIdentity, commentEntityLookup, uuid.UUID) {
	cid := uuid.New()
	eid := uuid.New()
	f := &fakeCommentIdentity{
		enabled:   true,
		pending:   identity.Pending{CommentID: cid, EntityID: eid},
		confirmed: identity.Confirmed{Comment: comment.Comment{ID: cid, EntityID: eid}, CookieLifetime: 90 * 24 * time.Hour},
	}
	lookup := func(context.Context, uuid.UUID) (string, string, bool) { return testConfirmTitle, path, true }
	return f, lookup, cid
}

func serveConfirm(ci commentIdentity, lookup commentEntityLookup, r *http.Request) *httptest.ResponseRecorder {
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	(&Server{}).commentConfirmHandler(ci, lookup)(w, r)
	return w
}

func confirmPost(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://site.test"+commentConfirmPrefix+"tok", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestCommentConfirm_SiteSwitchOff404(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	f.enabled = false
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		r := httptest.NewRequest(m, commentConfirmPrefix+"tok", nil)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if w := serveConfirm(f, lookup, r); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", m, w.Code)
		}
	}
	if f.peeks != 0 || f.confirms != 0 {
		t.Errorf("Peek/Confirm called while off: %d/%d", f.peeks, f.confirms)
	}
	if w := serveConfirm(nil, lookup, httptest.NewRequest(http.MethodGet, commentConfirmPrefix+"tok", nil)); w.Code != http.StatusNotFound {
		t.Errorf("nil identity: status %d, want 404", w.Code)
	}
}

func TestCommentConfirm_GetDoesNotConsume(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		w := serveConfirm(f, lookup, httptest.NewRequest(m, commentConfirmPrefix+"tok", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", m, w.Code)
		}
		if m == http.MethodGet && !strings.Contains(w.Body.String(), `action="`+commentConfirmPrefix+`tok"`) {
			t.Errorf("GET body lacks the POST form: %s", w.Body.String())
		}
		for k, want := range map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer"} {
			if got := w.Header().Get(k); got != want {
				t.Errorf("%s %s = %q, want %q", m, k, got, want)
			}
		}
	}
	if f.confirms != 0 {
		t.Errorf("Confirm called by GET/HEAD %d times", f.confirms)
	}
}

func TestCommentConfirm_PostSetsCookieAndRedirects(t *testing.T) {
	for _, origin := range []string{"", "null"} {
		f, lookup, cid := newCommentFixture("/post")
		hdr := map[string]string{"Sec-Fetch-Site": "same-origin"}
		if origin != "" {
			hdr["Origin"] = origin
		}
		r := confirmPost(hdr)
		r.TLS = &tls.ConnectionState{}
		w := serveConfirm(f, lookup, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("origin %q: status %d", origin, w.Code)
		}
		if got, want := w.Header().Get("Location"), "/post#comment-"+cid.String(); got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		cs := w.Result().Cookies()
		if len(cs) != 1 || cs[0].Name != "hm_c" {
			t.Fatalf("cookies = %v", cs)
		}
		c := cs[0]
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 90*24*3600 {
			t.Errorf("cookie attributes: %+v", c)
		}
		if f.confirms != 1 {
			t.Errorf("Confirm calls = %d", f.confirms)
		}
	}
}

func TestCommentConfirm_CrossSitePostRejected(t *testing.T) {
	cases := []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{},
		{"Sec-Fetch-Site": "same-origin", "Origin": "https://other.test"},
	}
	for _, hdr := range cases {
		f, lookup, _ := newCommentFixture("/post")
		if w := serveConfirm(f, lookup, confirmPost(hdr)); w.Code != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", hdr, w.Code)
		}
		if f.confirms != 0 {
			t.Errorf("%v: Confirm called", hdr)
		}
	}
}

func TestCommentConfirm_ReplayedTokenNeutral(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	f.confirmErr = identity.ErrTokenInvalid
	w := serveConfirm(f, lookup, confirmPost(map[string]string{"Sec-Fetch-Site": "same-origin"}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Error("cookie set for an invalid link")
	}
	if strings.Contains(w.Body.String(), testConfirmTitle) || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("body: %s", w.Body.String())
	}
	f.confirmErr = errors.New("boom")
	if w := serveConfirm(f, lookup, confirmPost(map[string]string{"Sec-Fetch-Site": "same-origin"})); w.Code != http.StatusInternalServerError {
		t.Errorf("other error: status %d, want 500", w.Code)
	}
}

func TestCommentConfirm_EmptyPathRedirectsHome(t *testing.T) {
	f, lookup, cid := newCommentFixture("")
	w := serveConfirm(f, lookup, confirmPost(map[string]string{"Sec-Fetch-Site": "same-origin"}))
	if got, want := w.Header().Get("Location"), "/#comment-"+cid.String(); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}
