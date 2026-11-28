package wpimport

import (
	"context"
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/form"
)

// maxFormSlugTries is how many numbered slugs (slug-2, slug-3, ...) an
// imported form tries before it fails.
const maxFormSlugTries = 20

// contentStep imports the Contact Form 7 forms, so the bodies can link to
// them, and then the items and their comments.
func (r *Runner) contentStep(ctx context.Context) error {
	if err := r.importForms(ctx); err != nil {
		return err
	}
	return r.importContentAndComments(ctx)
}

// importForms creates a Hatamoto form for each Contact Form 7 form of the
// source. A form imported by an earlier run is kept as it is, never updated;
// one that was deleted since is created again. A form that cannot be created
// is reported and the step goes on; an unreadable id map stops it.
func (r *Runner) importForms(ctx context.Context) error {
	if len(r.dump.CF7) == 0 {
		return nil
	}
	res := &FormsResult{Found: len(r.dump.CF7)}
	r.res.Forms = res
	if r.d.Forms == nil {
		r.res.Warnings = append(r.res.Warnings, "Contact Form 7 forms not imported: no form service")
		return nil
	}
	done, err := r.st.All(ctx, KindForm)
	if err != nil {
		return fmt.Errorf("read the imported forms: %w", err)
	}
	slugs := map[string]string{}
	for _, f := range r.dump.CF7 {
		if id, ok := done[f.ID]; ok {
			fm, err := r.d.Forms.GetForm(ctx, id)
			if err == nil {
				addFormSlug(slugs, f, fm.Slug)
				res.AlreadyImported++
				res.Forms = append(res.Forms, FormResult{Title: f.Title, Slug: fm.Slug, Status: "already", Fields: len(fm.Fields)})
				continue
			}
			if !errors.Is(err, form.ErrFormNotFound) {
				r.formFailed(f, err)
				continue
			}
			// The owner deleted it: create it again.
		}
		m := MapCF7(f.Template)
		fm, err := r.createForm(ctx, f, m)
		if err != nil {
			r.formFailed(f, err)
			continue
		}
		if err := r.st.Put(ctx, r.run.ID, KindForm, f.ID, fm.ID, f.Hash); err != nil {
			r.res.Warnings = append(r.res.Warnings, "id map: "+err.Error())
		}
		addFormSlug(slugs, f, fm.Slug)
		res.Created++
		line := FormResult{Title: f.Title, Slug: fm.Slug, Status: "imported", Fields: len(fm.Fields)}
		for _, d := range m.Dropped {
			line.Dropped = append(line.Dropped, d+" dropped")
		}
		if m.Conditional {
			line.Dropped = append(line.Dropped, "conditional rules not carried")
		}
		if len(line.Dropped) > 0 {
			line.Status = "partly"
		}
		res.Forms = append(res.Forms, line)
		if f.HasMail {
			res.MailNotImported = true
		}
	}
	if len(slugs) > 0 {
		r.formSlugs = slugs
	}
	return nil
}

// addFormSlug records the slug of an imported form under its CF7 id and hash.
func addFormSlug(slugs map[string]string, f CF7Form, slug string) {
	for _, k := range []string{f.ID, f.Hash} {
		if k != "" {
			slugs[k] = slug
		}
	}
}

// formFailed reports a form that could not be imported.
func (r *Runner) formFailed(f CF7Form, err error) {
	r.res.Errors = append(r.res.Errors, ItemError{Kind: "form", ID: f.ID, Title: f.Title, Err: err.Error()})
	r.res.Forms.Forms = append(r.res.Forms.Forms, FormResult{Title: f.Title, Status: "skipped", Note: "not imported: " + err.Error()})
}

// createForm creates the form; a slug that is taken becomes slug-2, slug-3, ...
// The form is active so [form slug=...] renders. Mail settings are not imported.
func (r *Runner) createForm(ctx context.Context, f CF7Form, m CF7Mapped) (*form.Form, error) {
	base := f.Slug
	if base == "" {
		base = "form-" + f.ID
	}
	var err error
	for try := 1; try <= maxFormSlugTries; try++ {
		slug := base
		if try > 1 {
			slug = fmt.Sprintf("%s-%d", base, try)
		}
		var fm *form.Form
		fm, err = r.d.Forms.CreateForm(ctx, &form.CreateFormInput{
			Name: firstNonEmpty(f.Title, base), Slug: slug, Fields: m.Fields,
			Settings: form.FormSettings{SubmitButtonText: m.Submit}, Status: form.StatusActive,
		})
		if !errors.Is(err, form.ErrSlugAlreadyExists) {
			return fm, err
		}
	}
	return nil, err
}
