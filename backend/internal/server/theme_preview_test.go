package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewTokenSignatureAndExpiry(t *testing.T) {
	p := &themePreview{key: []byte("k1")}
	now := time.Now()
	tok := p.Token("sunrise", now)
	if theme, ok := p.verify(tok, now); !ok || theme != "sunrise" {
		t.Fatalf("verify = %q, %v", theme, ok)
	}
	if _, ok := p.verify(tok, now.Add(previewTTL+time.Minute)); ok {
		t.Error("an expired token verified")
	}
	other := &themePreview{key: []byte("k2")}
	if _, ok := other.verify(tok, now); ok {
		t.Error("a token signed with another key verified")
	}
	enc, sig, _ := strings.Cut(tok, ".")
	if _, ok := p.verify(enc+"A."+sig, now); ok {
		t.Error("a tampered payload verified")
	}
	for _, bad := range []string{"", "x", "a.b", "off"} {
		if _, ok := p.verify(bad, now); ok {
			t.Errorf("%q verified", bad)
		}
	}
}

func TestPreviewStartSetsCookieAndRedirects(t *testing.T) {
	p := &themePreview{key: []byte("k")}
	tok := p.Token("sunrise", time.Now())

	w := httptest.NewRecorder()
	if !p.start(w, httptest.NewRequest(http.MethodGet, "/blog?x=1&"+previewParam+"="+tok, nil)) {
		t.Fatal("start did not answer")
	}
	res := w.Result()
	c := res.Cookies()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/blog?x=1" || len(c) != 1 || !c[0].HttpOnly || c[0].MaxAge <= 0 {
		t.Fatalf("status %d location %q cookies %+v", res.StatusCode, res.Header.Get("Location"), c)
	}
	if !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Errorf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}

	// A forged token or "off" clears instead of setting.
	for _, v := range []string{"forged.value", "off"} {
		w := httptest.NewRecorder()
		p.start(w, httptest.NewRequest(http.MethodGet, "/?"+previewParam+"="+v, nil))
		if c := w.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
			t.Errorf("%s: cookie %+v", v, c)
		}
	}
	if p.start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/blog", nil)) {
		t.Error("a request without the parameter was answered")
	}
	var none *themePreview
	if none.start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?"+previewParam+"=x", nil)) || none.engine(httptest.NewRequest(http.MethodGet, "/", nil)) != nil {
		t.Error("a nil preview must be inert")
	}
}
