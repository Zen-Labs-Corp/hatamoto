package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/user"
)

// accessRigWith is an owner rig whose site is in Sydney, so a date entered in
// the form is not the same instant in UTC, with the audit events captured.
func accessRigWith(t *testing.T) (*ownerRig, *[]AuditEvent) {
	t.Helper()
	g := newOwnerRig(t, true)
	g.a.deps.Site.(*fakeSite).cfg.Timezone = "Australia/Sydney"
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	return g, &events
}

func futureDay(days int) string { return time.Now().AddDate(0, 0, days).Format("2006-01-02") }

func TestInviteWithAccessEnd(t *testing.T) {
	t.Parallel()

	g, events := accessRigWith(t)
	day := futureDay(10)
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}, "access_ends": {day}}, nil)
	if rec.Code != 303 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	sydney, _ := time.LoadLocation("Australia/Sydney")
	want, _ := user.AccessEndFromDate(day, sydney)
	var got *time.Time
	for _, inv := range g.tokens.invites {
		got = inv.AccessEndsAt
	}
	if got == nil || !got.Equal(want) {
		t.Fatalf("invite end = %v, want %v (start of the next day in the site zone, UTC)", got, want)
	}
	if len(*events) != 1 || (*events)[0].Action != "user.access_changed" || (*events)[0].Detail["invite"] != true {
		t.Fatalf("audit %+v", *events)
	}

	// The invites table shows the day chosen, not the UTC instant.
	body := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Access ends") || !strings.Contains(body, fmtDate(want.Add(-time.Second).In(sydney))) {
		t.Errorf("invites table lacks the end:\n%s", body)
	}

	// Resend keeps the date.
	var id string
	for _, inv := range g.tokens.invites {
		id = inv.ID.String()
	}
	if rec := g.do("POST", "/admin/settings/users/invites/"+id+"/resend", "owner-token", nil, nil); rec.Code != 303 {
		t.Fatalf("resend: %d", rec.Code)
	}
	if len(g.tokens.invites) != 1 {
		t.Fatalf("invites: %d", len(g.tokens.invites))
	}
	for _, inv := range g.tokens.invites {
		if inv.AccessEndsAt == nil || !inv.AccessEndsAt.Equal(want) {
			t.Fatalf("resent invite end = %v", inv.AccessEndsAt)
		}
	}
}

func TestInviteAccessEndPastRefused(t *testing.T) {
	t.Parallel()

	g, _ := accessRigWith(t)
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}, "access_ends": {"2020-01-01"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Choose a date from today on.") || !strings.Contains(body, `aria-invalid="true"`) || !strings.Contains(body, `value="2020-01-01"`) {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
	if len(g.tokens.invites) != 0 {
		t.Error("an invite was created")
	}
	rec = g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}, "access_ends": {"soon"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a date as year-month-day.") {
		t.Fatalf("bad format: %d", rec.Code)
	}
}

