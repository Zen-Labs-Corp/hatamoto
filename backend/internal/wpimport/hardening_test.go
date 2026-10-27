package wpimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSafeMenuURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"/about": true, "https://x.test/a": true, "http://x.test": true, "mailto:a@b.test": true, "tel:+123": true, "#top": true, "?p=1": true,
		"javascript:alert(1)": false, "JaVaScRiPt:alert(1)": false, "data:text/html,x": false, "//evil.test/x": false, `/\evil.test`: false, "": false, "vbscript:x": false,
	} {
		if got := safeMenuURL(raw); got != want {
			t.Errorf("safeMenuURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestSiteKeySeparatesSubsitesOnOneHost(t *testing.T) {
	if siteKey("https://Example.com/a/") == siteKey("https://example.com/b") {
		t.Error("two sub-sites share a key")
	}
	if got := siteKey("https://Example.com/"); got != "example.com" {
		t.Errorf("siteKey = %q", got)
	}
	if got := KeyPrefix("https://old.example.test"); got != "wp:old.example.test:" {
		t.Errorf("prefix = %q (must stay compatible with earlier runs)", got)
	}
}

func TestIsStarterContent(t *testing.T) {
	for attrs, want := range map[string]bool{
		`{"starter":true,"content":"x"}`:                    true,
		`{"page_sections":[{"id":"welcome"}]}`:              true,
		`{"page_sections":[{"id":"welcome"},{"id":"b"}]}`:   false,
		`{"content":"<p>mine</p>"}`:                         false,
		`{"starter":false,"page_sections":[{"id":"hero"}]}`: false,
		`{"page_doc":{"v":2,"nodes":[{"id":"c-welcome","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"welcome","t":"component","p":{"type":"hero","content":{}}}]}]}}`: true,
		`{"page_doc":{"v":2,"nodes":[{"id":"c-x","t":"container","p":{"tag":"section","v1":true},"c":[{"id":"x","t":"component","p":{"type":"hero","content":{}}}]}]}}`:             false,
		`not json`: false,
	} {
		if got := isStarterContent([]byte(attrs)); got != want {
			t.Errorf("isStarterContent(%s) = %v", attrs, got)
		}
	}
}

type fixedProber map[string][2]any // path -> status, location

func (f fixedProber) Probe(_ context.Context, p string) (int, string, error) {
	v := f[p]
	if v[0] == nil {
		return 404, "", nil
	}
	return v[0].(int), v[1].(string), nil
}

func (f fixedProber) Hosts() []string { return []string{"new.test"} }

func TestAuditFailsOnRedirectOffTheSite(t *testing.T) {
	p := fixedProber{
		"/a": {301, "/b"}, "/b": {200, ""},
		"/c": {301, "https://new.test/b"},
		"/d": {301, "https://old.example.test/b"},
		"/e": {301, "https://evil.test/b"},
	}
	res := Audit(context.Background(), "https://old.example.test", []string{
		"https://old.example.test/a", "https://old.example.test/c", "https://old.example.test/d", "https://old.example.test/e"}, p)
	if res.Redirected != 3 || len(res.Failures) != 1 || res.Failures[0].URL != "/e" {
		t.Errorf("audit = %+v", res)
	}
}

func TestBasicAuthOnlyOverTheSitesScheme(t *testing.T) {
	var got string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { u, _, _ := r.BasicAuth(); got = u })
	srv := httptest.NewServer(h)
	defer srv.Close()
	c, err := NewClient(ClientOptions{URL: srv.URL, User: "u", AppPassword: "p", AllowPrivate: true, RatePerSec: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Do(context.Background(), srv.URL+"/x", 10); err != nil || got != "u" {
		t.Fatalf("same scheme: %q %v", got, err)
	}
	got = ""
	c.Base.Scheme = "https" // the site is https; this URL is plain http on the same host
	if _, _, err := c.Do(context.Background(), srv.URL+"/x", 10); err != nil || got != "" {
		t.Errorf("credentials sent in clear over http to an https site: %q %v", got, err)
	}
}
