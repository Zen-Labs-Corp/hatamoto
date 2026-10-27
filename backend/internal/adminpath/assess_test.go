package adminpath

import (
	"context"
	"strings"
	"testing"
)

type fakeSite struct {
	serves       map[string]string // first segment -> reason
	name, domain string
}

func (f fakeSite) Serves(_ context.Context, p string) (bool, string) {
	seg := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0]
	r, ok := f.serves[seg]
	return ok, r
}
func (f fakeSite) Name() string   { return f.name }
func (f fakeSite) Domain() string { return f.domain }

func TestNormalize(t *testing.T) {
	if got, err := Normalize("  /Admin-Zone "); err != nil || got != "/admin-zone" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "admin", "/a/b", "/abc", "/" + strings.Repeat("a", 65), "/-abcd", "/abcd-", "/ab_cd", "/ab cd", "/ab.cd", "//abcd"} {
		if got, err := Normalize(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
		if lvl := Assess(context.Background(), bad, nil).Level; lvl != LevelInvalid {
			t.Errorf("%q assessed %v, want invalid", bad, lvl)
		}
	}
	if _, err := Normalize("/" + strings.Repeat("a", 64)); err != nil {
		t.Errorf("64 chars refused: %v", err)
	}
}

func TestNearMisses(t *testing.T) {
	for _, p := range []string{"/admin2", "/My-Admin", "/site-login", "/admin-panel2", "/login-2026", "/my-admin-panel2", "/my-cpanel2026", "/my-controlpanel", "/site-adminpanel",
		"/september2026", "/strawberry12", "/december-2025", "/password1234", "/manchester-2024", "/sweetheart99"} {
		if got := Assess(context.Background(), p, nil); got.Level != LevelWeak {
			t.Errorf("%s: %v %+v, want weak", p, got.Level, got.Reasons)
		}
	}
	if got := Assess(context.Background(), "/blue-kettle", nil); got.Level != LevelOK {
		t.Errorf("/blue-kettle: %v %+v, want ok", got.Level, got.Reasons)
	}
	if got := Assess(context.Background(), "/Blue-Kettle", nil); got.Path != "/blue-kettle" {
		t.Errorf("path not normalised: %q", got.Path)
	}
}

func TestShortAndSiteName(t *testing.T) {
	ctx := context.Background()
	if got := Assess(ctx, "/k7qm2xr", nil); got.Level != LevelWeak || !hasCode(got, "short") {
		t.Errorf("short: %v %+v", got.Level, got.Reasons)
	}
	site := fakeSite{name: "Acme Bakery", domain: "www.sunnybake.example"}
	if got := Assess(ctx, "/x9-acmebakery-7q", site); got.Level != LevelWeak || !hasCode(got, "site-name") {
		t.Errorf("site name: %v %+v", got.Level, got.Reasons)
	}
	if got := Assess(ctx, "/q7-sunnybake-x2", site); got.Level != LevelWeak || !hasCode(got, "site-name") {
		t.Errorf("domain: %v %+v", got.Level, got.Reasons)
	}
	// A name under 4 characters is not matched.
	if got := Assess(ctx, "/k7qm-ab-x2rf-9tdc", fakeSite{name: "ab"}); got.Level != LevelStrong {
		t.Errorf("short name matched: %v %+v", got.Level, got.Reasons)
	}
}

func TestReservedFixedAndSite(t *testing.T) {
	ctx := context.Background()
	for _, r := range ReservedPrefixes {
		if got := Assess(ctx, "/"+r, nil); got.Level != LevelReserved {
			t.Errorf("/%s: %v, want reserved", r, got.Level)
		}
	}
	if got := Assess(ctx, "/API", nil); got.Level != LevelReserved {
		t.Errorf("/API: %v", got.Level)
	}
	if got := Assess(ctx, "/api/x", nil); got.Level != LevelInvalid {
		t.Errorf("/api/x: %v", got.Level)
	}
	site := fakeSite{serves: map[string]string{
		"about-us": "the page /about-us",
		"old-news": "a redirect from /old-news",
	}}
	for p, want := range map[string]string{"/about-us": "page", "/old-news": "redirect"} {
		got := Assess(ctx, p, site)
		if got.Level != LevelReserved || len(got.Reasons) == 0 || !strings.Contains(got.Reasons[0].Text, want) {
			t.Errorf("%s: %v %+v", p, got.Level, got.Reasons)
		}
	}
	if got := Assess(ctx, "/admin", nil); got.Level != LevelWeak {
		t.Errorf("/admin: %v, want weak (not reserved)", got.Level)
	}
}

func TestStrongLongRandom(t *testing.T) {
	got := Assess(context.Background(), "/k7qm-x2rf-9tdc", nil)
	if got.Level != LevelStrong || got.Bits < minStrongBits {
		t.Errorf("%v %.1f bits %+v", got.Level, got.Bits, got.Reasons)
	}
	for _, p := range []string{"/aaaaaaaaaaaaaaaa", "/abcdefghijklmnop", "/1234567890123456"} {
		if got := Assess(context.Background(), p, nil); got.Level == LevelStrong {
			t.Errorf("%s strong at %.1f bits", p, got.Bits)
		}
	}
}

func TestNoDashes(t *testing.T) {
	if strings.ContainsAny(wordsRaw, "\u2013\u2014") {
		t.Error("words.txt has an em or en dash")
	}
}

func TestPluginUIReserved(t *testing.T) {
	if got := Assess(context.Background(), "/plugin-ui", nil); got.Level != LevelReserved {
		t.Errorf("/plugin-ui: %v, want reserved", got.Level)
	}
}
