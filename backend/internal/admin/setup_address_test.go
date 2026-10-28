package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/user"
)

const (
	testPending   = "/k7qm-x2rf-9tdc"
	testGenerated = "/n3xt-r4nd-8wpz"
)

func setupAddressRig(t *testing.T) (*setupRig, *fakeAddress) {
	t.Helper()
	g := newSetupRig(t)
	g.count.n = 1
	fa := g.a.deps.Address.(*fakeAddress)
	fa.assess = assessByName
	g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID)
	return g, fa
}

func TestSetupStepperListsAdminAddressSecond(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	body := g.do("GET", "/admin/setup/admin-address", "owner-token", nil, nil).Body.String()
	account := strings.Index(body, "Your account")
	addr := strings.Index(body, `Admin address<span class="hm-visually-hidden"></span>`)
	if addr < 0 {
		addr = strings.Index(body, "hm-steps__item--current")
	}
	site := strings.Index(body, "Your site")
	if !(account < addr && addr < site) || !strings.Contains(body, "Step 2 of 8") {
		t.Errorf("stepper order wrong (%d %d %d):\n%s", account, addr, site, body)
	}
}

func TestSetupAddressPageCopy(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	body := g.do("GET", "/admin/setup/admin-address", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		"Keep this private address (recommended)", "Choose my own",
		"Where you sign in to manage your site.",
		`value="https://acme.example` + testPending + `"`,
		"Bookmark this. Scanners can't find it, and neither can you if you lose it. You can always see it on the server with <code>hatamoto admin-url</code>.",
		`data-hm-copy-field="addr-candidate"`, "Regenerate", `hx-get="/admin/setup/admin-address/strength"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.ContainsAny(body, "\u2013\u2014") {
		t.Error("em or en dash on the page")
	}
}

func TestSetupAddressSaveDecisions(t *testing.T) {
	t.Parallel()

	post := func(g *setupRig, form url.Values) (int, string, string) {
		rec := g.do("POST", "/admin/setup/admin-address", "owner-token", form, nil)
		return rec.Code, rec.Header().Get("Location"), rec.Body.String()
	}
	weak := url.Values{"mode": {"custom"}, "candidate": {testPending}, "custom": {"my-place"}}

	t.Run("weak without phrase", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		code, _, body := post(g, weak)
		if code != 422 || len(fa.applied) != 0 {
			t.Fatalf("%d applied=%d", code, len(fa.applied))
		}
		for _, want := range []string{"/my-place is the first address attack tools try", "To use this address anyway, type /my-place below.", "I understand, use /my-place", "<code>/wp-admin</code>", `name="confirm_phrase"`, `name="custom" value="my-place"`} {
			if !strings.Contains(body, want) {
				t.Errorf("card lacks %q", want)
			}
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y: %s", strings.Join(issues, "; "))
		}
		compareGolden(t, "pages/setup-admin-address-weak.html", normalizePage(body))
	})
	t.Run("weak wrong phrase", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		form := url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-plac"}}
		code, _, body := post(g, form)
		if code != 422 || len(fa.applied) != 0 || !strings.Contains(body, "The text does not match.") {
			t.Fatalf("%d applied=%d", code, len(fa.applied))
		}
	})
	t.Run("weak right phrase", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		form := url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-place"}}
		code, loc, _ := post(g, form)
		if code != 303 || loc != "/my-place/setup/site" {
			t.Fatalf("%d %s", code, loc)
		}
		if len(fa.applied) != 1 || fa.applied[0] != (ApplyRequest{Path: "/my-place", By: "Owner Olga", Ack: true}) {
			t.Errorf("applied %+v", fa.applied)
		}
	})
	t.Run("reserved", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		code, _, body := post(g, url.Values{"mode": {"custom"}, "custom": {"api"}})
		if code != 422 || len(fa.applied) != 0 || !strings.Contains(body, "The site serves /api.") {
			t.Fatalf("%d applied=%d", code, len(fa.applied))
		}
	})
	t.Run("keep", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		code, loc, _ := post(g, url.Values{"mode": {"keep"}, "candidate": {testPending}})
		if code != 303 || loc != "/admin/setup/site" {
			t.Fatalf("%d %s", code, loc)
		}
		if len(fa.applied) != 0 {
			t.Errorf("keeping the live address must not apply: %+v", fa.applied)
		}
	})
	t.Run("keep a new candidate", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		fa.state.Path = "/old-private-1234"
		code, loc, _ := post(g, url.Values{"mode": {"keep"}, "candidate": {testPending}})
		if code != 303 || loc != testPending+"/setup/site" {
			t.Fatalf("%d %s", code, loc)
		}
		if len(fa.applied) != 1 || fa.applied[0].Ack || fa.applied[0].Path != testPending {
			t.Errorf("applied %+v", fa.applied)
		}
	})
	t.Run("service refuses", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		fa.applyErr = errors.Join(ErrAddressRefused)
		fa.state.Path = "/old-private-1234"
		code, _, _ := post(g, url.Values{"mode": {"keep"}, "candidate": {testPending}})
		if code != 422 {
			t.Fatalf("%d", code)
		}
	})
	t.Run("service fails", func(t *testing.T) {
		t.Parallel()
		g, fa := setupAddressRig(t)
		fa.applyErr = errors.New("boom")
		fa.state.Path = "/old-private-1234"
		code, _, body := post(g, url.Values{"mode": {"keep"}, "candidate": {testPending}})
		if code != 500 || !strings.Contains(body, "The current one keeps working.") {
			t.Fatalf("%d", code)
		}
	})
}

func TestSetupAddressApplyFailureLogsNoAddress(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	var buf bytes.Buffer
	g.a.deps.Logger = slog.New(slog.NewTextHandler(&buf, nil))
	fa.state.Path = "/old-private-1234"
	fa.applyErr = errors.New("write " + testPending + " failed")
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}}, nil)
	if rec.Code != 500 || !strings.Contains(buf.String(), "apply admin address failed") || strings.Contains(buf.String(), "k7qm") {
		t.Errorf("%d log %q", rec.Code, buf.String())
	}
}

func TestSetupAddressRegenerateStoresNothing(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	rec := g.do("POST", "/admin/setup/admin-address/regenerate", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}}, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, testGenerated) || strings.Contains(body, testPending+`"`) {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
	if len(fa.applied) != 0 {
		t.Error("regenerate must store nothing")
	}
	if !strings.Contains(body, "Strength:") || !strings.Contains(body, "strong") {
		t.Error("the new candidate should show its strength")
	}
}

func TestSetupAddressRegenerateFailureIsVisible(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	fa.next = ""
	rec := g.do("POST", "/admin/setup/admin-address/regenerate", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}}, nil)
	body := rec.Body.String()
	if rec.Code != 500 || !strings.Contains(body, "We could not make a new address.") || !strings.Contains(body, testPending) {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
}

