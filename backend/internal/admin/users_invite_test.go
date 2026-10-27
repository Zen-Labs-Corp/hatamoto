package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func inviteForm(addr string) url.Values {
	return url.Values{"email": {addr}, "role": {"editor"}}
}

func TestInviteNewPageRendersForm(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/users/invite", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `action="/admin/settings/users/invite"`) || !strings.Contains(body, `name="email"`) || !strings.Contains(body, "/admin/settings/users/new") {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
}

func TestUsersListHasInviteDialogAndOpener(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	body := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`data-hm-open="invite"`, `href="/admin/settings/users/invite"`, `form="invite-form"`, `<dialog class="hm-dialog" id="invite"`, "Add with a password instead"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(body, "hm-reveal") {
		t.Error("the reveal card is back")
	}
}

func TestInviteRedirectsAndShowsSecretOnce(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), nil)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/settings/users?created=") || !strings.Contains(loc, "&secret=") {
		t.Fatalf("%d %q\n%s", rec.Code, loc, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "invite-token-") {
		t.Error("the POST response carries the link")
	}
	page := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	for _, want := range []string{`role="status"`, `id="secret-banner"`, "/admin/accept?token=invite-token-", "Email is not set up", "hm-row--new", "new@example.com"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	clean := strings.SplitN(loc, "&secret=", 2)[0]
	if again := g.do("GET", loc, "owner-token", nil, nil).Body.String(); strings.Contains(again, "secret-banner") {
		t.Error("the secret showed twice")
	}
	if again := g.do("GET", clean, "owner-token", nil, nil).Body.String(); strings.Contains(again, "invite-token-") {
		t.Error("the link leaked after it was taken")
	}
}

func TestInviteBadSecretIgnored(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	body := g.do("GET", "/admin/settings/users?created=not-a-uuid&secret=bogus", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "secret-banner") || strings.Contains(body, "not-a-uuid") {
		t.Fatal("bad created or secret echoed")
	}
	if !strings.Contains(body, "can no longer be displayed. Invite the person again if you lost it.") {
		t.Error("an unknown or expired secret shows no notice")
	}
}

func TestInviteErrorRerendersNewPage(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("not an address"), nil)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "Enter a valid email address.") || !strings.Contains(body, `value="not an address"`) || !strings.Contains(body, `action="/admin/settings/users/invite"`) {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
}

func TestInviteWithoutSiteAddressShowsNoteInBanner(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), map[string]string{"X-Forwarded-Host": "evil.test"})
	if len(g.mail.invites) != 0 {
		t.Fatalf("invites sent %d", len(g.mail.invites))
	}
	page := g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil).Body.String()
	if !strings.Contains(page, "site address is not set") || strings.Contains(page, "evil.test") {
		t.Fatalf("banner note or forged host:\n%s", page)
	}
}

func TestInviteResendUsesSameRedirect(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, false)
	g.do("POST", "/admin/settings/users/invite", "owner-token", inviteForm("new@example.com"), nil)
	var id string
	for _, inv := range g.tokens.invites {
		id = inv.ID.String()
	}
	rec := g.do("POST", "/admin/settings/users/invites/"+id+"/resend", "owner-token", url.Values{}, nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "?created=") || !strings.Contains(loc, "&secret=") {
		t.Fatalf("%d %q", rec.Code, loc)
	}
}
