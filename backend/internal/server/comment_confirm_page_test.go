package server

import (
	"hatamoto.dev/core/internal/comment/identity"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func confirmPageHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for k, want := range map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer"} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func themedConfirmServer(t *testing.T) *Server {
	t.Helper()
	return &Server{renderHandlers: newRenderFixture(t).h}
}

func TestCommentConfirm_ThemedPage(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	s := themedConfirmServer(t)
	r := httptest.NewRequest(http.MethodGet, commentConfirmPrefix+"tok", nil)
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	s.commentConfirmHandler(f, lookup)(w, r)

	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `class="hm-error"`) ||
		!strings.Contains(body, `action="`+commentConfirmPrefix+`tok"`) || !strings.Contains(body, testConfirmTitle) ||
		!strings.Contains(body, "Confirm") {
		t.Fatalf("status %d, body %s", w.Code, body)
	}
	confirmPageHeaders(t, w)
	if f.confirms != 0 {
		t.Error("GET consumed the link")
	}
}

func TestCommentConfirm_ThemedInvalidPage(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	f.peekErr = identity.ErrTokenInvalid
	s := themedConfirmServer(t)
	r := httptest.NewRequest(http.MethodGet, commentConfirmPrefix+"tok", nil)
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	s.commentConfirmHandler(f, lookup)(w, r)

	body := w.Body.String()
	if w.Code != http.StatusNotFound || !strings.Contains(body, `class="hm-error"`) || strings.Contains(body, "<form") {
		t.Fatalf("status %d, body %s", w.Code, body)
	}
	if !strings.Contains(body, "expired") && !strings.Contains(body, "no longer") {
		t.Errorf("no expired text: %s", body)
	}
	confirmPageHeaders(t, w)
}

func TestCommentConfirm_ThemedHeadHasNoBody(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	s := themedConfirmServer(t)
	r := httptest.NewRequest(http.MethodHead, commentConfirmPrefix+"tok", nil)
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	s.commentConfirmHandler(f, lookup)(w, r)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("HEAD: status %d, %d body bytes", w.Code, w.Body.Len())
	}
	confirmPageHeaders(t, w)
}

func TestCommentConfirm_FallbackWithoutThemedEngine(t *testing.T) {
	f, lookup, _ := newCommentFixture("/post")
	r := httptest.NewRequest(http.MethodGet, commentConfirmPrefix+"tok", nil)
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	(&Server{}).commentConfirmHandler(f, lookup)(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "hm-error") ||
		!strings.Contains(w.Body.String(), `action="`+commentConfirmPrefix+`tok"`) {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	confirmPageHeaders(t, w)
}

// The token is in the URL: the page may print it only in the form action, and
// no script may run that could read location.href.
func TestCommentConfirm_ThemedPageKeepsTokenOutOfUrlsAndRunsNoScript(t *testing.T) {
	const tok = "SECRETTOKEN"
	f, lookup, _ := newCommentFixture("/post")
	s := themedConfirmServer(t)
	r := httptest.NewRequest(http.MethodGet, commentConfirmPrefix+tok, nil)
	r.SetPathValue("token", tok)
	w := httptest.NewRecorder()
	s.commentConfirmHandler(f, lookup)(w, r)

	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `action="`+commentConfirmPrefix+tok+`"`) {
		t.Fatalf("status %d, body %s", w.Code, body)
	}
	// JSON-LD is inert data; no executable script may be on the page.
	if strings.Count(body, "<script") != strings.Count(body, `<script type="application/ld+json">`) {
		t.Errorf("confirm page runs a script:\n%s", body)
	}
	if got := strings.Count(body, tok); got != 1 {
		t.Errorf("token printed %d times, want only the form action:\n%s", got, body)
	}
}