func TestSetupAddressStrengthFragment(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	rec := g.do("GET", "/admin/setup/admin-address/strength?custom=my-place", "owner-token", nil, map[string]string{"HX-Request": "true"})
	body := rec.Body.String()
	for _, want := range []string{"Strength:", "weak", "on the list of addresses attack tools try", "shorter than 10 characters"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "<html") {
		t.Error("a fragment, not a page")
	}
	if rec := g.do("GET", "/admin/setup/admin-address/strength?custom=", "owner-token", nil, nil); strings.Contains(rec.Body.String(), "Strength") {
		t.Error("an empty address has no verdict")
	}
}

func TestSetupAddressLocked(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	fa.state.Lock, fa.state.Pinned = AddressLockedEnv, "HATAMOTO_ADMIN_PATH"
	body := g.do("GET", "/admin/setup/admin-address", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Set by the server environment (HATAMOTO_ADMIN_PATH).") || strings.Contains(body, "Choose my own") || strings.Contains(body, `action="/admin/setup/admin-address"`) {
		t.Errorf("pinned view:\n%s", body)
	}
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", url.Values{"mode": {"custom"}, "custom": {"zzzz-zzzz-zzzz"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/site" || len(fa.applied) != 0 {
		t.Errorf("pinned POST: %d %s applied=%d", rec.Code, rec.Header().Get("Location"), len(fa.applied))
	}
	fa.state.Lock = AddressLockedDemo
	rec = g.do("GET", "/admin/setup/admin-address", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/site" {
		t.Errorf("demo skips the step: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := g.do("GET", "/admin/setup/site", "owner-token", nil, nil).Body.String(); strings.Contains(body, "<span>Back</span>") {
		t.Error("the Site step has no Back when the address step is skipped")
	}
}

func TestSetupDoneShowsAddressAndMailsOnce(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	for range 2 {
		body := g.do("GET", "/admin/setup/done", "owner-token", nil, nil).Body.String()
		for _, want := range []string{`value="https://acme.example` + testPending + `"`, `data-hm-copy-field="addr-done"`, "We emailed it to you."} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q", want)
			}
		}
	}
	if n := len(g.mail.addrSetup); n != 1 {
		t.Errorf("sent %d mails for two visits, want 1", n)
	}
}

