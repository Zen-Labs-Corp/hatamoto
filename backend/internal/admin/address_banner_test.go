package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/adminpath"
)

const (
	bannerMarker = "data-hm-address-banner"
	keepPath     = "/admin/settings/security/admin-address/keep"
	weakPath     = "/my-place"
)

var weakState = AddressState{
	Path: weakPath, Level: adminpath.LevelWeak,
	Reasons: []adminpath.Reason{{Code: "scanner", Text: "on the scanner list"}},
}

type keepRig struct {
	*ownerRig
	fa     *fakeAddress
	mu     sync.Mutex
	events []AuditEvent
}

func newKeepRig(t *testing.T, st AddressState) *keepRig {
	t.Helper()
	g := &keepRig{ownerRig: newOwnerRig(t, true), fa: &fakeAddress{state: st, assess: assessByName}}
	g.a.deps.Address = g.fa
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.events = append(g.events, e)
	}
	return g
}

func TestAddressBannerShownToOwnerOnWeakUnacknowledged(t *testing.T) {
	t.Parallel()

	g := newKeepRig(t, weakState)
	rec := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{
		bannerMarker, "Your admin is at /my-place, the first address bots try.",
		`href="/admin/settings/security/admin-address/change"`, "Choose a private address",
		`href="` + keepPath + `"`, "Keep /my-place…",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "data-hm-callout-dismiss") && strings.Contains(sectionAround(body, bannerMarker), "data-hm-callout-dismiss") {
		t.Error("the banner must not be dismissible")
	}
	if strings.ContainsAny(body, "\u2013\u2014") {
		t.Error("em or en dash on the page")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/banner-address.html", normalizePage(body))
}

// sectionAround is the callout element that holds marker, to check it alone.
func sectionAround(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<div")
	end := strings.Index(body[i:], "</div>")
	if start < 0 || end < 0 {
		return ""
	}
	return body[start : i+end]
}

func TestAddressBannerMatrix(t *testing.T) {
	t.Parallel()

	acked := weakState
	acked.AckBy, acked.AckAt = "Ana Ruiz", chosenAt()
	pinned, demo := weakState, weakState
	pinned.Lock, pinned.Pinned = AddressLockedEnv, "HATAMOTO_ADMIN_PATH"
	demo.Lock = AddressLockedDemo
	for _, c := range []struct {
		name, method, path, token string
		st                        AddressState
		form                      url.Values
		want                      bool
	}{
		{"owner weak unacked", "GET", "/admin/settings/general", "owner-token", weakState, nil, true},
		{"HEAD", "HEAD", "/admin/settings/general", "owner-token", weakState, nil, true},
		{"editor", "GET", "/admin/profile", "editor-token", weakState, nil, false},
		{"pinned by env", "GET", "/admin/settings/general", "owner-token", pinned, nil, false},
		{"demo", "GET", "/admin/settings/general", "owner-token", demo, nil, false},
		{"acknowledged", "GET", "/admin/settings/general", "owner-token", acked, nil, false},
		{"strong", "GET", "/admin/settings/general", "owner-token", strongState, nil, false},
		{"security page", "GET", securityPath, "owner-token", weakState, nil, false},
		{"post render", "POST", "/admin/settings/general/identity", "owner-token", weakState, url.Values{"name": {""}}, false},
	} {
		g := newKeepRig(t, c.st)
		rec := g.do(c.method, c.path, c.token, c.form, nil)
		if c.method == "POST" && rec.Code != 422 {
			t.Errorf("%s: want a 422 re-render, got %d", c.name, rec.Code)
		}
		if got := strings.Contains(rec.Body.String(), bannerMarker); got != c.want && c.method != "HEAD" {
			t.Errorf("%s: banner = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAddressKeepCard(t *testing.T) {
	t.Parallel()

	g := newKeepRig(t, weakState)
	rec := g.do("GET", keepPath, "owner-token", nil, nil) // no sudo
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{
		"/my-place is the first address attack tools try", "To use this address anyway, type /my-place below.",
		`name="confirm_phrase"`, `action="` + keepPath + `"`, `href="/admin/settings/security/admin-address"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, bannerMarker) {
		t.Error("banner on the Keep page")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-security-keep.html", normalizePage(body))
}

func TestAddressKeepPost(t *testing.T) {
	t.Parallel()

	t.Run("wrong phrase", func(t *testing.T) {
		t.Parallel()
		g := newKeepRig(t, weakState)
		rec := g.do("POST", keepPath, "owner-token", url.Values{"confirm_phrase": {"my-place"}}, nil)
		if rec.Code != 422 || len(g.fa.kept) != 0 || !strings.Contains(rec.Body.String(), "The text does not match.") {
			t.Fatalf("%d kept=%v", rec.Code, g.fa.kept)
		}
	})
	t.Run("no phrase", func(t *testing.T) {
		t.Parallel()
		g := newKeepRig(t, weakState)
		if rec := g.do("POST", keepPath, "owner-token", url.Values{}, nil); rec.Code != 422 || len(g.fa.kept) != 0 {
			t.Fatalf("%d kept=%v", rec.Code, g.fa.kept)
		}
	})
	t.Run("right phrase", func(t *testing.T) {
		t.Parallel()
		g := newKeepRig(t, weakState)
		rec := g.do("POST", keepPath, "owner-token", url.Values{"confirm_phrase": {weakPath}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != securityPath {
			t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
		}
		if len(g.fa.kept) != 1 || g.fa.kept[0] != "Owner Olga" || len(g.fa.applied) != 0 {
			t.Errorf("kept %v applied %v", g.fa.kept, g.fa.applied)
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.events) != 1 || g.events[0].Action != "admin_address.keep" || g.events[0].Target != "admin-address" {
			t.Fatalf("audit %+v", g.events)
		}
		for k, v := range g.events[0].Detail {
			if s, ok := v.(string); ok && strings.Contains(s, "my-place") {
				t.Errorf("detail %s carries the address", k)
			}
		}
	})
	t.Run("keep fails", func(t *testing.T) {
		t.Parallel()
		g := newKeepRig(t, weakState)
		g.fa.keepErr = context.DeadlineExceeded
		if rec := g.do("POST", keepPath, "owner-token", url.Values{"confirm_phrase": {weakPath}}, nil); rec.Code != 500 {
			t.Fatalf("%d", rec.Code)
		}
	})
	t.Run("csrf missing", func(t *testing.T) {
		t.Parallel()
		g := newKeepRig(t, weakState)
		rec := g.do("POST", keepPath, "owner-token", url.Values{"confirm_phrase": {weakPath}}, map[string]string{"X-CSRF-Token": ""})
		if rec.Code != 403 || len(g.fa.kept) != 0 {
			t.Fatalf("%d kept=%v", rec.Code, g.fa.kept)
		}
	})
}

func TestAddressKeepEditorForbidden(t *testing.T) {
	t.Parallel()

	g := newKeepRig(t, weakState)
	if rec := g.do("GET", keepPath, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("GET %d", rec.Code)
	}
	if rec := g.do("POST", keepPath, "editor-token", url.Values{"confirm_phrase": {weakPath}}, nil); rec.Code != 403 || len(g.fa.kept) != 0 {
		t.Errorf("POST %d kept=%v", rec.Code, g.fa.kept)
	}
}

func TestAddressKeepWhenNotNeededWritesNothing(t *testing.T) {
	t.Parallel()

	acked := weakState
	acked.AckBy, acked.AckAt = "Ana Ruiz", chosenAt()
	pinned := weakState
	pinned.Lock, pinned.Pinned = AddressLockedEnv, "HATAMOTO_ADMIN_PATH"
	for name, st := range map[string]AddressState{"acknowledged": acked, "pinned": pinned, "strong": strongState} {
		g := newKeepRig(t, st)
		for _, method := range []string{"GET", "POST"} {
			var form url.Values
			if method == "POST" {
				form = url.Values{"confirm_phrase": {st.Path}}
			}
			rec := g.do(method, keepPath, "owner-token", form, nil)
			if rec.Code != 303 || rec.Header().Get("Location") != securityPath {
				t.Errorf("%s %s: %d %s", name, method, rec.Code, rec.Header().Get("Location"))
			}
		}
		if len(g.fa.kept) != 0 {
			t.Errorf("%s: Keep wrote", name)
		}
	}
}

func TestAddressAfterKeepBannerGoneAndSecurityShowsAcceptance(t *testing.T) {
	t.Parallel()

	g := newKeepRig(t, weakState)
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(body, bannerMarker) {
		t.Fatal("banner expected before Keep")
	}
	if rec := g.do("POST", keepPath, "owner-token", url.Values{"confirm_phrase": {weakPath}}, nil); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	// The service records the acknowledgement; the fake does not, so do it here.
	g.fa.mu.Lock()
	g.fa.state.AckBy, g.fa.state.AckAt = "Owner Olga", chosenAt()
	g.fa.mu.Unlock()
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, bannerMarker) {
		t.Error("banner still shown after Keep")
	}
	body := g.do("GET", securityPath, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "weak (accepted by Owner Olga on 25 Oct 2026)") {
		t.Errorf("Security page lacks the accepted line:\n%s", body)
	}
	if g.fa.state.Path != weakPath {
		t.Error("Keep must not change the address")
	}
}
