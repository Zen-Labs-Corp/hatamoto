package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/adminpath"
)

const (
	changePath = "/admin/settings/security/admin-address/change"
	newPrivate = "/n3xt-r4nd-8wpz"
)

// changeRig is an owner rig with a fake address service, an audit sink and an
// owner session that already confirmed its password.
type changeRig struct {
	*ownerRig
	fa     *fakeAddress
	mu     sync.Mutex
	events []AuditEvent
}

func newChangeRig(t *testing.T) *changeRig {
	t.Helper()
	chosen := time.Now() // someone chose the address: a new one is a change
	g := &changeRig{ownerRig: newOwnerRig(t, true), fa: &fakeAddress{
		state:  AddressState{Path: "/k7qm-x2rf-9tdc", Level: adminpath.LevelStrong, SetAt: &chosen},
		assess: assessByName, next: newPrivate,
	}}
	g.a.deps.Address = g.fa
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.events = append(g.events, e)
	}
	g.sudo("owner-token")
	return g
}

func (g *changeRig) sudo(token string) { g.a.sudo.store.Grant(g.sessions.byToken[token].ID) }

func (g *changeRig) post(form url.Values) (int, string, string) {
	rec := g.do("POST", changePath, "owner-token", form, nil)
	return rec.Code, rec.Header().Get("Location"), rec.Body.String()
}