func TestSetupDoneMailFailureAndNoMail(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	g.mail.addrErr = errors.New("smtp down")
	body := g.do("GET", "/admin/setup/done", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "We emailed it to you.") || !strings.Contains(body, testPending) {
		t.Error("a failed mail must not be claimed; the address still shows")
	}
	g.mail.addrErr = nil
	g.mail.configured = false
	body = g.do("GET", "/admin/setup/done", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "We emailed it to you.") || len(g.mail.addrSetup) != 1 {
		t.Errorf("no mail configured: nothing claimed, nothing sent (sent %d)", len(g.mail.addrSetup))
	}
}

func TestSetupAddressChangeNeedsSudoUnlessFresh(t *testing.T) {
	t.Parallel()

	form := url.Values{"mode": {"keep"}, "candidate": {testPending}}
	stale, fa := setupAddressRig(t)
	stale.a.sudo.store = &sudoStore{} // an owner session from an earlier visit
	fa.state.Path = "/old-private-1234"
	// The prompt must bring the owner back to the step: no Referer is sent.
	wantNext := "next=" + url.QueryEscape("/admin/setup/admin-address")
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/setup/admin-address"}, {"POST", "/admin/setup/admin-address"}, {"POST", "/admin/setup/admin-address/regenerate"},
	} {
		rec := stale.do(c.method, c.path, "owner-token", form, nil)
		loc := rec.Header().Get("Location")
		if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/sudo") || (c.method == "GET" || c.path == "/admin/setup/admin-address") && !strings.Contains(loc, wantNext) {
			t.Errorf("%s %s: %d %s, want the sudo prompt returning to the step", c.method, c.path, rec.Code, loc)
		}
	}
	if len(fa.applied) != 0 {
		t.Errorf("Apply ran without sudo: %+v", fa.applied)
	}

	fresh := newSetupRig(t) // no sudo grant: the account step must give one
	fresh.count.n = 0
	good := url.Values{"code": {"abcd 2345"}, "name": {"Olga"}, "email": {"o@example.com"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}
	if rec := fresh.do("POST", "/admin/setup", "", good, nil); rec.Code != 303 {
		t.Fatalf("account: %d", rec.Code)
	}
	fresh.count.n = 1
	fresh.a.deps.Address.(*fakeAddress).assess = assessByName
	fresh.a.deps.Address.(*fakeAddress).state.Path = "/old-private-1234"
	rec := fresh.do("POST", "/admin/setup/admin-address", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != testPending+"/setup/site" {
		t.Errorf("fresh account session: %d %s, want to proceed", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSetupAddressKeepUnchangedNeedsNoSudo(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	g.a.sudo.store = &sudoStore{}
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup/site" || len(fa.applied) != 0 {
		t.Errorf("%d %s applied=%d", rec.Code, rec.Header().Get("Location"), len(fa.applied))
	}
}

func TestSetupAddressChangeAuditsAndNotifies(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	g.a.deps.Manager = &fakeManager{users: map[uuid.UUID]*user.User{g.owner.ID: g.owner}}
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	fa.state.Path = "/old-private-1234"
	chosen := time.Now()
	fa.state.SetAt = &chosen
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}}, nil)
	g.a.notifyWG.Wait()
	if rec.Code != 303 || len(events) != 1 || events[0].Action != "admin_address.change" {
		t.Fatalf("%d events %+v", rec.Code, events)
	}
	if b := fmt.Sprint(events[0]); strings.Contains(b, "k7qm") || strings.Contains(b, "old-private") {
		t.Errorf("audit carries an address: %s", b)
	}
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if len(g.mail.addrChanged) != 1 || g.mail.addrChanged[0].to != "owner@example.com" {
		t.Errorf("owner mails %+v", g.mail.addrChanged)
	}
}

