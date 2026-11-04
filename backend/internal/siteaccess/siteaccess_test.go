package siteaccess

import (
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	key := []byte("k")
	now := time.Unix(1_800_000_000, 0)
	v := Sign(key, 3, now)
	if !Verify(key, v, 3, now.Add(time.Hour)) {
		t.Fatal("round trip failed")
	}
	if Verify(key, v, 4, now) {
		t.Fatal("other epoch accepted")
	}
	if Verify(key, v, 3, now.Add(CookieTTL+time.Second)) {
		t.Fatal("expired accepted")
	}
	if Verify([]byte("other"), v, 3, now) {
		t.Fatal("other key accepted")
	}
	tampered := "v1.9999999999.3." + v[len(v)-43:]
	for _, bad := range []string{"", "x", v + "x", tampered, "v1.1.3.sig"} {
		if Verify(key, bad, 3, now) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestBypass(t *testing.T) {
	cases := []struct {
		path, method string
		want         bool
	}{
		{"/health", "GET", true}, {"/ready", "GET", true},
		{"/api/v1/pages", "GET", true}, {"/api/v1/public/forms/x/submit", "POST", false},
		{"/api/v1/public/forms/x/submit", "OPTIONS", true}, {"/api/v1/public/forms/x/token", "POST", true},
		{"/forms/contact/submit", "POST", false}, {"/forms/contact/submit", "GET", false},
		{"/p/plugin/x", "GET", true}, {"/.well-known/security.txt", "GET", true},
		{"/media/a.png", "GET", true}, {"/theme/assets/a.css", "GET", true},
		{"/favicon.ico", "GET", true}, {"/auth/login", "POST", true},
		{"/", "GET", false}, {"/about", "GET", false}, {"/sitemap.xml", "GET", false},
		{"/robots.txt", "GET", false}, {"/apiary", "GET", false}, {"/-/site-access", "POST", false},
	}
	for _, c := range cases {
		if got := Bypass(c.path, c.method); got != c.want {
			t.Errorf("Bypass(%q,%s)=%v want %v", c.path, c.method, got, c.want)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/about?x=1": "/about?x=1", "": "/", "about": "/", "//evil.test": "/",
		"https://evil.test": "/", "/\\evil.test": "/", "/-/site-access": "/", "/a\r\nb": "/",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q)=%q want %q", in, got, want)
		}
	}
}