func TestInvitePageHasLabelledDateInput(t *testing.T) {
	t.Parallel()

	g, _ := accessRigWith(t)
	rec := g.do("GET", "/admin/settings/users/invite", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{`for="invite-access-ends">Access ends on</label>`, `type="date"`, `aria-describedby="invite-access-ends-help"`, `id="invite-access-ends-help"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
}

func TestUserEditSavesShowsAndClearsAccessEnd(t *testing.T) {
	t.Parallel()

	g, events := accessRigWith(t)
	path := "/admin/settings/users/" + g.editor.ID.String()
	day := futureDay(20)
	form := url.Values{"name": {"Ed"}, "role": {"editor"}, "access_ends": {day}}
	if rec := g.do("POST", path, "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	if g.mgr.users[g.editor.ID].AccessEndsAt == nil {
		t.Fatal("end not stored")
	}
	if len(*events) != 1 || (*events)[0].Action != "user.access_changed" || (*events)[0].Detail["from"] != nil || (*events)[0].Detail["to"] == nil {
		t.Fatalf("audit %+v", *events)
	}
	body := g.do("GET", path, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `value="`+day+`"`) || !strings.Contains(body, "<span>Clear date</span>") || !strings.Contains(body, `name="clear_access"`) {
		t.Fatalf("edit form lacks the date or the clear checkbox:\n%s", body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}

	// Saving again unchanged is not an audited change.
	_ = g.do("POST", path, "owner-token", form, nil)
	if len(*events) != 1 {
		t.Errorf("unchanged save audited: %+v", *events)
	}

	// Clear date.
	form.Set("clear_access", "1")
	if rec := g.do("POST", path, "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if g.mgr.users[g.editor.ID].AccessEndsAt != nil {
		t.Fatal("end not cleared")
	}
	if len(*events) != 2 || (*events)[1].Detail["to"] != nil || (*events)[1].Detail["from"] == nil {
		t.Fatalf("clear audit %+v", *events)
	}
	if body := g.do("GET", path, "owner-token", nil, nil).Body.String(); strings.Contains(body, "Clear date") {
		t.Error("clear checkbox shown without a date")
	}
}

func TestUserEditPastDateRules(t *testing.T) {
	t.Parallel()

	g, _ := accessRigWith(t)
	path := "/admin/settings/users/" + g.editor.ID.String()
	form := url.Values{"name": {"Ed"}, "role": {"editor"}, "access_ends": {"2020-01-01"}}
	rec := g.do("POST", path, "owner-token", form, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose a date from today on.") {
		t.Fatalf("new past date: %d", rec.Code)
	}
	// A user who already ended can still be edited without touching the date.
	sydney, _ := time.LoadLocation("Australia/Sydney")
	end, _ := user.AccessEndFromDate("2020-01-01", sydney)
	g.mgr.users[g.editor.ID].AccessEndsAt = &end
	if rec := g.do("POST", path, "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("unchanged past date: %d\n%s", rec.Code, rec.Body.String())
	}
	body := g.do("GET", path, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Access ended on 1 Jan 2020") || !strings.Contains(body, `value="2020-01-01"`) {
		t.Errorf("ended note or date missing:\n%s", body)
	}
}

func TestUserEditLastOpenOwnerNeedsOtherOwner(t *testing.T) {
	t.Parallel()

	g, events := accessRigWith(t)
	path := "/admin/settings/users/" + g.owner.ID.String()
	form := url.Values{"name": {"Olga"}, "role": {"owner"}, "access_ends": {futureDay(5)}}
	rec := g.do("POST", path, "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Another owner without an end date is needed first.") || !strings.Contains(body, `href="#access-ends"`) {
		t.Fatalf("%d\n%s", rec.Code, body)
	}
	if g.mgr.users[g.owner.ID].AccessEndsAt != nil || len(*events) != 0 {
		t.Error("the refused date was stored or audited")
	}
}

func TestAddWithPasswordTakesAccessEnd(t *testing.T) {
	t.Parallel()

	g, events := accessRigWith(t)
	form := url.Values{"email": {"pw@example.com"}, "name": {"Pw"}, "role": {"editor"}, "password": {"a-long-enough-password"}, "access_ends": {futureDay(3)}}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	var made *user.User
	for _, u := range g.mgr.users {
		if u.Email == "pw@example.com" {
			made = u
		}
	}
	if made == nil || made.AccessEndsAt == nil {
		t.Fatalf("created user: %+v", made)
	}
	if len(*events) != 1 || (*events)[0].Detail["created"] != true {
		t.Fatalf("audit %+v", *events)
	}
}

func TestUsersListAccessColumn(t *testing.T) {
	t.Parallel()

	g, _ := accessRigWith(t)
	sydney, _ := time.LoadLocation("Australia/Sydney")
	past, _ := user.AccessEndFromDate("2026-11-02", sydney)
	future, _ := user.AccessEndFromDate(futureDay(30), sydney)
	g.mgr.users[g.editor.ID].AccessEndsAt = &past
	name := "Later"
	later := &user.User{ID: uuid.New(), Email: "later@example.com", Role: user.RoleEditor, Name: &name, AccessEndsAt: &future}
	g.mgr.users[later.ID] = later

	body := g.do("GET", "/admin/settings/users", "owner-token", nil, nil).Body.String()
	for _, want := range []string{">Access</", "No end date", "Ended 2 Nov 2026", "Blocked", "Ends " + fmtDate(future.Add(-time.Second).In(sydney)), "Extend"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
}

// limitedOwner gives the signed-in owner an end date ten days out.
func limitedOwner(t *testing.T) (*ownerRig, time.Time) {
	t.Helper()
	g, _ := accessRigWith(t)
	own := time.Now().Add(10 * 24 * time.Hour).UTC()
	g.mgr.users[g.owner.ID].AccessEndsAt = &own
	return g, own
}

func TestLimitedOwnerCannotChangeOwnEndDate(t *testing.T) {
	t.Parallel()

	g, own := limitedOwner(t)
	path := "/admin/settings/users/" + g.owner.ID.String()
	for name, form := range map[string]url.Values{
		"clear":  {"name": {"Olga"}, "role": {"owner"}, "clear_access": {"1"}},
		"empty":  {"name": {"Olga"}, "role": {"owner"}},
		"extend": {"name": {"Olga"}, "role": {"owner"}, "access_ends": {futureDay(60)}},
	} {
		if rec := g.do("POST", path, "owner-token", form, nil); rec.Code == 303 {
			t.Errorf("%s: saved", name)
		}
		if got := g.mgr.users[g.owner.ID].AccessEndsAt; got == nil || !got.Equal(own) {
			t.Fatalf("%s: end is now %v", name, got)
		}
	}
	other := "/admin/settings/users/" + g.editor.ID.String()
	if rec := g.do("POST", other, "owner-token", url.Values{"name": {"Ed"}, "role": {"editor"}, "access_ends": {futureDay(5)}}, nil); rec.Code == 303 {
		t.Error("a limited owner set another user's date")
	}
}

func TestLimitedOwnerCannotCreateOrPromoteBeyondOwnEnd(t *testing.T) {
	t.Parallel()

	g, _ := limitedOwner(t)
	pw := func(email, day string) url.Values {
		f := url.Values{"email": {email}, "name": {"N"}, "role": {"editor"}, "password": {"a-long-enough-password"}}
		if day != "" {
			f.Set("access_ends", day)
		}
		return f
	}
	invite := func(email, day string) url.Values {
		f := url.Values{"email": {email}, "role": {"editor"}}
		if day != "" {
			f.Set("access_ends", day)
		}
		return f
	}
	has := func(email string) bool {
		for _, u := range g.mgr.users {
			if u.Email == email {
				return true
			}
		}
		return false
	}
	for name, tc := range map[string]struct{ path, email string }{
		"add no date": {"/admin/settings/users", "a@example.com"},
	} {
		if rec := g.do("POST", tc.path, "owner-token", pw(tc.email, ""), nil); rec.Code == 303 || has(tc.email) {
			t.Errorf("%s: created", name)
		}
	}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", pw("b@example.com", futureDay(40)), nil); rec.Code == 303 || has("b@example.com") {
		t.Error("add later date: created")
	}
	if rec := g.do("POST", "/admin/settings/users", "owner-token", pw("c@example.com", futureDay(3)), nil); rec.Code != 303 || !has("c@example.com") {
		t.Errorf("add earlier date: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", invite("d@example.com", ""), nil); rec.Code == 303 || len(g.tokens.invites) != 0 {
		t.Error("invite no date: issued")
	}
	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", invite("e@example.com", futureDay(40)), nil); rec.Code == 303 || len(g.tokens.invites) != 0 {
		t.Error("invite later date: issued")
	}
	if rec := g.do("POST", "/admin/settings/users/invite", "owner-token", invite("f@example.com", futureDay(3)), nil); rec.Code != 303 || len(g.tokens.invites) != 1 {
		t.Errorf("invite earlier date: %d", rec.Code)
	}
	// Promoting an editor who has no end date.
	path := "/admin/settings/users/" + g.editor.ID.String()
	if rec := g.do("POST", path, "owner-token", url.Values{"name": {"Ed"}, "role": {"owner"}}, nil); rec.Code == 303 || g.mgr.users[g.editor.ID].Role != user.RoleEditor {
		t.Error("promoted a user with no end date")
	}
}

func TestOpenOwnerStillSetsDates(t *testing.T) {
	t.Parallel()

	g, _ := accessRigWith(t)
	path := "/admin/settings/users/" + g.editor.ID.String()
	if rec := g.do("POST", path, "owner-token", url.Values{"name": {"Ed"}, "role": {"editor"}, "access_ends": {futureDay(90)}}, nil); rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
}

// An end set over the API at another time of day still counts as unchanged
// when the form sends back the same day, so a limited owner can save a name.
func TestLimitedOwnerCanEditUserWithOddEndTime(t *testing.T) {
	t.Parallel()

	g, _ := limitedOwner(t)
	sydney, _ := time.LoadLocation("Australia/Sydney")
	odd := time.Now().Add(3*24*time.Hour + 5*time.Hour).UTC()
	g.mgr.users[g.editor.ID].AccessEndsAt = &odd
	form := url.Values{"name": {"Ed"}, "role": {"editor"}, "access_ends": {user.AccessEndDay(odd, sydney)}}
	if rec := g.do("POST", "/admin/settings/users/"+g.editor.ID.String(), "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
	}
	if got := g.mgr.users[g.editor.ID].AccessEndsAt; got == nil || !got.Equal(odd) {
		t.Errorf("end is now %v", got)
	}
}
