package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/user"
)

type fakeManager struct {
	mu      sync.Mutex
	users   map[uuid.UUID]*user.User
	created []string
}

func (f *fakeManager) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, user.ErrUserNotFound
}
func (f *fakeManager) List(context.Context, int, int) ([]*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*user.User
	for _, u := range f.users {
		c := *u
		out = append(out, &c)
	}
	return out, nil
}
func (f *fakeManager) owners(except uuid.UUID) int {
	n := 0
	for id, u := range f.users {
		if id != except && u.Role == user.RoleOwner {
			n++
		}
	}
	return n
}
func (f *fakeManager) Create(_ context.Context, in *user.CreateUserInput, hash string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Email, in.Email) {
			return nil, user.ErrEmailAlreadyExists
		}
	}
	n := in.Name
	u := &user.User{ID: uuid.New(), Email: in.Email, Role: in.Role, Name: &n}
	f.users[u.ID] = u
	f.created = append(f.created, hash)
	return u, nil
}
func (f *fakeManager) Update(_ context.Context, id uuid.UUID, in *user.UpdateUserInput) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, user.ErrUserNotFound
	}
	if in.Role != nil && *in.Role != user.RoleOwner && u.Role == user.RoleOwner && f.owners(id) == 0 {
		return nil, user.ErrLastOwner
	}
	if in.Name != nil {
		u.Name = in.Name
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	c := *u
	return &c, nil
}
func (f *fakeManager) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return user.ErrUserNotFound
	}
	if u.Role == user.RoleOwner && f.owners(id) == 0 {
		return user.ErrLastOwner
	}
	delete(f.users, id)
	return nil
}

type fakePasswords struct {
	calls int
	keep  *uuid.UUID
	err   error
}

func (f *fakePasswords) ChangePassword(_ context.Context, _ uuid.UUID, cur, pw string, keep *uuid.UUID) error {
	f.calls++
	f.keep = keep
	if cur != "current-password-1" {
		return auth.ErrInvalidCredentials
	}
	return f.err
}

type fakeKeys struct {
	keys    map[uuid.UUID]*auth.APIKey
	created []auth.CreateAPIKeyInput
	regen   int
}

func (f *fakeKeys) List(context.Context) ([]*auth.APIKey, error) {
	var out []*auth.APIKey
	for _, k := range f.keys {
		out = append(out, k)
	}
	return out, nil
}
func (f *fakeKeys) Create(_ context.Context, by *uuid.UUID, in *auth.CreateAPIKeyInput) (*auth.APIKeyWithSecret, error) {
	f.created = append(f.created, *in)
	k := &auth.APIKey{ID: uuid.New(), Name: in.Name, KeyPrefix: "hm_ab12", Scopes: in.Scopes, IsActive: true, ExpiresAt: in.ExpiresAt, CreatedAt: time.Now()}
	f.keys[k.ID] = k
	return &auth.APIKeyWithSecret{APIKey: *k, PlainKey: "hm_ab12_THE-ONLY-SECRET"}, nil
}
func (f *fakeKeys) Revoke(_ context.Context, id uuid.UUID, _ *uuid.UUID) error {
	now := time.Now()
	if k, ok := f.keys[id]; ok {
		k.IsActive, k.RevokedAt = false, &now
	}
	return nil
}
func (f *fakeKeys) Regenerate(context.Context, uuid.UUID) (string, error) {
	f.regen++
	return "hm_ab12_REGENERATED", nil
}
func (f *fakeKeys) Delete(_ context.Context, id uuid.UUID) error { delete(f.keys, id); return nil }

type ownerRig struct {
	*authRig
	mgr     *fakeManager
	pw      *fakePasswords
	keys    *fakeKeys
	keyID   uuid.UUID
	flushed int
}

func newOwnerRig(t *testing.T, mail bool) *ownerRig {
	t.Helper()
	g := newAuthRig(t, mail)
	mgr := &fakeManager{users: map[uuid.UUID]*user.User{g.owner.ID: g.owner, g.editor.ID: g.editor}}
	pw := &fakePasswords{}
	kid := uuid.New()
	keys := &fakeKeys{keys: map[uuid.UUID]*auth.APIKey{kid: {ID: kid, Name: "Deploy", KeyPrefix: "hm_zz99", Scopes: []auth.APIKeyScope{auth.ScopeReadEntities}, IsActive: true}}}
	g.a.deps.Manager, g.a.deps.Passwords, g.a.deps.APIKeys = mgr, pw, keys
	r := &ownerRig{authRig: g, mgr: mgr, pw: pw, keys: keys, keyID: kid}
	g.a.deps.Invalidate = func(context.Context) { r.flushed++ }
	return r
}

