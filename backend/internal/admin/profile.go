package admin

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/user"
)

type profileData struct {
	Name, Email, Role string
	Photo, JobTitle   string
	ShortBio          string
	LinkedIn          string
	Public            bool
	// PublicURL is the path of the person's public page; Slug is its last part.
	// SlugFixed: the person has been public, so the path no longer follows the name.
	PublicURL, Slug string
	SlugFixed       bool
	// Next is where "Save and continue" goes (the complete-profile page).
	Next string
	// PhotoRequired marks the photo with * on "Complete your profile" only.
	PhotoRequired bool
	// Initials fill the avatar until a photo exists; RoleLabel and RoleKind draw the role badge.
	Initials, RoleLabel, RoleKind string

	NameErr, PhotoErr, JobTitleErr string
	ShortBioErr, LinkedInErr       string
	CurrentErr                     string
	NewErr, MatchErr               string
	Summary                        []ui.FieldError
}

// profileFields maps the field ids of a person.ProfileInput error to labels.
var profileFields = []struct{ ID, Label string }{
	{"name", "Name"}, {"photo", "Photo"}, {"job_title", "Job title"}, {"short_bio", "Short bio"}, {"linkedin", "LinkedIn"},
}

func (a *Admin) mountProfile(authed *httpx.Group) {
	authed.HandleFunc("GET /profile", a.handleProfile)
	authed.HandleFunc("POST /profile", a.handleProfileSave)
	authed.HandleFunc("POST /profile/password", a.handleProfilePassword)
	authed.HandleFunc("GET /profile/complete", a.handleProfileComplete)
	authed.HandleFunc("POST /profile/complete", a.handleProfileCompleteSave)
}

// fillPerson loads the signed-in user's person into d. A user who has none yet
// gets an empty form: the person is made on the first save, from the name typed
// there.
func (a *Admin) fillPerson(r *http.Request, d *profileData) error {
	u := userFrom(r.Context())
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return err
	}
	p, err := a.deps.People.ForUser(r.Context(), id)
	if errors.Is(err, person.ErrNotFound) {
		if u.Name != "" && u.Name != u.Email {
			d.Name = u.Name
		}
		a.setPublicURL(r.Context(), d, nil)
		return nil
	}
	if err != nil {
		return err
	}
	d.Name = p.Name
	d.Photo, d.JobTitle, d.ShortBio, d.LinkedIn = p.Photo, p.JobTitle, p.ShortBio, p.Links["linkedin"]
	d.Public = p.Public
	a.setPublicURL(r.Context(), d, p)
	return nil
}

// setPublicURL fills the page address shown beside the public switch. A person
// who has been public keeps the saved slug; otherwise the address follows the
// name (d.Name), as it will on save.
func (a *Admin) setPublicURL(ctx context.Context, d *profileData, p *person.Person) {
	d.Slug = "your-name"
	if p != nil && p.SlugFixed {
		d.Slug, d.SlugFixed = p.Slug, true
	} else if p != nil && p.Name == d.Name {
		d.Slug = p.Slug
	} else if s := author.Slugify(d.Name); s != "" {
		d.Slug = s
	}
	d.PublicURL = a.personPath(ctx, d.Slug)
}

func (a *Admin) personPath(ctx context.Context, slug string) string {
	if a.deps.PersonPath != nil {
		if path := a.deps.PersonPath(ctx, slug); path != "" {
			return path
		}
	}
	return "/author/" + slug
}

func (a *Admin) renderProfile(w http.ResponseWriter, r *http.Request, status int, page string, d profileData) {
	u := userFrom(r.Context())
	d.Email, d.Role = u.Email, u.Role
	d.Initials = initialsOf(cmp.Or(d.Name, u.Name, u.Email))
	d.RoleKind = roleBadgeKind(u.Role)
	d.RoleLabel = strings.ToUpper(u.Role[:1]) + u.Role[1:]
	v := a.NewView(w, r, "Your profile")
	if page == "profile/complete" {
		v.Title = "Complete your profile"
		v.Wide = true // the gated step has no nav: it renders on the bare layout
		d.PhotoRequired = true
	}
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, page, v)
}

func (a *Admin) peopleReady(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.People == nil || a.deps.Manager == nil {
		a.Error(w, r, http.StatusServiceUnavailable, "Unavailable", "The server is not ready.")
		return false
	}
	return true
}

func (a *Admin) handleProfile(w http.ResponseWriter, r *http.Request) {
	var d profileData
	if a.deps.People != nil {
		if err := a.fillPerson(r, &d); err != nil {
			a.logger().Error("load profile failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Your profile could not be loaded.")
			return
		}
	}
	a.renderProfile(w, r, http.StatusOK, "profile/index", d)
}

// handleProfileComplete is the one-time step after sign-in. A complete profile
// has nothing to ask, so it goes on to where the user was headed.
func (a *Admin) handleProfileComplete(w http.ResponseWriter, r *http.Request) {
	if !a.peopleReady(w, r) {
		return
	}
	next := SafeNextFor(a.adminPath, r.URL.Query().Get("next"))
	var d profileData
	if err := a.fillPerson(r, &d); err != nil {
		a.logger().Error("load profile failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Your profile could not be loaded.")
		return
	}
	if d.Name != "" && d.Photo != "" {
		http.Redirect(w, r, a.afterProfile(next), http.StatusSeeOther)
		return
	}
	d.Next = next
	a.renderProfile(w, r, http.StatusOK, "profile/complete", d)
}

