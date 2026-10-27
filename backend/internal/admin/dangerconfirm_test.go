package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
)

func TestCheckDangerPhraseHelper(t *testing.T) {
	t.Parallel()

	dc := ui.DangerConfirm{Phrase: "/admin"}
	post := func(phrase string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(url.Values{"confirm_phrase": {phrase}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	for _, c := range []struct {
		name, typed string
		phrase      string
		ok          bool
	}{
		{"exact", "/admin", "/admin", true},
		{"wrong case", "/ADMIN", "/admin", false},
		{"trailing space", "/admin ", "/admin", false},
		{"leading space", " /admin", "/admin", false},
		{"empty typed", "", "/admin", false},
		{"empty phrase never matches", "", "", false},
	} {
		d := dc
		d.Phrase = c.phrase
		got, ok := CheckDangerPhrase(post(c.typed), d)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
		}
		if !ok && got.Error == "" {
			t.Errorf("%s: a failed check must set Error", c.name)
		}
		if ok && got.Error != "" {
			t.Errorf("%s: a passed check must not set Error", c.name)
		}
	}
	if _, ok := CheckDangerPhrase(httptest.NewRequest(http.MethodPost, "/x", nil), dc); ok {
		t.Error("a missing confirm_phrase must fail")
	}
}

// A screen calls the helper and re-renders with 422 on a mismatch; this is the
// smallest such screen, behind the real CSRF and session middleware.
func TestDangerPhraseScreen(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	card := func() ui.DangerConfirm {
		return ui.DangerConfirm{ID: "dc", Action: "/t/danger", Back: "/t", Phrase: "/admin", Title: "Sure?", Prompt: "Type /admin below.", Label: "Do it"}
	}
	g.h.(*http.ServeMux).Handle("POST /t/danger", g.a.guardFor(scopeAdmin)(g.a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dc, ok := CheckDangerPhrase(r, card())
		if ok {
			_, _ = w.Write([]byte("done"))
			return
		}
		out, err := g.a.RenderPartial("hm/danger-confirm", dc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(out))
	}))))

	post := func(form url.Values) *httptest.ResponseRecorder {
		return g.do("POST", "/t/danger", "owner-token", form, nil)
	}
	for name, typed := range map[string]string{"wrong": "/nope", "empty": "", "wrong case": "/ADMIN", "extra space": "/admin "} {
		rec := post(url.Values{"confirm_phrase": {typed}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), dangerMismatch) {
			t.Errorf("%s: want 422 with the error text, got %d\n%s", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `aria-invalid="true"`) {
			t.Errorf("%s: the input must be marked invalid", name)
		}
	}
	if rec := post(url.Values{}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no confirm_phrase field: want 422, got %d", rec.Code)
	}
	if rec := post(url.Values{"confirm_phrase": {"/admin"}}); rec.Code != http.StatusOK || rec.Body.String() != "done" {
		t.Errorf("exact phrase: want 200 done, got %d %q", rec.Code, rec.Body.String())
	}
}
