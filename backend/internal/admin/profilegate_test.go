package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/person"
)

// gateRig is an admin with a fake people store where the editor has no photo
// yet and the owner is complete.
func gateRig(t *testing.T) (*ownerRig, *fakePeople) {
	t.Helper()
	g := newOwnerRig(t, false)
	people := newFakePeople()
	people.set(g.owner.ID, &person.Person{Name: "Owner Olga", Photo: "/media/olga.jpg", Slug: "olga", Links: map[string]string{}})
	g.a.deps.People = people
	return g, people
}

func TestProfileGateBouncesIncompleteUser(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	for _, path := range []string{"/admin/entities", "/admin/", "/admin/settings/general", "/admin/settings/media", "/admin/missing"} {
		rec := g.do("GET", path, "editor-token", nil, nil)
		want := ""
		if next := SafeNext(path); next != "" && path != "/admin/" && path != "/admin/missing" {
			want = "/admin/profile/complete?next=" + url.QueryEscape(next)
		} else if path == "/admin/" {
			want = "/admin/profile/complete?next=" + url.QueryEscape("/admin/")
		}
		if path == "/admin/missing" { // the unowned-path fallback is not gated
			continue
		}
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("GET %s = %d %q, want 303 %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	// The query string of the page survives in next.
	rec := g.do("GET", "/admin/entities?status=draft&page=2", "editor-token", nil, nil)
	if got, want := rec.Header().Get("Location"), "/admin/profile/complete?next="+url.QueryEscape("/admin/entities?status=draft&page=2"); got != want {
		t.Errorf("next with a query = %q, want %q", got, want)
	}
}

func TestProfileGateExemptRoutesAreReachable(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	loc := func(rec interface{ Header() http.Header }) string { return rec.Header().Get("Location") }
	// Every exempt route, as an incomplete user: never a redirect to the step.
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/profile"},
		{"GET", "/admin/profile/complete"},
		{"GET", "/admin/profile/complete?next=/admin/entities"},
		{"POST", "/admin/profile"},
		{"POST", "/admin/profile/complete"},
		{"POST", "/admin/profile/password"},
		{"GET", "/admin/media/picker"},
		{"POST", "/admin/media/upload"},
		{"GET", "/admin/_/css/admin.css"},
	} {
		for _, htmx := range []bool{false, true} {
			hdr := map[string]string{}
			if htmx {
				hdr["HX-Request"] = "true"
			}
			var form url.Values
			if c.method == "POST" {
				form = url.Values{"name": {"E"}}
			}
			rec := g.do(c.method, c.path, "editor-token", form, hdr)
			if l := loc(rec); strings.HasPrefix(l, "/admin/profile/complete") && !strings.HasPrefix(c.path, "/admin/profile") {
				t.Errorf("%s %s (htmx=%v) was bounced to the step: %d %s", c.method, c.path, htmx, rec.Code, l)
			}
			if rec.Header().Get("HX-Redirect") != "" {
				t.Errorf("%s %s (htmx=%v) got HX-Redirect %q", c.method, c.path, htmx, rec.Header().Get("HX-Redirect"))
			}
			if c.path == "/admin/profile" || strings.HasPrefix(c.path, "/admin/profile/complete") {
				if c.method == "GET" && rec.Code != 200 {
					t.Errorf("GET %s (htmx=%v) = %d, want 200", c.path, htmx, rec.Code)
				}
			}
		}
	}
	// Sign out works for an incomplete user (htmx too), and is the last request: it ends the session.
	for _, hdr := range []map[string]string{nil, {"HX-Request": "true"}} {
		g2, _ := gateRig(t)
		rec := g2.do("POST", "/admin/logout", "editor-token", url.Values{}, hdr)
		if strings.HasPrefix(rec.Header().Get("Location"), "/admin/profile/complete") || strings.HasPrefix(rec.Header().Get("HX-Redirect"), "/admin/profile/complete") {
			t.Errorf("logout bounced to the step (hdr %v): %d", hdr, rec.Code)
		}
	}
	// The step itself must not redirect to itself (no loop), with or without next.
	for _, path := range []string{"/admin/profile/complete", "/admin/profile/complete?next=/admin/profile/complete"} {
		rec := g.do("GET", path, "editor-token", nil, nil)
		if rec.Code != 200 {
			t.Errorf("GET %s = %d %s, want the page", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestProfileGateHTMXAndPost(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	// An htmx fragment request: HX-Redirect to the step, next is the page it came from.
	rec := g.do("GET", "/admin/entities/rows", "editor-token", nil, map[string]string{
		"HX-Request": "true", "HX-Target": "rows", "HX-Current-URL": "https://acme.example/admin/entities?status=draft",
	})
	if rec.Code != 200 || rec.Header().Get("HX-Redirect") != "/admin/profile/complete?next="+url.QueryEscape("/admin/entities?status=draft") {
		t.Errorf("htmx GET = %d HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	// An htmx request from nowhere useful: the step without next, never an open redirect.
	rec = g.do("GET", "/admin/entities", "editor-token", nil, map[string]string{"HX-Request": "true", "HX-Current-URL": "https://evil.example/x"})
	if rec.Header().Get("HX-Redirect") != "/admin/profile/complete" {
		t.Errorf("htmx foreign current URL: HX-Redirect %q", rec.Header().Get("HX-Redirect"))
	}
	// A POST is bounced to the step with no next (it is not replayed).
	rec = g.do("POST", "/admin/entities/bulk", "editor-token", url.Values{"x": {"1"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/profile/complete" {
		t.Errorf("POST = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("POST", "/admin/entities/bulk", "editor-token", url.Values{"x": {"1"}}, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") != "/admin/profile/complete" {
		t.Errorf("htmx POST HX-Redirect %q", rec.Header().Get("HX-Redirect"))
	}
}

func TestProfileGateLetsCompleteUsersThrough(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	for _, path := range []string{"/admin/entities", "/admin/settings/general"} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if strings.HasPrefix(rec.Header().Get("Location"), "/admin/profile/complete") {
			t.Errorf("complete user bounced from %s: %d %s", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Anonymous requests still go to sign-in, not to the step.
	if rec := g.do("GET", "/admin/entities", "", nil, nil); !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// The signed-in sign-in page bounces on to the landing page, and the gate to the step: one hop each, no loop.
	rec := g.do("GET", "/admin/login", "editor-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities" {
		t.Fatalf("login while signed in = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("GET", rec.Header().Get("Location"), "editor-token", nil, nil)
	if !strings.HasPrefix(rec.Header().Get("Location"), "/admin/profile/complete?next=") {
		t.Errorf("second hop = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProfileGateFinishesInOneStep(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	if rec := g.do("GET", "/admin/entities", "editor-token", nil, nil); rec.Code != 303 {
		t.Fatalf("incomplete editor not bounced: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/profile/complete", "editor-token", completeForm(url.Values{"next": {"/admin/entities"}}), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities" {
		t.Fatalf("complete = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("GET", "/admin/entities", "editor-token", nil, nil)
	if strings.HasPrefix(rec.Header().Get("Location"), "/admin/profile/complete") {
		t.Errorf("still bounced after completing: %q", rec.Header().Get("Location"))
	}
}

func TestProfileGateWithoutPeopleStoreIsOpen(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	rec := g.do("GET", "/admin/entities", "editor-token", nil, nil)
	if strings.HasPrefix(rec.Header().Get("Location"), "/admin/profile/complete") {
		t.Errorf("gate active without a people store: %q", rec.Header().Get("Location"))
	}
}

// The first-run wizard comes before the profile step: while setup is
// unfinished an incomplete owner reaches every wizard page; once it is
// finished the wizard paths are ordinary admin paths and the gate applies.
func TestProfileGateSkipsSetupWizardUntilSetupCompletes(t *testing.T) {
	t.Parallel()

	g, people := gateRig(t)
	people.set(g.owner.ID, &person.Person{Slug: "olga", Links: map[string]string{}}) // no name, no photo
	site := newFakeSite("Acme")
	g.a.deps.Site = site

	g.a.deps.EmailSettings = &fakeEmailSettings{}
	g.a.deps.Stats = &fakeStats{view: StatsView{Consent: "unset"}}
	site.cfg.SetupCompletedAt = nil
	for _, path := range []string{"/admin/setup/site", "/admin/setup/email", "/admin/setup/theme", "/admin/setup/stats", "/admin/setup/content", "/admin/setup/done"} {
		if rec := g.do("GET", path, "owner-token", nil, nil); rec.Code != http.StatusOK {
			t.Errorf("during setup GET %s = %d %q, want 200", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	// The wizard exemption does not leak to other admin pages.
	if rec := g.do("GET", "/admin/entities", "owner-token", nil, nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), completeProfilePath) {
		t.Errorf("during setup GET /admin/entities = %d %q, want the profile step", rec.Code, rec.Header().Get("Location"))
	}

	done := time.Now()
	site.cfg.SetupCompletedAt = &done
	rec := g.do("GET", "/admin/setup/site", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), completeProfilePath) {
		t.Errorf("after setup GET /admin/setup/site = %d %q, want the profile step", rec.Code, rec.Header().Get("Location"))
	}
	// The content step finishes setup and lands on done: that page still shows.
	if rec := g.do("GET", "/admin/setup/done", "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("after setup GET /admin/setup/done = %d, want 200", rec.Code)
	}
}

// A profile with no photo shows initials and a hidden preview; the required star and the bare
// layout (no sidebar) belong to the gated "Complete your profile" step only.
func TestProfilePhotoFallbackAndCompleteLayout(t *testing.T) {
	t.Parallel()

	g, _ := gateRig(t)
	profile := g.do("GET", "/admin/profile", "editor-token", nil, nil).Body.String()
	if !strings.Contains(profile, `class="hm-avatar hm-avatar--lg hm-photo-avatar"`) || !strings.Contains(profile, `class="hm-sf-image hm-photo-preview" src="" alt="Your photo" data-hm-photo hidden`) {
		t.Errorf("no photo: initials avatar and a hidden preview expected:\n%s", profile)
	}
	if strings.Contains(profile, `Photo <span aria-hidden="true">*</span>`) {
		t.Error("the photo label must not show * on the profile page")
	}
	// The owner has a photo: the identity row shows it instead of initials.
	if own := g.do("GET", "/admin/profile", "owner-token", nil, nil).Body.String(); !strings.Contains(own, `<img class="hm-avatar" src="/media/olga.jpg" alt="" data-hm-photo data-initials="`) {
		t.Error("the identity row must show the photo (with the initials fallback) when there is one")
	}
	complete := g.do("GET", "/admin/profile/complete", "editor-token", nil, nil).Body.String()
	if !strings.Contains(complete, `Photo <span aria-hidden="true">*</span>`) {
		t.Error("the photo label must show * on Complete your profile")
	}
	if strings.Contains(complete, "hm-sidebar") || !strings.Contains(complete, `class="hm-bare"`) {
		t.Error("Complete your profile must render without the sidebar")
	}
	for _, want := range []string{`action="/admin/logout"`, `action="/admin/theme"`} {
		if !strings.Contains(complete, want) {
			t.Errorf("Complete your profile must keep %s (the gate leaves it open)", want)
		}
	}
	// A phone has no tooltips: the two controls carry visible text.
	if !strings.Contains(complete, "<span>Sign out</span>") {
		t.Error("Complete your profile needs a visible Sign out label")
	}
	if !strings.Contains(complete, "theme</span>") {
		t.Error("Complete your profile needs a visible theme label")
	}
}