func TestOwnerOnlyScreens(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	paths := []string{"/admin/settings/users", "/admin/settings/users/new", "/admin/settings/users/" + g.editor.ID.String(), "/admin/settings/users/" + g.editor.ID.String() + "/delete",
		"/admin/settings/general"}
	for _, p := range paths {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d, want 403", p, rec.Code)
		}
		if rec := g.do("GET", p, "", nil, nil); rec.Code != 303 {
			t.Errorf("anonymous GET %s = %d, want redirect to login", p, rec.Code)
		}
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != 200 {
			t.Errorf("owner GET %s = %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/settings/users/invite", "/admin/settings/users", "/admin/settings/users/" + g.editor.ID.String(), "/admin/settings/users/" + g.editor.ID.String() + "/reset",
		"/admin/settings/users/" + g.editor.ID.String() + "/delete", "/admin/settings/general/identity", "/admin/settings/general/contact", "/admin/settings/general/homepage", "/admin/settings/email/test", "/admin/settings/email/reset", "/admin/settings/email",
		"/admin/settings/api-keys", "/admin/settings/api-keys/" + g.keyID.String() + "/revoke"} {
		if rec := g.do("POST", p, "editor-token", url.Values{"email": {"x@example.com"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d, want 403", p, rec.Code)
		}
	}
	if rec := g.do("GET", "/admin/settings", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/general" {
		t.Errorf("settings index: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// Editors still reach their own profile.
	if rec := g.do("GET", "/admin/profile", "editor-token", nil, nil); rec.Code != 200 {
		t.Errorf("editor profile: %d", rec.Code)
	}
}

func TestUsersListGoldenA11yAndFilters(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/users", "owner-token", nil, nil)
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/users-list.html", normalizePage(userIDs(body, g)))
	for _, want := range []string{"owner@example.com", "ed@example.com", `href="/admin/settings/users/` + g.editor.ID.String() + `/delete"`, "Invite someone"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// You cannot delete yourself from the list.
	if strings.Contains(body, `href="/admin/settings/users/`+g.owner.ID.String()+`/delete"`) {
		t.Error("own row offers Delete")
	}
	// filter and sort
	body = g.do("GET", "/admin/settings/users?q=ed@&role=editor", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "owner@example.com") || !strings.Contains(body, "ed@example.com") {
		t.Errorf("filter: %s", body)
	}
	body = g.do("GET", "/admin/settings/users?q=zzzz", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "No users match") {
		t.Error("empty state missing")
	}
	// a fragment request returns the main block only
	frag := g.do("GET", "/admin/settings/users?q=ed", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "users"}).Body.String()
	if strings.Contains(frag, "<!doctype") || !strings.Contains(frag, `id="users"`) {
		t.Error("fragment response wrong")
	}
}

// userIDs makes the golden stable: user ids are random.
func userIDs(body string, g *ownerRig) string {
	body = strings.ReplaceAll(body, g.owner.ID.String(), "OWNER-ID")
	return strings.ReplaceAll(body, g.editor.ID.String(), "EDITOR-ID")
}

func TestInviteFlowShowsLinkOnceAndEmailsWhenConfigured(t *testing.T) {
	t.Parallel()

	for _, mail := range []bool{false, true} {
		g := newOwnerRig(t, mail)
		rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"New@Example.com"}, "role": {"owner"}}, nil)
		if rec.Code != 303 || strings.Contains(rec.Body.String(), "invite-token-") {
			t.Fatalf("mail=%v: the POST must redirect without the link: %d", mail, rec.Code)
		}
		// The link shows once, in the banner on the list the redirect lands on.
		body := g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, "https://acme.example/admin/accept?token=invite-token-") || !strings.Contains(body, "new@example.com") {
			t.Fatalf("mail=%v: %d\n%s", mail, rec.Code, body)
		}
		if mail && (len(g.mail.invites) != 1 || g.mail.invites[0].Role != "owner" || !strings.Contains(body, "We also emailed it")) {
			t.Errorf("invite email: %+v", g.mail.invites)
		}
		if !mail && (len(g.mail.invites) != 0 || !strings.Contains(body, "Email is not set up")) {
			t.Error("without SMTP the link must be shown and nothing sent")
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("a11y: %v", issues)
		}
		// The link is not in the list afterwards (a reload does not replay it); the pending invite is.
		list := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
		if strings.Contains(list, "invite-token-") || !strings.Contains(list, "Pending invitations") || !strings.Contains(list, "new@example.com") {
			t.Error("pending list wrong or token leaked")
		}
	}
	g := newOwnerRig(t, false)
	for name, email := range map[string]string{"existing": "olga@example.com", "invalid": "not-an-email"} {
		if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {email}, "role": {"editor"}}, nil); rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// revoke and resend
	g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"x@example.com"}, "role": {"editor"}}, nil)
	invs, _ := g.tokens.ListInvites(context.Background())
	rec := g.do("POST", "/admin/settings/users/invites/"+invs[0].ID.String()+"/resend", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || !strings.Contains(g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil).Body.String(), "invite-token-") {
		t.Errorf("resend: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	invs, _ = g.tokens.ListInvites(context.Background())
	if rec := g.do("POST", "/admin/settings/users/invites/"+invs[0].ID.String()+"/revoke", "owner-token", url.Values{}, nil); rec.Code != 303 {
		t.Errorf("revoke: %d", rec.Code)
	}
	if left, _ := g.tokens.ListInvites(context.Background()); len(left) != 0 {
		t.Errorf("%d invites left", len(left))
	}
}

func TestUserCreateEditDeleteAndLastOwnerGuard(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	good := url.Values{"email": {"nia@example.com"}, "name": {"Nia"}, "role": {"editor"}, "password": {"a-long-enough-pw-1"}}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", good, nil); rec.Code != 303 || len(g.mgr.created) != 1 || strings.Contains(g.mgr.created[0], "a-long-enough") {
		t.Fatalf("create: %d hash=%v", rec.Code, g.mgr.created)
	}
	for name, f := range map[string]url.Values{
		"short":     {"email": {"b@example.com"}, "role": {"editor"}, "password": {"short"}},
		"badmail":   {"email": {"nope"}, "role": {"editor"}, "password": {"a-long-enough-pw-1"}},
		"badrole":   {"email": {"c@example.com"}, "role": {"root"}, "password": {"a-long-enough-pw-1"}},
		"duplicate": {"email": {"NIA@example.com"}, "role": {"editor"}, "password": {"a-long-enough-pw-1"}},
	} {
		rec := g.do("POST", "/admin/settings/users", "owner-token", f, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	// Edit: change the editor into an owner.
	editURL := "/admin/settings/users/" + g.editor.ID.String()
	if rec := g.do("POST", editURL, "owner-token", url.Values{"name": {"Edie"}, "photo": {"/media/edie.jpg"}, "role": {"owner"}}, nil); rec.Code != 303 || g.mgr.users[g.editor.ID].Role != user.RoleOwner {
		t.Fatalf("promote: %d", rec.Code)
	}
	// Now two owners: demoting the other is fine; the last owner cannot be demoted.
	if rec := g.do("POST", editURL, "owner-token", url.Values{"name": {"Edie"}, "role": {"editor"}}, nil); rec.Code != 303 {
		t.Fatalf("demote with 2 owners: %d", rec.Code)
	}
	ownURL := "/admin/settings/users/" + g.owner.ID.String()
	rec := g.do("POST", ownURL, "owner-token", url.Values{"name": {"Olga"}, "role": {"editor"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "at least one owner") || g.mgr.users[g.owner.ID].Role != user.RoleOwner {
		t.Fatalf("last owner demotion: %d", rec.Code)
	}
	// Delete: confirmation page, self and last owner are blocked.
	del := g.do("GET", editURL+"/delete", "owner-token", nil, nil)
	if del.Code != 200 || !strings.Contains(del.Body.String(), "Delete user") {
		t.Fatalf("confirm page: %d", del.Code)
	}
	if issues := a11y.Check(del.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("delete a11y: %v", issues)
	}
	if rec := g.do("POST", ownURL+"/delete", "owner-token", url.Values{}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "cannot delete your own") {
		t.Errorf("self delete: %d", rec.Code)
	}
	if rec := g.do("POST", editURL+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || g.mgr.users[g.editor.ID] != nil {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/users/"+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown user: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/users/not-a-uuid", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestOwnerPasswordResetLink(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	rec := g.do("POST", "/admin/settings/users/"+g.editor.ID.String()+"/reset", "owner-token", url.Values{}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "https://acme.example/admin/reset?token=reset-token-") || !strings.Contains(rec.Body.String(), "Email is not set up") {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	g2 := newOwnerRig(t, true)
	g2.tokens.user = g2.editor
	g2.do("POST", "/admin/settings/users/"+g2.editor.ID.String()+"/reset", "owner-token", url.Values{}, nil)
	select {
	case d := <-g2.mail.sent:
		if !strings.Contains(d.URL, "/admin/reset?token=") {
			t.Errorf("mail %+v", d)
		}
	case <-time.After(time.Second):
		t.Error("reset email not sent")
	}
}

func TestProfile(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	g.a.deps.People = newFakePeople()
	page := g.do("GET", "/admin/profile", "editor-token", nil, nil)
	if issues := a11y.Check(page.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/profile.html", normalizePage(page.Body.String()))
	if rec := g.do("POST", "/admin/profile", "editor-token", url.Values{"name": {"Edie"}, "photo": {"/media/edie.jpg"}, "role": {"owner"}}, nil); rec.Code != 303 || g.mgr.users[g.editor.ID].Role != user.RoleEditor {
		t.Errorf("a profile save must never change the role: %d role=%s", rec.Code, g.mgr.users[g.editor.ID].Role)
	}
	bad := map[string]url.Values{
		"no current": {"password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}},
		"short":      {"current": {"current-password-1"}, "password": {"short"}, "confirm": {"short"}},
		"mismatch":   {"current": {"current-password-1"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-2"}},
		"wrong":      {"current": {"not-my-password"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}},
	}
	for name, f := range bad {
		rec := g.do("POST", "/admin/profile/password", "editor-token", f, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	callsBefore := g.pw.calls
	rec := g.do("POST", "/admin/profile/password", "editor-token", url.Values{"current": {"current-password-1"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}, nil)
	if rec.Code != 303 || g.pw.calls != callsBefore+1 || g.pw.keep == nil {
		t.Errorf("change: %d calls=%d keep=%v", rec.Code, g.pw.calls, g.pw.keep)
	}
}

func TestSiteSettings(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	if issues := a11y.Check(page.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/settings-general.html", normalizePage(strings.ReplaceAll(page.Body.String(), g.keyID.String(), "KEY-ID")))
	site := g.a.deps.Site.(*fakeSite)
	form := url.Values{"name": {"Acme"}, "tagline": {"Fresh daily"}, "url": {"https://acme.example/"}, "domain": {"www.acme.example"}, "language": {"fr"}, "timezone": {"Europe/Paris"}, "logo": {"/media/logo.png"}, "colour": {"#112233"}}
	site.cfg.PrimaryColor = "#abcdef"
	rec := g.do("POST", "/admin/settings/general/identity", "owner-token", form, nil)
	// The brand colour moved to Appearance: identity ignores a stray field.
	if rec.Code != 303 || site.cfg.CompanyName != "Acme" || site.cfg.Language != "fr" || site.cfg.Tagline != "Fresh daily" || site.cfg.PrimaryColor != "#abcdef" || g.flushed != 1 {
		t.Fatalf("save: %d %+v flushed=%d", rec.Code, site.cfg, g.flushed)
	}
	bad := url.Values{"name": {""}, "url": {"ftp://x"}, "language": {"!!"}, "timezone": {"Nowhere/Land"}, "logo": {"javascript:alert(1)"}, "colour": {"red"}, "domain": {"a b/c"}, "tagline": {strings.Repeat("x", 201)}}
	rec = g.do("POST", "/admin/settings/general/identity", "owner-token", bad, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "There are 7 problems") {
		t.Fatalf("errors: %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("error page a11y: %v", issues)
	}
	if site.cfg.CompanyName != "Acme" {
		t.Error("an invalid submission changed the stored settings")
	}
}

func TestBusinessDetailsSettings(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	body := page.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Business details", `href="#contact"`, "Save business details", "Opening hours", "Profiles &amp; rating", "Commerce", "Add languages"} {
		if !strings.Contains(body, want) {
			t.Errorf("General lacks %q", want)
		}
	}
	if !strings.Contains(body, "Contact points, opening hours, area served, currency, price range and geo region are used on locale pages.") ||
		strings.Contains(body, "map addresses") {
		t.Error("the single-locale hint does not name the locale-only fields")
	}
	if strings.Contains(body, "Contact details") {
		t.Error("General still says Contact details")
	}
	form := url.Values{
		"notify": {"boss@example.com"}, "biz.phone": {"+1 555 0100"}, "biz.hours_note": {"Mon-Fri 9-5"},
		"biz.address.street": {"1 Main St"}, "biz.address.city": {"Springfield"}, "biz.address.state": {"IL"},
		"biz.address.postal_code": {"62701"}, "biz.address.country": {"Exampleland"},
		"biz.tax_id": {"TX-1"}, "biz.currency": {"USD"},
		"_present.biz.opening_hours": {"1"}, "_present.biz.opening_hours.0.days": {"1"},
		"biz.opening_hours.0.days": {"Monday, Friday"}, "biz.opening_hours.0.opens": {"09:00"}, "biz.opening_hours.0.closes": {"17:00"},
	}
	if rec := g.do("POST", "/admin/settings/general/contact", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if g.flushed != 1 {
		t.Errorf("render cache invalidated %d times, want 1", g.flushed)
	}
	cfg := g.a.deps.Site.(*fakeSite).cfg
	if cfg.Phone != "+1 555 0100" || cfg.BusinessHours != "Mon-Fri 9-5" || !strings.Contains(string(cfg.PostalAddress), `"city":"Springfield"`) {
		t.Errorf("legacy columns = %q %q %s", cfg.Phone, cfg.BusinessHours, cfg.PostalAddress)
	}
	for _, mustNot := range []string{"Springfield", "0100", "Mon-Fri"} {
		if strings.Contains(string(cfg.BusinessProfile), mustNot) {
			t.Errorf("profile column repeats a legacy value %q: %s", mustNot, cfg.BusinessProfile)
		}
	}
	if !strings.Contains(string(cfg.BusinessProfile), `"tax_id":"TX-1"`) || !strings.Contains(string(cfg.BusinessProfile), `"Monday"`) {
		t.Errorf("profile column = %s", cfg.BusinessProfile)
	}
	again := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`value="boss@example.com"`, `value="Springfield"`, `value="62701"`, `value="&#43;1 555 0100"`, `value="TX-1"`, `value="Monday, Friday"`, `value="17:00"`} {
		if !strings.Contains(again, want) {
			t.Errorf("not persisted: %s", want)
		}
	}
}

// Coordinates and the map address are locale card fields: a site-wide save
// neither validates nor stores them.
func TestBusinessDetailsSaveIgnoresSiteWideCoordinates(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	g.a.deps.Site.(*fakeSite).cfg.BusinessProfile = []byte(`{"address":{"lat":"12.5","map_url":"https://example.test/map","po_box":"PO 1"}}`)
	rec := g.do("POST", "/admin/settings/general/contact", "owner-token", url.Values{
		"biz.address.lat": {"999"}, "biz.address.lng": {"12"}, "biz.address.map_url": {"https://example.test/m"}, "biz.address.po_box": {"PO 2"},
	}, nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := string(g.a.deps.Site.(*fakeSite).cfg.BusinessProfile)
	for _, gone := range []string{"lat", "lng", "map_url", "12.5", "example.test/m"} {
		if strings.Contains(got, gone) {
			t.Errorf("profile column keeps %q: %s", gone, got)
		}
	}
	if !strings.Contains(got, `"po_box":"PO 2"`) {
		t.Errorf("PO box not saved: %s", got)
	}
}

func TestBusinessDetailsPrefillFromLegacyColumns(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	site := g.a.deps.Site.(*fakeSite)
	site.cfg.Phone, site.cfg.BusinessHours = "555 0199", "Weekdays"
	site.cfg.PostalAddress = []byte(`{"street":"2 Old Road","city":"Oldtown","state":"","postal_code":"9","country":"Exampleland"}`)
	body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`value="555 0199"`, `value="Weekdays"`, `value="2 Old Road"`, `value="Oldtown"`} {
		if !strings.Contains(body, want) {
			t.Errorf("upgrade lost %s", want)
		}
	}
}

func TestBusinessDetailsInvalidSaveKeepsValues(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("POST", "/admin/settings/general/contact", "owner-token", url.Values{
		"notify": {"bad"}, "biz.phone": {strings.Repeat("1", 51)}, "biz.rating.value": {"9"}, "biz.address.city": {"Keepville"},
	}, nil)
	if rec.Code != 422 {
		t.Fatalf("invalid: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`value="Keepville"`, `href="#sf-biz-rating-value"`, `href="#notify"`, "Enter a number between 0 and 5."} {
		if !strings.Contains(body, want) {
			t.Errorf("422 page lacks %q", want)
		}
	}
	if g.flushed != 0 {
		t.Error("a failed save invalidated the cache")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestBusinessDetailsAddRowReRendersWithoutSaving(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("POST", "/admin/settings/general/contact", "owner-token", url.Values{
		"biz.tax_id": {"TX-9"}, "_present.biz.opening_hours": {"1"}, "_add.biz.opening_hours": {"1"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("add row: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="TX-9"`) || !strings.Contains(body, "<details open>") {
		t.Errorf("the new row is not open or the typed value is lost")
	}
	if g.flushed != 0 || len(g.a.deps.Site.(*fakeSite).cfg.BusinessProfile) != 0 {
		t.Error("adding a row saved the form")
	}
}

func TestGeneralEmailCardLinksToEmailTab(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	page := g.do("GET", "/admin/settings/general", "owner-token", nil, nil)
	body := page.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Email is set up", `href="/admin/settings/email"`, "Manage email"} {
		if !strings.Contains(body, want) {
			t.Errorf("General lacks %q", want)
		}
	}
	for _, bad := range []string{"SMTP_HOST", "Send a test email", "/email/test"} {
		if strings.Contains(body, bad) {
			t.Errorf("General still has %q; the form lives in Settings > Email", bad)
		}
	}
	off := newOwnerRig(t, false)
	if body := off.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Email is not set up") || !strings.Contains(body, `href="/admin/settings/email"`) {
		t.Error("an unconfigured site must say so and link to Settings > Email")
	}
}

func list2(g *ownerRig) string {
	return g.do("GET", "/admin/settings/api-keys", "owner-token", nil, nil).Body.String()
}

func TestAPIKeysSecretShownOnce(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	if body := g.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, "hm_zz99") || strings.Contains(body, "Create an API key") {
		t.Error("API keys are on their own page, not General")
	}
	list := g.do("GET", "/admin/settings/api-keys", "owner-token", nil, nil)
	body := list.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	if !strings.Contains(body, "hm_zz99…") || strings.Contains(body, "THE-ONLY-SECRET") {
		t.Error("list must show the prefix only")
	}
	compareGolden(t, "pages/settings-api.html", normalizePage(strings.ReplaceAll(body, g.keyID.String(), "KEY-ID")))
	rec := g.do("POST", "/admin/settings/api-keys", "owner-token", url.Values{"name": {"CI"}, "scopes": {"read:entities", "write:entities", "bogus"}, "expiry": {"30"}}, nil)
	// POST, redirect, GET: the secret is shown in a banner on the list GET that
	// follows, once, so a reload cannot mint another key or replay the secret.
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/settings/api-keys?created=") || !strings.Contains(rec.Header().Get("Location"), "&secret=") {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	secretURL := rec.Header().Get("Location")
	rec = g.do("GET", secretURL, "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hm_ab12_THE-ONLY-SECRET") || !strings.Contains(rec.Body.String(), `role="status"`) {
		t.Fatalf("list with the secret banner: %d", rec.Code)
	}
	if again := g.do("GET", secretURL, "owner-token", nil, nil); again.Code != 200 || strings.Contains(again.Body.String(), "THE-ONLY-SECRET") || strings.Contains(again.Body.String(), "secret-banner") {
		t.Errorf("reloading the list with the secret: %d", again.Code)
	}
	if len(g.keys.created) != 1 {
		t.Errorf("a key was minted by the reload: %d", len(g.keys.created))
	}
	if other := g.do("POST", "/admin/settings/api-keys", "owner-token", url.Values{"name": {"CI2"}, "scopes": {"read:entities"}}, nil); strings.Contains(g.do("GET", other.Header().Get("Location"), "editor-token", nil, nil).Body.String(), "THE-ONLY-SECRET") {
		t.Error("another user could read the secret")
	}
	rec = g.do("POST", "/admin/settings/api-keys", "owner-token", url.Values{"name": {"CI"}, "scopes": {"read:entities", "write:entities", "bogus"}, "expiry": {"30"}}, nil)
	rec = g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil)
	if len(g.keys.created) != 3 || len(g.keys.created[2].Scopes) != 2 || g.keys.created[2].ExpiresAt == nil {
		t.Errorf("created with %+v", g.keys.created)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the page showing the secret must not be cacheable")
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("list with banner a11y: %v", issues)
	}
	// The secret is gone from the next view.
	if again := list2(g); strings.Contains(again, "THE-ONLY-SECRET") {
		t.Error("the secret came back")
	}
	for name, f := range map[string]url.Values{"no name": {"scopes": {"read:entities"}}, "no scope": {"name": {"x"}}} {
		rec := g.do("POST", "/admin/settings/api-keys", "owner-token", f, nil)
		if rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	id := g.keyID.String()
	baseline := len(g.keys.keys)
	// Destructive actions: a confirmation page without JS, a dialog hook with it.
	for _, act := range []string{"regenerate", "revoke", "delete"} {
		rec := g.do("GET", "/admin/settings/api-keys/"+id+"/"+act, "owner-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, `action="/admin/settings/api-keys/`+id+`/`+act+`"`) || !strings.Contains(body, `method="post"`) {
			t.Errorf("confirmation page for %s: %d\n%s", act, rec.Code, body)
		}
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s confirmation a11y: %v", act, issues)
		}
		if g.keys.regen != 0 && act == "regenerate" || len(g.keys.keys) != baseline {
			t.Errorf("GET %s must change nothing", act)
		}
		if !strings.Contains(list2(g), `data-hm-confirm=`) || !strings.Contains(list2(g), `href="/admin/settings/api-keys/`+id+`/`+act+`"`) {
			t.Errorf("row action %s lacks its dialog hook", act)
		}
	}
	if rec := g.do("GET", "/admin/settings/api-keys/"+id+"/explode", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown action: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/settings/api-keys/"+id+"/regenerate", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || g.keys.regen != 1 {
		t.Errorf("regenerate: %d", rec.Code)
	}
	if rec = g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil); !strings.Contains(rec.Body.String(), "REGENERATED") {
		t.Errorf("regenerated secret not shown: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/api-keys/"+id+"/revoke", "owner-token", url.Values{}, nil); rec.Code != 303 || g.keys.keys[g.keyID].IsActive {
		t.Errorf("revoke: %d", rec.Code)
	}
	if body := list2(g); !strings.Contains(body, "Revoked") {
		t.Error("revoked status missing")
	}
	if rec := g.do("POST", "/admin/settings/api-keys/"+id+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(g.keys.keys) != baseline-1 {
		t.Errorf("delete: %d keys=%d", rec.Code, len(g.keys.keys))
	}
	if rec := g.do("POST", "/admin/settings/api-keys/not-a-uuid/revoke", "owner-token", url.Values{}, nil); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// Every Go-owned screen is covered by the a11y sweep above; this guards the
// list: a new page template without a test fails here.
func TestEveryPageTemplateIsExercised(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	_, pages := g.a.state()
	covered := map[string]bool{
		"errors/error": true, "errors/bare": true, "auth/login": true, "auth/forgot": true, "auth/reset": true, "auth/accept": true, "auth/sudo": true, // sudo: sudo_test.go
		"setup/account": true, "setup/admin-address": true, "setup/admin-address-strength": true, "setup/site": true, "setup/email": true, "setup/theme": true, "setup/stats": true, "setup/content": true, "setup/done": true,
		"users/list": true, "users/edit": true, "users/delete": true, "users/link": true, "profile/index": true, "profile/complete": true, "people/list": true, "people/new": true, "people/duplicates": true,
		"settings/general": true, "settings/locales": true, "settings/locale-new": true, "settings/locale-edit": true, "settings/locales-addresses": true, "settings/locales-business": true, "settings/locale-convert": true, "settings/locale-convert-confirm": true, "settings/security-keep": true, "settings/security-change": true, "settings/security-admin-address": true, "settings/export": true, "settings/performance": true, "settings/spam": true, "settings/email": true, "settings/updates": true, "settings/backups": true, "settings/apikeynew": true, "settings/api": true, "settings/webhooks": true, "settings/webhooknew": true, "settings/webhookdeliveries": true, "users/invite": true, "confirm": true, "audit/index": true, // audit_log_test.go
		// v1-11: content_test.go
		"entities/list": true, "entities/new": true, "entities/start": true, "entities/builder": true, "entities/edit": true, "entities/versions": true, "entities/compare": true, "entities/version": true,
		"schemas/list": true, "schemas/edit": true, "taxonomies/list": true, "taxonomies/new": true, "taxonomies/term": true, "taxonomies/merge": true,
		"media/list": true, "media/new": true, "settings/upload": true, "media/detail": true, "media/picker": true,
		"entities/parent":   true,                            // a fragment: content_forge_test.go
		"entities/blogedit": true, "entities/seopanel": true, // v1-13: blog_editor_test.go
		"entities/builderseo":  true,                              // a fragment: builder_seo_test.go
		"dev/styleguide":       true,                              // styleguide_test.go (Dev only)
		"settings/seo-general": true, "settings/seo-titles": true, // seo_settings_test.go
	}
	for _, names := range areaPages {
		for _, n := range names {
			covered[n] = true
		}
	}
	for name := range pages {
		if !covered[name] {
			t.Errorf("page %q has no a11y test", name)
		}
	}
	for name := range covered {
		if _, ok := pages[name]; !ok {
			t.Errorf("%q is listed as covered but does not exist", name)
		}
	}
	_ = http.StatusOK
}

func TestUserAndInviteRemovalAskFirst(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}}, nil)
	var inviteID string
	for _, inv := range g.tokens.invites {
		inviteID = inv.ID.String()
	}
	list := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`href="/admin/settings/users/invites/` + inviteID + `/revoke" data-hm-confirm=`,
		`href="/admin/settings/users/` + g.editor.ID.String() + `/delete" data-hm-confirm=`,
	} {
		if !strings.Contains(list, want) {
			t.Errorf("missing dialog hook %q", want)
		}
	}
	rec := g.do("GET", "/admin/settings/users/invites/"+inviteID+"/revoke", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `action="/admin/settings/users/invites/`+inviteID+`/revoke"`) || len(g.tokens.invites) != 1 {
		t.Errorf("revoke confirmation page: %d, invites=%d", rec.Code, len(g.tokens.invites))
	}
	if rec := g.do("GET", "/admin/settings/users/invites/"+uuid.NewString()+"/revoke", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown invite: %d", rec.Code)
	}
	if body := g.do("GET", "/admin/settings/users/"+uuid.NewString(), "owner-token", nil, nil).Body.String(); !strings.Contains(body, `href="/admin/settings/users"><span>Back to users`) {
		t.Error("user-not-found should lead back to the users list")
	}
}

// The reset link page shows the link in the shared one-time banner, with Copy.
func TestOwnerPasswordResetLinkUsesSecretBanner(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	body := g.do("POST", "/admin/settings/users/"+g.editor.ID.String()+"/reset", "owner-token", url.Values{}, nil).Body.String()
	for _, want := range []string{`id="secret-banner"`, `data-hm-copy-field="secret-value"`, "reset?token=reset-token-"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestRoleBadgeKinds(t *testing.T) {
	t.Parallel()

	for role, want := range map[string]string{"owner": "info", "admin": "warning", "editor": "neutral", "": "neutral"} {
		if got := roleBadgeKind(role); got != want {
			t.Errorf("roleBadgeKind(%q) = %q, want %q", role, got, want)
		}
	}
}

// "Add with a password instead" carries the invite address into the new-user form.
func TestUserNewPrefillsEmail(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	if body := g.do("GET", "/admin/settings/users/new?email=ann@x.test", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `value="ann@x.test"`) {
		t.Error("the carried address must prefill the form")
	}
	if body := g.do("GET", "/admin/settings/users/new?email=%3Cb%3E", "owner-token", nil, nil).Body.String(); strings.Contains(body, "&lt;b&gt;") {
		t.Error("a bad address must be ignored")
	}
}

// zeroCountBadge matches a neutral badge whose label is "0", with or without the default dot.
var zeroCountBadge = regexp.MustCompile(`class="hm-badge hm-badge--neutral">(<span class="hm-badge__dot"[^>]*></span>)?0<`)

// A person who never signed in shows a muted "Never"; when the invitations cannot be loaded the
// header shows the error and no "0" count.
func TestUsersListMutesNeverAndHidesCountOnInviteError(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	list := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, `<span class="hm-table__suffix">Never</span>`) {
		t.Errorf("a user who never signed in needs a muted Never:\n%s", list)
	}
	g.tokens.listErr = errors.New("db down")
	list = g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, "The pending invitations could not be loaded.") || zeroCountBadge.MatchString(list) {
		t.Errorf("invite error: message and no 0 badge expected:\n%s", list)
	}
}
