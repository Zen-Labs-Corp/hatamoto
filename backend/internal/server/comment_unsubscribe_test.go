package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
)

type fakeUnsubscriber struct {
	unsubscribed []uuid.UUID
	muted        map[uuid.UUID]bool
	calls        int
}

func (f *fakeUnsubscriber) UnsubscribeReplies(_ context.Context, id uuid.UUID) (int64, error) {
	f.calls++
	f.unsubscribed = append(f.unsubscribed, id)
	return 1, nil
}

func (f *fakeUnsubscriber) SetModeratorMuted(_ context.Context, id uuid.UUID, muted bool) error {
	f.calls++
	if f.muted == nil {
		f.muted = map[uuid.UUID]bool{}
	}
	f.muted[id] = muted
	return nil
}

var unsubscribeTestKey = comment.UnsubscribeKey("unsubscribe-test-secret")

func unsubscribeServe(store commentUnsubscriber, method, token, body string) *httptest.ResponseRecorder {
	h := commentUnsubscribeHandler(store, unsubscribeTestKey,
		func(context.Context) (string, string, string) { return "Site", "https://site.test", "/admin" },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRequest(method, commentUnsubscribePrefix+token, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestCommentUnsubscribeGetNeverMutates(t *testing.T) {
	id := uuid.New()
	tok := comment.UnsubscribeToken(unsubscribeTestKey, comment.UnsubscribeReplies, id)
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		f := &fakeUnsubscriber{}
		w := unsubscribeServe(f, m, tok, "")
		if w.Code != http.StatusOK || f.calls != 0 {
			t.Fatalf("%s: code %d, store calls %d", m, w.Code, f.calls)
		}
		if m == http.MethodGet && !strings.Contains(w.Body.String(), `action="`+commentUnsubscribePrefix+tok+`"`) {
			t.Errorf("GET page has no form to the token: %s", w.Body.String())
		}
		if m == http.MethodHead && w.Body.Len() != 0 {
			t.Errorf("HEAD wrote a body")
		}
		h := w.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s headers = %v", m, h)
		}
	}
}

func TestCommentUnsubscribePost(t *testing.T) {
	cid, uid := uuid.New(), uuid.New()
	rTok := comment.UnsubscribeToken(unsubscribeTestKey, comment.UnsubscribeReplies, cid)
	mTok := comment.UnsubscribeToken(unsubscribeTestKey, comment.UnsubscribeModerator, uid)

	f := &fakeUnsubscriber{}
	if w := unsubscribeServe(f, http.MethodPost, rTok, "List-Unsubscribe=One-Click"); w.Code != http.StatusOK {
		t.Fatalf("one-click POST = %d", w.Code)
	}
	if len(f.unsubscribed) != 1 || f.unsubscribed[0] != cid {
		t.Errorf("unsubscribed = %v, want [%s]", f.unsubscribed, cid)
	}

	f = &fakeUnsubscriber{}
	w := unsubscribeServe(f, http.MethodPost, mTok, "List-Unsubscribe=One-Click")
	if !f.muted[uid] || !strings.Contains(w.Body.String(), "Turn them back on") {
		t.Errorf("moderator POST: muted=%v body=%s", f.muted, w.Body.String())
	}

	unsubscribeServe(f, http.MethodPost, mTok, "action=resubscribe")
	if muted, ok := f.muted[uid]; !ok || muted {
		t.Errorf("resubscribe left muted = %v", f.muted[uid])
	}

	f = &fakeUnsubscriber{}
	unsubscribeServe(f, http.MethodPost, rTok, "action=resubscribe")
	if len(f.unsubscribed) != 1 || len(f.muted) != 0 {
		t.Errorf("a commenter cannot resubscribe: unsubscribed=%v muted=%v", f.unsubscribed, f.muted)
	}
}

func TestCommentUnsubscribeBadTokenAndBody(t *testing.T) {
	f := &fakeUnsubscriber{}
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if w := unsubscribeServe(f, m, "nonsense", "List-Unsubscribe=One-Click"); w.Code != http.StatusNotFound ||
			!strings.Contains(w.Body.String(), "This link is not valid.") {
			t.Errorf("%s bad token = %d %q", m, w.Code, w.Body.String())
		}
	}
	other := comment.UnsubscribeToken(comment.UnsubscribeKey("another secret"), comment.UnsubscribeReplies, uuid.New())
	if w := unsubscribeServe(f, http.MethodPost, other, ""); w.Code != http.StatusNotFound {
		t.Errorf("token of another key = %d", w.Code)
	}
	if f.calls != 0 {
		t.Errorf("store touched %d times by invalid requests", f.calls)
	}
	tok := comment.UnsubscribeToken(unsubscribeTestKey, comment.UnsubscribeReplies, uuid.New())
	w := unsubscribeServe(f, http.MethodPost, tok, "x="+strings.Repeat("a", 2048))
	if w.Code != http.StatusRequestEntityTooLarge || f.calls != 0 {
		t.Errorf("big body = %d, store calls %d", w.Code, f.calls)
	}
	if w := unsubscribeServe(f, http.MethodPut, tok, ""); w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Errorf("PUT = %d allow %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestCommentUnsubscribeTokenIsRedacted(t *testing.T) {
	if got := redactPreviewToken(commentUnsubscribePrefix + "X"); got != commentUnsubscribePrefix+"<token>" {
		t.Errorf("redactPreviewToken = %q", got)
	}
}

func TestCommentUnsubscribeRoutes(t *testing.T) {
	f := newRenderFixture(t)
	f.next = func() (*routeResult, pageOutcome, error) {
		return &routeResult{NotFound: true}, pageOutcome{Status: 404, NotFound: true}, nil
	}
	r, h := baseRouter()
	mountRenderRoutes(r, renderRoutes{
		pages: f.h, assets: stub("assets"), robots: stub("robots"), sitemap: stub("sitemap"),
		subSitemap: stub("sub"), llms: stub("llms"), xsl: stub("xsl"), formPost: stub("form"),
		commentUnsubscribe: stub("unsubscribe"), commentPost: stub("post"),
	})
	for _, c := range []struct{ method, path, want string }{
		{"GET", commentUnsubscribePrefix + "abc", "unsubscribe"},
		{"POST", commentUnsubscribePrefix + "abc", "unsubscribe"},
		{"POST", "/-/comments/" + uuid.NewString(), "post"},
	} {
		if got := commentRoutesDo(h, c.method, c.path).Body.String(); got != c.want {
			t.Errorf("%s %s -> %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