func TestSecurityChangeRequiresSudo(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.a.sudo.store = &sudoStore{} // the grant is gone
	rec := g.do("GET", changePath, "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != g.a.sudoURL(changePath, false) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	code, loc, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
	if code != 303 || !strings.HasPrefix(loc, g.a.sudoPath()) {
		t.Fatalf("POST: %d %s", code, loc)
	}
	if len(g.fa.applied) != 0 {
		t.Fatal("Apply ran without sudo")
	}
}

func TestSecurityChangeEditorGets403BeforeSudo(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t) // the editor never confirmed a password
	for _, c := range []struct{ method, path string }{
		{"GET", changePath}, {"POST", changePath}, {"POST", changePath + "/regenerate"}, {"GET", changePath + "/strength?custom=x"},
	} {
		var form url.Values
		if c.method == "POST" {
			form = url.Values{"mode": {"keep"}, "candidate": {newPrivate}}
		}
		if rec := g.do(c.method, c.path, "editor-token", form, nil); rec.Code != 403 {
			t.Errorf("editor %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if len(g.fa.applied) != 0 {
		t.Fatal("Apply ran for an editor")
	}
}

func TestSecurityChangeCSRFMissing(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	rec := g.do("POST", changePath, "owner-token", url.Values{"mode": {"keep"}, "candidate": {newPrivate}}, map[string]string{"X-CSRF-Token": ""})
	if rec.Code != 403 || len(g.fa.applied) != 0 {
		t.Fatalf("%d applied=%d", rec.Code, len(g.fa.applied))
	}
}

func TestSecurityChangePageAccessibleAndGolden(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	rec := g.do("GET", changePath, "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{
		"Keep this private address (recommended)", "Choose my own", `value="https://acme.example` + newPrivate + `"`,
		`data-hm-copy-field="addr-candidate"`, "Regenerate", `hx-get="/admin/settings/security/admin-address/change/strength"`,
		`formaction="/admin/settings/security/admin-address/change/regenerate"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.ContainsAny(body, "\u2013\u2014") {
		t.Error("em or en dash on the page")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-security-change.html", normalizePage(body))
}

func TestSecurityChangeDecisions(t *testing.T) {
	t.Parallel()

	t.Run("weak without phrase shows the card", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		code, _, body := g.post(url.Values{"mode": {"custom"}, "custom": {"my-place"}})
		if code != 422 || len(g.fa.applied) != 0 || !strings.Contains(body, "/my-place is the first address attack tools try") ||
			!strings.Contains(body, `name="confirm_phrase"`) || !strings.Contains(body, `action="`+changePath+`"`) {
			t.Fatalf("%d applied=%d\n%s", code, len(g.fa.applied), body)
		}
		if a11yIssues := a11y.Check(body, a11y.Options{Page: true}); len(a11yIssues) > 0 {
			t.Errorf("a11y: %s", strings.Join(a11yIssues, "; "))
		}
	})
	t.Run("wrong phrase", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		code, _, body := g.post(url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-plac"}})
		if code != 422 || len(g.fa.applied) != 0 || !strings.Contains(body, "The text does not match.") {
			t.Fatalf("%d applied=%d", code, len(g.fa.applied))
		}
	})
	t.Run("right phrase", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		code, loc, _ := g.post(url.Values{"mode": {"custom"}, "custom": {"My-Place"}, "confirm_phrase": {"/my-place"}})
		if code != 303 || loc != "/my-place/settings/security/admin-address" {
			t.Fatalf("%d %s", code, loc)
		}
		if len(g.fa.applied) != 1 || g.fa.applied[0] != (ApplyRequest{Path: "/my-place", By: "Owner Olga", Ack: true}) {
			t.Errorf("applied %+v", g.fa.applied)
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.events) != 1 || g.events[0].Action != "admin_address.change" || g.events[0].Target != "admin-address" {
			t.Errorf("audit %+v", g.events)
		}
	})
	t.Run("reserved", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		code, _, body := g.post(url.Values{"mode": {"custom"}, "custom": {"api"}})
		if code != 422 || len(g.fa.applied) != 0 || !strings.Contains(body, "The site serves /api.") {
			t.Fatalf("%d applied=%d", code, len(g.fa.applied))
		}
	})
	t.Run("keep the private candidate", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		code, loc, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
		if code != 303 || loc != newPrivate+"/settings/security/admin-address" {
			t.Fatalf("%d %s", code, loc)
		}
		if len(g.fa.applied) != 1 || g.fa.applied[0].Ack {
			t.Errorf("applied %+v", g.fa.applied)
		}
	})
	t.Run("service refuses", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		g.fa.applyErr = errors.Join(ErrAddressRefused)
		if code, _, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}}); code != 422 {
			t.Fatalf("%d", code)
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.events) != 0 {
			t.Error("a refused change must not be audited as a change")
		}
	})
	t.Run("service fails", func(t *testing.T) {
		t.Parallel()
		g := newChangeRig(t)
		g.fa.applyErr = errors.New("boom")
		code, loc, body := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
		if code != 500 || loc != "" || !strings.Contains(body, "The current one keeps working.") {
			t.Fatalf("%d %s", code, loc)
		}
	})
}

func TestSecurityChangeRegenerateAndStrength(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	rec := g.do("POST", changePath+"/regenerate", "owner-token", url.Values{"mode": {"keep"}, "candidate": {"/k7qm-x2rf-9tdc"}}, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, newPrivate) || len(g.fa.applied) != 0 {
		t.Fatalf("%d applied=%d", rec.Code, len(g.fa.applied))
	}
	frag := g.do("GET", changePath+"/strength?custom=my-place", "owner-token", nil, map[string]string{"HX-Request": "true"}).Body.String()
	if !strings.Contains(frag, "weak") || strings.Contains(frag, "<html") {
		t.Errorf("fragment: %q", frag)
	}
}

func TestSecurityChangeLockedRefusesAndNeverApplies(t *testing.T) {
	t.Parallel()

	for _, st := range []AddressState{
		{Path: "/k7qm-x2rf-9tdc", Level: adminpath.LevelStrong, Lock: AddressLockedEnv, Pinned: "HATAMOTO_ADMIN_PATH"},
		{Path: "/k7qm-x2rf-9tdc", Level: adminpath.LevelStrong, Lock: AddressLockedDemo},
	} {
		g := newChangeRig(t)
		g.fa.state = st
		if rec := g.do("GET", changePath, "owner-token", nil, nil); rec.Code != 403 {
			t.Errorf("%s GET: %d", st.Lock, rec.Code)
		}
		rec := g.do("POST", changePath, "owner-token", url.Values{"mode": {"keep"}, "candidate": {newPrivate}}, nil)
		if rec.Code != 403 || len(g.fa.applied) != 0 {
			t.Errorf("%s POST: %d applied=%d", st.Lock, rec.Code, len(g.fa.applied))
		}
		if rec := g.do("POST", changePath+"/regenerate", "owner-token", url.Values{"mode": {"keep"}}, nil); rec.Code != 403 {
			t.Errorf("%s regenerate: %d", st.Lock, rec.Code)
		}
	}
}

func TestSecurityChangeAuditHasNoAddress(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.post(url.Values{"mode": {"custom"}, "custom": {"my-place"}, "confirm_phrase": {"/my-place"}})
	g.a.notifyWG.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.events) != 1 {
		t.Fatalf("events %+v", g.events)
	}
	b, _ := json.Marshal(g.events[0])
	if strings.Contains(string(b), "my-place") || strings.Contains(string(b), "k7qm") {
		t.Errorf("the audit event carries an address: %s", b)
	}
	d := g.events[0].Detail
	if d["mode"] != "custom" || d["level"] != "weak" || d["acknowledged"] != true {
		t.Errorf("detail %+v", d)
	}
	if _, ok := d["ip"]; !ok {
		t.Error("detail lacks ip")
	}
}

func TestSecurityChangeMailsEveryOwnerOnly(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	second := *g.owner
	second.ID, second.Email = g.editor.ID, "second-owner@example.com"
	g.mgr.users[second.ID] = &second // the editor row becomes a second owner
	if code, _, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}}); code != 303 {
		t.Fatalf("%d", code)
	}
	g.a.notifyWG.Wait()
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if len(g.mail.addrChanged) != 2 {
		t.Fatalf("mails %+v", g.mail.addrChanged)
	}
	got := map[string]bool{}
	for _, m := range g.mail.addrChanged {
		got[m.to] = true
		d := m.data
		if d.Address != "https://acme.example"+newPrivate || d.ChangedBy != "Owner Olga" || d.IP == "" || !strings.HasSuffix(d.When, " UTC") || d.SiteName == "" {
			t.Errorf("mail data %+v", d)
		}
	}
	if !got["owner@example.com"] || !got["second-owner@example.com"] {
		t.Errorf("recipients %v", got)
	}
}

func TestSecurityChangeMailsNoEditor(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
	g.a.notifyWG.Wait()
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if len(g.mail.addrChanged) != 1 || g.mail.addrChanged[0].to != "owner@example.com" {
		t.Fatalf("mails %+v", g.mail.addrChanged)
	}
}

func TestSecurityChangeMailFailureNeverBlocks(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.mail.addrErr = errors.New("smtp down")
	code, loc, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
	g.a.notifyWG.Wait()
	if code != 303 || loc != newPrivate+"/settings/security/admin-address" || len(g.fa.applied) != 1 {
		t.Fatalf("%d %s", code, loc)
	}
}

func TestSecurityChangeWithoutMailSendsNothing(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.mail.configured = false
	code, _, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
	g.a.notifyWG.Wait()
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if code != 303 || len(g.mail.addrChanged) != 0 {
		t.Fatalf("%d mails %+v", code, g.mail.addrChanged)
	}
}

func TestSecurityChangeTypedCustomUnderKeepIsRefused(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	code, _, body := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}, "custom": {"my-private-place-9"}})
	g.a.notifyWG.Wait()
	if code != 422 || !strings.Contains(body, "Select &#34;Choose my own&#34; to use this address, or clear the field.") {
		t.Fatalf("%d\n%s", code, body)
	}
	if len(g.fa.applied) != 0 || len(g.events) != 0 || len(g.mail.addrChanged) != 0 {
		t.Errorf("nothing may be stored, audited or mailed: %+v %+v %+v", g.fa.applied, g.events, g.mail.addrChanged)
	}
}

func TestSecurityChangeFlashReachesTheNewAddress(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	rec := g.do("POST", changePath, "owner-token", url.Values{"mode": {"keep"}, "candidate": {newPrivate}}, nil)
	g.a.notifyWG.Wait()
	var flash *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie {
			flash = c
		}
	}
	if rec.Code != 303 || flash == nil || flash.Path != newPrivate {
		t.Fatalf("%d flash cookie %+v, want it scoped to %s", rec.Code, flash, newPrivate)
	}
	// What the browser sends on the redirected GET at the new address.
	cookie := "hatamoto_session=owner-token; " + flashCookie + "=" + flash.Value
	body := g.do("GET", securityPath, "", nil, map[string]string{"Cookie": cookie}).Body.String()
	if !strings.Contains(body, "Admin address changed.") {
		t.Errorf("the success message is not shown:\n%s", body)
	}
}

func TestSecurityChangeToTheLiveAddressWritesNothing(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	rec := g.do("POST", changePath, "owner-token", url.Values{"mode": {"custom"}, "custom": {"k7qm-x2rf-9tdc"}}, nil)
	g.a.notifyWG.Wait()
	if rec.Code != 303 || rec.Header().Get("Location") != securityPath {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	var flash *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie {
			flash = c
		}
	}
	if flash == nil {
		t.Fatal("no flash")
	}
	cookie := "hatamoto_session=owner-token; " + flashCookie + "=" + flash.Value
	if body := g.do("GET", securityPath, "", nil, map[string]string{"Cookie": cookie}).Body.String(); !strings.Contains(body, "The admin address is unchanged.") || strings.Contains(body, "Admin address changed.") {
		t.Errorf("flash:\n%s", body)
	}
	if len(g.fa.applied) != 0 || len(g.events) != 0 {
		t.Errorf("a no-op must not write or audit: applied %+v events %+v", g.fa.applied, g.events)
	}
}

// An upgraded install has the row at /admin with SetAt NULL: the first change
// from Security must still alert the owners.
func TestSecurityChangeOnUnchosenRowStillAlerts(t *testing.T) {
	t.Parallel()

	g := newChangeRig(t)
	g.fa.state.SetAt = nil
	code, _, _ := g.post(url.Values{"mode": {"keep"}, "candidate": {newPrivate}})
	g.a.notifyWG.Wait()
	if code != 303 {
		t.Fatalf("status %d", code)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.events) != 1 || g.events[0].Action != "admin_address.change" {
		t.Errorf("audit %+v", g.events)
	}
	g.mail.mu.Lock()
	defer g.mail.mu.Unlock()
	if len(g.mail.addrChanged) != 1 || g.mail.addrChanged[0].to != "owner@example.com" {
		t.Errorf("mails %+v", g.mail.addrChanged)
	}
}
