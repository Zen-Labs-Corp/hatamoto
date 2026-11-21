package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/user"
)

// No t.Parallel: the authorizer is process-wide.

// Submissions hold personal data. The roles without form.submission.read must
// get 403 on every read of them, with a real forms store behind the routes (a
// missing store answers 503 and would prove nothing); owner and editor keep access.
func TestFormSubmissionReadsNeedTheReadAction(t *testing.T) {
	store, err := form.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ff := newFakeForms()
	roles := []user.Role{user.RoleReadOnly, user.RoleAuthor, user.RoleContributor, user.RoleReviewer}
	ids := map[user.Role]uuid.UUID{}
	g := newRig(t, func(d *Deps) {
		d.Growth.Forms = filesForms{ff, store}
		fu := d.Users.(fakeUsers)
		for _, r := range roles {
			u := &user.User{ID: uuid.New(), Email: string(r) + "@example.com", Role: r}
			fu[u.ID] = u
			ids[r] = u.ID
		}
	})
	fm := ff.addForm("Apply", form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name"},
		form.FormField{ID: "f", Type: form.FieldTypeFile, Label: "CV", Name: "cv", Validation: &form.FieldValidation{Accept: []string{"pdf"}}})
	staged, err := store.Stage(strings.NewReader(pdfBody), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := store.Commit(fm.ID, staged)
	if err != nil {
		t.Fatal(err)
	}
	sub := ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Ada",
		"cv": map[string]any{"id": fid, "name": "cv.pdf", "type": "application/pdf", "size": float64(len(pdfBody))}})

	sid := sub.ID.String()
	paths := []string{
		"/admin/forms/submissions?form=" + fm.ID.String(),
		"/admin/forms/submissions/export.csv?form=" + fm.ID.String(),
		"/admin/forms/submissions/" + sid,
		"/admin/forms/submissions/" + sid + "/files/cv",
		"/admin/forms/submissions/" + sid + "/delete",
		"/admin/forms/design/" + fm.ID.String() + "/submissions",
	}
	for _, p := range paths {
		for _, role := range roles {
			rec := g.do("GET", p, g.freshToken(ids[role]), nil, nil)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s GET %s = %d, want 403", role, p, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "Ada") || strings.Contains(rec.Body.String(), "%PDF") {
				t.Errorf("%s GET %s leaked submission data", role, p)
			}
		}
		for _, token := range []string{"owner-token", "editor-token"} {
			// The list and the single page redirect to the form's own tab.
			rec := g.do("GET", p, token, nil, nil)
			if loc := rec.Header().Get("Location"); rec.Code != http.StatusOK && (rec.Code < 300 || rec.Code > 399 || strings.Contains(loc, "/login")) {
				t.Errorf("%s GET %s = %d %s, want it served", token, p, rec.Code, loc)
			}
		}
	}
}

// The screens that only summarise submissions and comments must not carry their
// personal data to the roles without the read action: the page stays reachable,
// the section or sample is left out. Owner and editor still see it all.
func TestPagesWithheldFromRolesWithoutReadAction(t *testing.T) {
	ff := newFakeForms()
	roles := []user.Role{user.RoleReadOnly, user.RoleAuthor, user.RoleContributor, user.RoleReviewer}
	ids := map[user.Role]uuid.UUID{}
	g := newRig(t, func(d *Deps) {
		d.Growth.Forms = ff
		d.Comments = &fakeComments{settings: CommentSettings{Enabled: true}, counts: CommentCounts{Pending: 1},
			rows: []CommentRow{{ID: "c1", AuthorName: "Grace Hopper", EntityTitle: "Alpha service", BodyRaw: "Pending words nobody may read", CreatedAt: time.Now()}}}
		fu := d.Users.(fakeUsers)
		for _, r := range roles {
			u := &user.User{ID: uuid.New(), Email: string(r) + "@example.com", Role: r}
			fu[u.ID] = u
			ids[r] = u.ID
		}
	})
	fm := ff.addForm("Apply", form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name"},
		form.FormField{ID: "e", Type: form.FieldTypeEmail, Label: "Email", Name: "email"},
		form.FormField{ID: "m", Type: form.FieldTypeTextarea, Label: "Message", Name: "message"})
	ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Ada Lovelace", "email": "ada@example.com", "message": "Quartz answer text"})

	secrets := []string{"Ada Lovelace", "ada@example.com", "Quartz answer text", "Grace Hopper", "Pending words nobody may read"}
	paths := map[string][]string{
		"/admin":       {"ada@example.com", "Grace Hopper", "Pending words nobody may read"},
		"/admin/forms": {"Ada Lovelace", "ada@example.com", "Quartz answer text"},
		"/admin/forms/design/" + fm.ID.String() + "/settings": {"Ada Lovelace", "ada@example.com", "Quartz answer text"},
	}
	for p, visible := range paths {
		for _, role := range roles {
			rec := g.do("GET", p, g.freshToken(ids[role]), nil, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("%s GET %s = %d, want 200", role, p, rec.Code)
			}
			for _, secret := range secrets {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("%s GET %s leaked %q", role, p, secret)
				}
			}
		}
		for _, token := range []string{"owner-token", "editor-token"} {
			rec := g.do("GET", p, token, nil, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("%s GET %s = %d, want 200", token, p, rec.Code)
			}
			for _, want := range visible {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("%s GET %s lacks %q", token, p, want)
				}
			}
		}
	}
}
