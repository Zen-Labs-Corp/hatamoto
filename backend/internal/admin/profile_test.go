package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/person"
)

func newProfileRig(t *testing.T) (*ownerRig, *fakePeople) {
	t.Helper()
	g := newOwnerRig(t, false)
	people := newFakePeople()
	g.a.deps.People = people
	return g, people
}

func completeForm(extra url.Values) url.Values {
	f := url.Values{"name": {"Edie Editor"}, "photo": {"/media/edie.jpg"}, "public": {"false"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestCompleteProfilePage(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	rec := g.do("GET", "/admin/profile/complete?next=/admin/settings", "editor-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Complete your profile", "This is how you appear as an author on the site.",
		`name="name"`, `name="photo"`, `name="job_title"`, `name="short_bio"`, `name="linkedin"`,
		"Show my public profile", "/author/your-name", "Save and continue", `name="next" value="/admin/settings"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Name is the natively required field; the photo is checked on the server so
	// its styled error shows (the address input is hidden when the picker works).
	for id, required := range map[string]bool{"name": true, "photo": false, "job_title": false, "short_bio": false, "linkedin": false} {
		tag := inputTag(body, id)
		if strings.Contains(tag, " required") != required {
			t.Errorf("field %s required=%v, tag %q", id, !required, tag)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	form := body[strings.Index(body, `<form method="post" action="/admin/profile/complete"`):]
	if strings.ContainsAny(form[:strings.Index(form, "</form>")], "\u2013\u2014") {
		t.Error("em or en dash in the profile form copy")
	}
	compareGolden(t, "pages/profile-complete.html", normalizePage(body))
}

// inputTag returns the opening tag of the control with the given id.
func inputTag(body, id string) string {
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<")
	end := strings.Index(body[i:], ">")
	return body[start : i+end]
}

func TestCompleteProfileRequiresPhoto(t *testing.T) {
	t.Parallel()

	g, people := newProfileRig(t)
	rec := g.do("POST", "/admin/profile/complete", "editor-token", completeForm(url.Values{"photo": {""}}), nil)
	if rec.Code != 422 {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Add a photo") || !strings.Contains(body, `aria-invalid="true"`) {
		t.Errorf("no field error for the photo:\n%s", body)
	}
	if !strings.Contains(body, `value="Edie Editor"`) {
		t.Error("the typed name was lost on the error page")
	}
	if p, err := people.ForUser(context.Background(), g.editor.ID); err == nil && person.Complete(p) {
		t.Error("an invalid save completed the profile")
	}
	// The page address on the error page follows the typed name.
	if !strings.Contains(body, "Your page will be /author/edie-editor") {
		t.Errorf("the address hint does not follow the name:\n%s", body)
	}
	// Name missing too.
	rec = g.do("POST", "/admin/profile/complete", "editor-token", completeForm(url.Values{"name": {" "}}), nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter your name") {
		t.Errorf("blank name: %d", rec.Code)
	}
}

func TestCompleteProfileRedirects(t *testing.T) {
	t.Parallel()

	g, people := newProfileRig(t)
	rec := g.do("POST", "/admin/profile/complete", "editor-token", completeForm(url.Values{"next": {"/admin/settings/design"}}), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/design" {
		t.Fatalf("with next: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if p, _ := people.ForUser(context.Background(), g.editor.ID); !person.Complete(p) || p.Name != "Edie Editor" {
		t.Errorf("saved person = %+v", p)
	}
	if name := g.mgr.users[g.editor.ID].Name; name == nil || *name != "Edie Editor" {
		t.Errorf("the account name did not follow the profile: %v", name)
	}
	for _, next := range []string{"", "https://evil.example/", "//evil.example", "/admin/profile/complete?next=/x"} {
		rec := g.do("POST", "/admin/profile/complete", "editor-token", completeForm(url.Values{"next": {next}}), nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities" {
			t.Errorf("next %q: %d %s, want the landing page", next, rec.Code, rec.Header().Get("Location"))
		}
	}
	// A complete profile has nothing to ask.
	rec = g.do("GET", "/admin/profile/complete?next=/admin/settings/design", "editor-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/design" {
		t.Errorf("complete profile on the step: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProfilePublicSwitchPersists(t *testing.T) {
	t.Parallel()

	g, people := newProfileRig(t)
	ctx := context.Background()
	for _, public := range []bool{true, false} {
		val := "false"
		if public {
			val = "true"
		}
		// A checked switch posts "true" then the off value; unchecked posts only "false".
		form := completeForm(url.Values{"public": {val}})
		if public {
			form["public"] = []string{"true", "false"}
		}
		if rec := g.do("POST", "/admin/profile", "editor-token", form, nil); rec.Code != 303 {
			t.Fatalf("save: %d", rec.Code)
		}
		if p, _ := people.ForUser(ctx, g.editor.ID); p.Public != public {
			t.Errorf("public = %v, want %v", p.Public, public)
		}
	}
	// The saved profile is shown again, with the switch matching.
	body := g.do("GET", "/admin/profile", "editor-token", nil, nil).Body.String()
	if !strings.Contains(body, `value="Edie Editor"`) || !strings.Contains(body, `/media/edie.jpg`) {
		t.Errorf("profile page does not show the saved person")
	}
}

// A user with no person sees an empty form and a placeholder address, never a
// name or address taken from the email.
func TestCompleteProfilePageNeverUsesTheEmail(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	body := g.do("GET", "/admin/profile/complete", "editor-token", nil, nil).Body.String()
	if strings.Contains(body, "/author/ed") || strings.Contains(inputTag(body, "name"), `value="ed"`) {
		t.Errorf("name or address derived from the email:\n%s", inputTag(body, "name"))
	}
	if !strings.Contains(body, "/author/your-name") {
		t.Error("no placeholder address")
	}
}
