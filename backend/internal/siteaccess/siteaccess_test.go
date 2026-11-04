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
	for path, want := range map[string]bool{
		"/health": true, "/ready": true, "/api/v1/pages": true, "/api/v1/auth/login": true,
		"/api/v1/track": true, "/api/v1/entities/1": true,
		"/api/v1/public": false, "/api/v1/public/llms.txt": false, "/api/v1/public/sitemap.xml": false,
		"/api/v1/public/knowledge-graph": false, "/api/v1/public/terms/a/b/entities": false,
		"/api/v1/public/forms/x/submit": false, "/forms/contact/submit": false,
		"/api/other": false, "/api": false,
		"/p/plugin/x": true, "/.well-known/security.txt": true,
		"/media/a.png": true, "/theme/assets/a.css": true,
		"/favicon.ico": true, "/auth/login": true,
		"/": false, "/about": false, "/sitemap.xml": false,
		"/robots.txt": false, "/apiary": false, "/-/site-access": false,
	} {
		if got := Bypass(path); got != want {
			t.Errorf("Bypass(%q)=%v want %v", path, got, want)
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
