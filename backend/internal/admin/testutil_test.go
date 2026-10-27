package admin

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/web"
)

func httptestReq(method, target, token string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	} else {
		// Signed out: the pre-session cookie a browser holds after loading a form.
		// newPreID does not read the Admin, so none is needed here.
		req.AddCookie(&http.Cookie{Name: "hm_pre", Value: (*Admin)(nil).newPreID(time.Now().Add(time.Hour))})
	}
	setFetchMeta(req, csrfValue)
	return req
}

// setFetchMeta sets what a browser sends for req: Sec-Fetch-* metadata by kind
// of request (unsafe method or HX-Request: a same-origin fetch carrying the
// token; otherwise a top-level navigation).
func setFetchMeta(req *http.Request, csrf string) {
	h := req.Header
	h.Set("Sec-Fetch-Site", "same-origin")
	if req.Method != http.MethodGet && req.Method != http.MethodHead || h.Get("HX-Request") != "" {
		h.Set("Sec-Fetch-Mode", "cors")
		h.Set("Sec-Fetch-Dest", "empty")
		h.Set("X-CSRF-Token", csrf)
		return
	}
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Sec-Fetch-Dest", "document")
}

// addFetchMeta makes req protocol-complete: hdr's HX-Request is applied first
// (it decides navigation vs fetch), then the metadata and token for the session
// behind sessionToken, a pre-session cookie when signed out, and finally hdr as
// overrides (an empty value deletes the header).
func (g *rig) addFetchMeta(req *http.Request, sessionToken string, hdr map[string]string) {
	g.t.Helper()
	if v := hdr["HX-Request"]; v != "" {
		req.Header.Set("HX-Request", v)
	}
	csrf := g.a.preToken("")
	if s, ok := g.sessions.byToken[sessionToken]; ok {
		csrf = g.a.sessionToken(s.ID)
	}
	if sessionToken == "" {
		pre := g.a.newPreID(time.Now().Add(time.Hour))
		req.AddCookie(&http.Cookie{Name: "hm_pre", Value: pre})
		csrf = g.a.preToken(pre)
	}
	setFetchMeta(req, csrf)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
}

// postJSON posts a JSON body; header overrides follow rig.do (empty deletes).
func (g *rig) postJSON(path, token, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	}
	g.addFetchMeta(req, token, hdr)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

// browserForm is a form POST the way a browser sends it under Referrer-Policy
// no-referrer: a same-origin form navigation with Origin "null", the token in
// the _csrf field and none in a header. A signed-in request (sessionToken
// known to the rig) carries the session token, a signed-out one the
// pre-session token of the hm_pre cookie holding pre. hdr overrides last (an
// empty value deletes the header).
func (g *rig) browserForm(target, sessionToken, pre string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	g.t.Helper()
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	if s, ok := g.sessions.byToken[sessionToken]; ok {
		f.Set("_csrf", g.a.sessionToken(s.ID))
	} else {
		f.Set("_csrf", g.a.preToken(pre))
	}
	req := httptest.NewRequest("POST", target, strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Origin", "null")
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: sessionToken})
	}
	if pre != "" {
		req.AddCookie(&http.Cookie{Name: "hm_pre", Value: pre})
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func httptestRec() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// serve runs h with pre, if given, applied to the response writer first (as the
// global middleware would before admin handles the request).
func serve(h http.Handler, req *http.Request, pre func(http.ResponseWriter)) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	if pre != nil {
		pre(rec)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func copyTree(t *testing.T, dst string) error {
	t.Helper()
	src := web.Admin()
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func TestRigSendsFetchMetadata(t *testing.T) {
	g := newRig(t)
	var got *http.Request
	g.h = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r })
	send := func(method string, hdr map[string]string) http.Header {
		t.Helper()
		got = nil
		g.do(method, "/admin/entities", "owner-token", nil, hdr)
		if got == nil {
			t.Fatal("request not served")
		}
		return got.Header
	}
	want := func(name string, h http.Header, site, mode, dest string, token bool) {
		t.Helper()
		if h.Get("Sec-Fetch-Site") != site || h.Get("Sec-Fetch-Mode") != mode || h.Get("Sec-Fetch-Dest") != dest {
			t.Errorf("%s: fetch metadata %q/%q/%q", name, h.Get("Sec-Fetch-Site"), h.Get("Sec-Fetch-Mode"), h.Get("Sec-Fetch-Dest"))
		}
		if (h.Get("X-CSRF-Token") != "") != token {
			t.Errorf("%s: token header %q, want present=%v", name, h.Get("X-CSRF-Token"), token)
		}
	}
	want("nav GET", send(http.MethodGet, nil), "same-origin", "navigate", "document", false)
	want("htmx GET", send(http.MethodGet, map[string]string{"HX-Request": "true"}), "same-origin", "cors", "empty", true)
	want("POST", send(http.MethodPost, nil), "same-origin", "cors", "empty", true)
	h := send(http.MethodGet, map[string]string{"Sec-Fetch-Mode": ""})
	if _, ok := h["Sec-Fetch-Mode"]; ok {
		t.Error("empty override must delete the header")
	}
	if h = send(http.MethodPost, map[string]string{"X-CSRF-Token": ""}); h.Get("X-CSRF-Token") != "" {
		t.Error("empty override must delete the token")
	}
	if c, err := got.Cookie("hm_pre"); err == nil {
		t.Errorf("signed-in request carries hm_pre %q", c.Value)
	}
	g.do(http.MethodGet, "/admin/login", "", nil, nil)
	if _, err := got.Cookie("hm_pre"); err != nil {
		t.Errorf("signed-out request lacks hm_pre: %v", err)
	}
}