func (a *Admin) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	if !a.peopleReady(w, r) {
		return
	}
	if !a.saveProfile(w, r, "profile/index") {
		return
	}
	a.SetFlash(w, r, "success", "Profile saved.")
	http.Redirect(w, r, a.adminPath+"/profile", http.StatusSeeOther)
}

func (a *Admin) handleProfileCompleteSave(w http.ResponseWriter, r *http.Request) {
	if !a.peopleReady(w, r) {
		return
	}
	if !a.saveProfile(w, r, "profile/complete") {
		return
	}
	http.Redirect(w, r, a.afterProfile(SafeNextFor(a.adminPath, r.PostFormValue("next"))), http.StatusSeeOther)
}

// afterProfile is where the complete-profile step ends: the page the user was
// headed for, never the step itself.
func (a *Admin) afterProfile(next string) string {
	if next == "" || strings.HasPrefix(next, a.url("profile", "complete")) {
		return a.landing()
	}
	return next
}

// saveProfile saves the profile form. On invalid input it renders page again
// with 422 and the field errors; on a server error it renders the error page.
// It reports whether the save went through.
func (a *Admin) saveProfile(w http.ResponseWriter, r *http.Request, page string) bool {
	u := userFrom(r.Context())
	id, _ := uuid.Parse(u.ID)
	in := person.ProfileInput{
		Name: r.PostFormValue("name"), Photo: r.PostFormValue("photo"), JobTitle: r.PostFormValue("job_title"),
		ShortBio: r.PostFormValue("short_bio"), LinkedIn: r.PostFormValue("linkedin"),
		Public: r.PostFormValue("public") == "true",
	}
	saved, err := a.deps.People.Save(r.Context(), id, in)
	var verr *person.ValidationError
	if errors.As(err, &verr) {
		d := profileData{
			Name: in.Name, Photo: in.Photo, JobTitle: in.JobTitle, ShortBio: in.ShortBio, LinkedIn: in.LinkedIn,
			Public: in.Public, Next: SafeNextFor(a.adminPath, r.PostFormValue("next")),
		}
		a.setPublicURL(r.Context(), &d, a.currentPerson(r, id))
		msgs := map[string]*string{"name": &d.NameErr, "photo": &d.PhotoErr, "job_title": &d.JobTitleErr, "short_bio": &d.ShortBioErr, "linkedin": &d.LinkedInErr}
		for _, f := range profileFields {
			if msg := verr.Fields[f.ID]; msg != "" {
				*msgs[f.ID] = msg
				d.Summary = append(d.Summary, ui.FieldError{ID: f.ID, Label: f.Label, Message: msg})
			}
		}
		a.renderProfile(w, r, http.StatusUnprocessableEntity, page, d)
		return false
	}
	if err != nil {
		a.logger().Error("save profile failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Your profile could not be saved.")
		return false
	}
	// The account name follows the profile name. Never the role: people
	// cannot promote themselves here.
	if saved.Name != u.Name {
		name := saved.Name
		if _, err := a.deps.Manager.Update(r.Context(), id, &user.UpdateUserInput{Name: &name}); err != nil {
			a.logger().Error("update profile failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Your profile could not be saved.")
			return false
		}
	}
	return true
}

// currentPerson is the user's saved person, or nil, for the form shown again
// after an error.
func (a *Admin) currentPerson(r *http.Request, userID uuid.UUID) *person.Person {
	p, err := a.deps.People.ForUser(r.Context(), userID)
	if err != nil {
		return nil
	}
	return p
}

func (a *Admin) handleProfilePassword(w http.ResponseWriter, r *http.Request) {
	cur, pw, confirm := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("confirm")
	d := profileData{}
	add := func(id, label, msg string, field *string) {
		*field = msg
		d.Summary = append(d.Summary, ui.FieldError{ID: id, Label: label, Message: msg})
	}
	if cur == "" {
		add("current", "Current password", "Enter your current password", &d.CurrentErr)
	}
	if len(pw) < auth.MinPasswordLen {
		add("password", "New password", sentence(auth.ErrPasswordShort.Error()), &d.NewErr)
	}
	if pw != confirm {
		add("confirm", "Repeat the new password", "The passwords do not match", &d.MatchErr)
	}
	if len(d.Summary) == 0 && a.deps.Passwords != nil {
		id, _ := uuid.Parse(userFrom(r.Context()).ID)
		var keep *uuid.UUID
		if s := sessionFrom(r.Context()); s != nil {
			keep = &s.ID
		}
		err := a.deps.Passwords.ChangePassword(r.Context(), id, cur, pw, keep)
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			add("current", "Current password", "That is not your current password", &d.CurrentErr)
		case errors.Is(err, auth.ErrAccountLocked):
			add("current", "Current password", "Too many wrong attempts. Try again in a few minutes", &d.CurrentErr)
		case err != nil:
			a.logger().Error("change password failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Your password could not be changed.")
			return
		default:
			a.SetFlash(w, r, "success", "Password changed. Your other browser sessions were signed out.")
			http.Redirect(w, r, a.adminPath+"/profile", http.StatusSeeOther)
			return
		}
	}
	if a.deps.People != nil {
		_ = a.fillPerson(r, &d)
	}
	a.renderProfile(w, r, http.StatusUnprocessableEntity, "profile/index", d)
}
