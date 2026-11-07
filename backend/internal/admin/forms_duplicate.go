package admin

import (
	"errors"
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/form"
)

const (
	formNameMaxRunes = 120
	formSlugTries    = 20
)

// handleFormDuplicate copies a form's fields and settings into a new paused
// form named "Copy of ...", and opens the copy's Settings tab. Submissions are not copied.
func (a *Admin) handleFormDuplicate(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.loadForm(w, r)
	if !ok {
		return
	}
	name := []rune("Copy of " + cur.Name)
	if len(name) > formNameMaxRunes {
		name = name[:formNameMaxRunes]
	}
	base := slugifyName(string(name))
	if base == "" {
		base = "copy"
	}
	if len(base) > 90 {
		base = base[:90]
	}
	var copied *form.Form
	for i := 1; i <= formSlugTries && copied == nil; i++ {
		slug := base
		if i > 1 {
			slug = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := a.deps.Growth.Forms.CreateForm(r.Context(), &form.CreateFormInput{
			Name: string(name), Slug: slug, Description: cur.Description, Fields: append([]form.FormField{}, cur.Fields...), Settings: cur.Settings,
		})
		switch {
		case err == nil:
			copied = f
		case errors.Is(err, form.ErrSlugAlreadyExists):
		default:
			a.logger().Error("duplicate form failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be duplicated.")
			return
		}
	}
	if copied == nil {
		a.Error(w, r, http.StatusConflict, "Could not duplicate", "No free identifier was found for the copy. Rename a form and try again.")
		return
	}
	paused := form.StatusInactive
	if _, err := a.deps.Growth.Forms.UpdateForm(r.Context(), copied.ID, &form.UpdateFormInput{Status: &paused}); err != nil {
		a.logger().Error("pause duplicated form failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The copy was made but could not be paused.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Form duplicated. The copy is paused.")
	Redirect(w, r, a.adminPath+"/forms/design/"+copied.ID.String()+"/settings")
}
