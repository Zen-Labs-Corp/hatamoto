package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetSessionCookie_Attributes(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "opaque-token", true)
	cs := rec.Result().Cookies()
	if len(cs) != 2 {
		t.Fatalf("expected the session cookie and the member marker, got %d cookies", len(cs))
	}
	c := cs[0]
	if c.Name != SessionCookieName || c.Value != "opaque-token" {
		t.Errorf("name/value = %s/%s", c.Name, c.Value)
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if !c.Secure {
		t.Error("expected Secure when secure=true")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want > 0", c.MaxAge)
	}
}

func TestSetSessionCookie_InsecureForDev(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "t", false)
	if rec.Result().Cookies()[0].Secure {
		t.Error("expected Secure=false when secure=false (local HTTP dev)")
	}
}

func TestClearSessionCookie_Expires(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, true)
	c := rec.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative (expired)", c.MaxAge)
	}
	if !c.HttpOnly {
		t.Error("cleared cookie must still be HttpOnly")
	}
}

func TestSessionCookieHelpersNeverNameLegacy(t *testing.T) {
	old := SessionCookieName
	t.Cleanup(func() { SessionCookieName = old })
	UseNeutralCookieNames(true)
	for _, fn := range []func(http.ResponseWriter){
		func(w http.ResponseWriter) { SetSessionCookie(w, "tok", true) },
		func(w http.ResponseWriter) { ClearSessionCookie(w, true) },
	} {
		rec := httptest.NewRecorder()
		fn(rec)
		cs := rec.Result().Cookies()
		if len(cs) != 2 || cs[0].Name != "__Host-s" || cs[1].Name != MemberMarkerName {
			t.Fatalf("cookies %v, want only __Host-s and the member marker", cs)
		}
	}
}

// A __Host- cookie needs Secure, Path=/ and no Domain, or browsers drop it and
// admin login silently breaks.
func TestNeutralSessionCookieAttributes(t *testing.T) {
	old := SessionCookieName
	t.Cleanup(func() { SessionCookieName = old })
	for _, tc := range []struct {
		secure bool
		name   string
	}{{true, "__Host-s"}, {false, "s"}} {
		UseNeutralCookieNames(tc.secure)
		set, clear := httptest.NewRecorder(), httptest.NewRecorder()
		SetSessionCookie(set, "tok", tc.secure)
		ClearSessionCookie(clear, tc.secure)
		for what, rec := range map[string]*httptest.ResponseRecorder{"set": set, "clear": clear} {
			var c *http.Cookie
			for _, got := range rec.Result().Cookies() {
				if got.Name == tc.name {
					c = got
				}
			}
			if c == nil {
				t.Fatalf("secure=%v %s: no %q cookie", tc.secure, what, tc.name)
			}
			if c.Secure != tc.secure || c.Path != "/" || c.Domain != "" || !c.HttpOnly {
				t.Errorf("secure=%v %s: Secure=%v Path=%q Domain=%q HttpOnly=%v", tc.secure, what, c.Secure, c.Path, c.Domain, c.HttpOnly)
			}
		}
	}
}

func TestAPISessionCookieNameDiffersByDefault(t *testing.T) {
	if APISessionCookieName == SessionCookieName {
		t.Fatalf("API cookie name %q must differ from the admin cookie name", APISessionCookieName)
	}
}

func TestAPISessionCookieHelpersNeverNameLegacy(t *testing.T) {
	oldS, oldA := SessionCookieName, APISessionCookieName
	t.Cleanup(func() { SessionCookieName, APISessionCookieName = oldS, oldA })
	UseNeutralCookieNames(true)
	for _, fn := range []func(http.ResponseWriter){
		func(w http.ResponseWriter) { SetAPISessionCookie(w, "tok", true) },
		func(w http.ResponseWriter) { ClearAPISessionCookie(w, true) },
	} {
		rec := httptest.NewRecorder()
		fn(rec)
		cs := rec.Result().Cookies()
		if len(cs) != 1 || cs[0].Name != "__Host-a" {
			t.Fatalf("cookies %v, want only __Host-a", cs)
		}
	}
}

func TestNeutralAPISessionCookieAttributes(t *testing.T) {
	oldS, oldA := SessionCookieName, APISessionCookieName
	t.Cleanup(func() { SessionCookieName, APISessionCookieName = oldS, oldA })
	for _, tc := range []struct {
		secure bool
		name   string
	}{{true, "__Host-a"}, {false, "a"}} {
		UseNeutralCookieNames(tc.secure)
		set, clear := httptest.NewRecorder(), httptest.NewRecorder()
		SetAPISessionCookie(set, "tok", tc.secure)
		ClearAPISessionCookie(clear, tc.secure)
		for what, rec := range map[string]*httptest.ResponseRecorder{"set": set, "clear": clear} {
			cs := rec.Result().Cookies()
			if len(cs) != 1 || cs[0].Name != tc.name {
				t.Fatalf("secure=%v %s: cookies %v, want only %q", tc.secure, what, cs, tc.name)
			}
			c := cs[0]
			if c.Secure != tc.secure || c.Path != "/" || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Errorf("secure=%v %s: Secure=%v Path=%q Domain=%q HttpOnly=%v SameSite=%v", tc.secure, what, c.Secure, c.Path, c.Domain, c.HttpOnly, c.SameSite)
			}
		}
	}
}

func TestMemberMarkerSetAndCleared(t *testing.T) {
	set, clear := httptest.NewRecorder(), httptest.NewRecorder()
	SetSessionCookie(set, "secret-token", true)
	ClearSessionCookie(clear, true)
	find := func(rec *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range rec.Result().Cookies() {
			if c.Name == MemberMarkerName {
				return c
			}
		}
		t.Fatal("no member marker cookie")
		return nil
	}
	m := find(set)
	if m.Value != "1" || m.Path != "/" || m.HttpOnly || !m.Secure || m.SameSite != http.SameSiteLaxMode || m.MaxAge <= 0 {
		t.Errorf("marker = %+v", m)
	}
	if m.Value == "secret-token" {
		t.Error("marker must not carry the token")
	}
	if c := find(clear); c.MaxAge >= 0 {
		t.Errorf("cleared marker MaxAge = %d, want negative", c.MaxAge)
	}
	insecure := httptest.NewRecorder()
	SetSessionCookie(insecure, "t", false)
	if find(insecure).Secure {
		t.Error("marker Secure must follow the session cookie")
	}
}
