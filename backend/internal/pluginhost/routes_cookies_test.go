package pluginhost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestPublicRouteCookies(t *testing.T) {
	r := newCapaRig(t)
	r.load(t, nil)
	public := r.rt.PublicRoutes()
	serve := func(url string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", url, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		public.Serve(rec, req, "capa", "ck", nil)
		return rec
	}

	// Declared cookies arrive by short name; undeclared and core ones do not.
	rec := serve("/p/capa/ck", &http.Cookie{Name: "hmp_capa_unlock", Value: "1"}, &http.Cookie{Name: "hmp_capa_other", Value: "x"},
		&http.Cookie{Name: "hmp_capa_visited", Value: "y"}, &http.Cookie{Name: "hatamoto_session", Value: "secret"}, &http.Cookie{Name: "unlock", Value: "raw"})
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 || got["unlock"] != "1" || got["visited"] != "y" {
		t.Fatalf("cookies seen by the plugin: %v (%v) %s", got, err, rec.Body)
	}

	// Set: namespaced, with render.page's flags, never cached.
	rec = serve("/p/capa/ck?set=unlock")
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "hmp_capa_unlock" || cs[0].Value != "v-unlock" || cs[0].MaxAge != 60 || !cs[0].HttpOnly || !cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode || cs[0].Path != "/" {
		t.Fatalf("Set-Cookie = %v", rec.Header()["Set-Cookie"])
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	// An undeclared name fails the response.
	if rec = serve("/p/capa/ck?set=sneaky"); rec.Code != http.StatusBadGateway || len(rec.Result().Cookies()) != 0 {
		t.Errorf("undeclared cookie: %d %v", rec.Code, rec.Header())
	}
	// More than the limit fails it.
	if rec = serve("/p/capa/ck?set=unlock&set=unlock&set=unlock&set=unlock&set=unlock&set=unlock&set=unlock&set=unlock&set=unlock"); rec.Code != http.StatusBadGateway {
		t.Errorf("too many cookies: %d", rec.Code)
	}
}

func TestRouteCookieValueLimits(t *testing.T) {
	m := sdk.Manifest{Name: "capa", Cookies: []string{"a"}}
	for name, c := range map[string]sdk.SetCookie{
		"huge":   {Name: "a", Value: strings.Repeat("x", maxCookieValue+1)},
		"space":  {Name: "a", Value: "a b"},
		"age":    {Name: "a", Value: "1", MaxAge: maxCookieAge + 1},
		"path":   {Name: "a", Value: "1", Path: "x"},
		"undecl": {Name: "b", Value: "1"},
	} {
		if checkSetCookies(m, []sdk.SetCookie{c}) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := checkSetCookies(m, []sdk.SetCookie{{Name: "a", Value: "ok", MaxAge: 5, Path: "/x"}}); err != nil {
		t.Error(err)
	}
}
