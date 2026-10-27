package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// sudoGuarded mounts a counted handler behind RequireSudo at path and returns the counter.
func sudoGuarded(g *rig, path string) *int {
	calls := new(int)
	h := g.a.guardFor(scopeAdmin)(g.a.RequireSession(g.a.RequireSudo(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	}))))
	g.h.(*http.ServeMux).Handle(path, h)
	return calls
}

const guardedPath = "/sudo-test/guarded"

func TestRequireSudoSendsAnUnelevatedGetToTheSudoPage(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	calls := sudoGuarded(g, "GET "+guardedPath)
	rec := g.do("GET", guardedPath+"?x=1", "owner-token", nil, nil)
	// The test path is outside the admin prefix, so SafeNext drops it: home.
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != g.a.sudoURL("", false) {
		t.Fatalf("Location %q", loc)
	}
	if *calls != 0 {
		t.Fatal("the handler ran")
	}
}

func TestRequireSudoCarriesTheAdminPathAsNext(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	p := g.a.url("sudo-test/page")
	sudoGuarded(g, "GET "+p)
	rec := g.do("GET", p+"?x=1", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != g.a.sudoURL(p+"?x=1", false) {
		t.Fatalf("Location %q", loc)
	}
}

func TestRequireSudoPostUsesTheRefererAndNeverRunsTheHandler(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	p := g.a.url("sudo-test/danger")
	calls := sudoGuarded(g, "POST "+p)
	from := g.a.url("people/new")
	rec := g.do("POST", p, "owner-token", url.Values{"a": {"1"}}, map[string]string{"Referer": "http://example.com" + from + "?tab=1"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != g.a.sudoURL(from+"?tab=1", true) {
		t.Fatalf("Location %q", loc)
	}
	if *calls != 0 {
		t.Fatal("the handler ran on an unelevated POST")
	}
}

// Confirming after a blocked POST lands on a GET of the page it came from. The
// guarded handler must never run for the blocked POST.
func TestRequireSudoPostIsNeverReplayedAfterConfirming(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	p := g.a.url("sudo-test/danger")
	calls := sudoGuarded(g, "POST "+p)
	from := g.a.url("people/new")
	rec := g.do("POST", p, "owner-token", url.Values{"a": {"1"}}, map[string]string{"Referer": "http://example.com" + from})
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusSeeOther {
		t.Fatalf("blocked POST: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Follow the redirect to the sudo page, then confirm with its next value.
	if page := g.do("GET", loc.String(), "owner-token", nil, nil); page.Code != http.StatusOK {
		t.Fatalf("sudo page: %d", page.Code)
	}
	confirm := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "next": {loc.Query().Get("next")}}, nil)
	if confirm.Code != http.StatusSeeOther || confirm.Header().Get("Location") != from {
		t.Fatalf("confirm: %d %q, want a redirect to %q", confirm.Code, confirm.Header().Get("Location"), from)
	}
	if !g.elevated("owner-token") {
		t.Fatal("not elevated")
	}
	if *calls != 0 {
		t.Fatal("the blocked POST was replayed")
	}
}

func TestRequireSudoPostIgnoresAForeignOrMissingReferer(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	p := g.a.url("sudo-test/danger")
	sudoGuarded(g, "POST "+p)
	for _, ref := range []string{"http://evil.example" + g.a.url("people"), ""} {
		rec := g.do("POST", p, "owner-token", url.Values{"a": {"1"}}, map[string]string{"Referer": ref})
		if loc := rec.Header().Get("Location"); loc != g.a.sudoURL("", true) {
			t.Errorf("Referer %q: Location %q, want the home fallback", ref, loc)
		}
	}
}

func TestRequireSudoAnswersHTMXWithHXRedirect(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	p := g.a.url("sudo-test/page")
	sudoGuarded(g, "GET "+p)
	rec := g.do("GET", p, "owner-token", nil, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != g.a.sudoURL(p, false) {
		t.Fatalf("HX-Redirect %q", got)
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Location") != "" {
		t.Fatalf("htmx answer has a body or Location: %q", rec.Body.String())
	}
}

func TestRequireSudoLetsAnElevatedSessionThroughUntilTheWindowEnds(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g.a.sudo.store.now = clock.now
	p := g.a.url("sudo-test/page")
	calls := sudoGuarded(g, "GET "+p)
	g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID)

	clock.t = clock.t.Add(9 * time.Minute)
	if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("elevated status %d", rec.Code)
	}
	// Using it did not extend the window: 10 minutes after the Grant it ends.
	clock.t = clock.t.Add(time.Minute)
	rec := g.do("GET", p, "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) {
		t.Fatalf("after the window: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if *calls != 1 {
		t.Fatalf("handler ran %d times, want 1", *calls)
	}
	// Another session was never elevated.
	if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("other session status %d", rec.Code)
	}
}