func TestSetupAddressFirstChoiceIsAuditedNotMailed(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t) // SetAt is nil: only the generated address exists
	g.a.deps.Manager = &fakeManager{users: map[uuid.UUID]*user.User{g.owner.ID: g.owner}}
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	form := url.Values{"mode": {"custom"}, "custom": {"my-private-place-9"}}
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", form, nil)
	g.a.notifyWG.Wait()
	if rec.Code != 303 || len(fa.applied) != 1 || len(events) != 1 || events[0].Action != "admin_address.choose" {
		t.Fatalf("%d applied=%d events %+v", rec.Code, len(fa.applied), events)
	}
	if b := fmt.Sprint(events[0]); strings.Contains(b, "my-private-place") {
		t.Errorf("audit carries an address: %s", b)
	}
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if len(g.mail.addrChanged) != 0 {
		t.Errorf("the first choice must not send the changed alarm: %+v", g.mail.addrChanged)
	}
}

func TestSetupAddressTypedCustomUnderKeepIsRefused(t *testing.T) {
	t.Parallel()

	g, fa := setupAddressRig(t)
	g.a.deps.Manager = &fakeManager{users: map[uuid.UUID]*user.User{g.owner.ID: g.owner}}
	rec := g.do("POST", "/admin/setup/admin-address", "owner-token", url.Values{"mode": {"keep"}, "candidate": {testPending}, "custom": {"my-private-place-9"}}, nil)
	g.a.notifyWG.Wait()
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Select &#34;Choose my own&#34; to use this address, or clear the field.") {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	if len(fa.applied) != 0 || len(g.mail.addrChanged) != 0 {
		t.Errorf("nothing may be stored or mailed: %+v %+v", fa.applied, g.mail.addrChanged)
	}
}

func TestSetupDoneMailUsesTheConfiguredSiteAddress(t *testing.T) {
	t.Parallel()

	g, _ := setupAddressRig(t)
	g.do("GET", "/admin/setup/done", "owner-token", nil, map[string]string{"X-Forwarded-Host": "evil.example"})
	if len(g.mail.setupAddrs) != 1 || g.mail.setupAddrs[0] != "https://acme.example"+testPending {
		t.Errorf("mail address %v", g.mail.setupAddrs)
	}

	g, _ = setupAddressRig(t)
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	g.do("GET", "/admin/setup/done", "owner-token", nil, map[string]string{"X-Forwarded-Host": "evil.example"})
	if len(g.mail.setupAddrs) != 1 || g.mail.setupAddrs[0] != testPending {
		t.Errorf("without a site address the mail carries the path only, got %v", g.mail.setupAddrs)
	}
}
