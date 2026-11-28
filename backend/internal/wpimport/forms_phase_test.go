package wpimport

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/form"
)

// memFormRepo is a form repository that keeps forms in memory; it implements
// only what creating a form needs.
type memFormRepo struct {
	form.Repository
	byID map[uuid.UUID]*form.Form
}

func (m *memFormRepo) CreateForm(_ context.Context, in *form.CreateFormInput) (*form.Form, error) {
	for _, f := range m.byID {
		if f.Slug == in.Slug {
			return nil, form.ErrSlugAlreadyExists
		}
	}
	f := &form.Form{ID: uuid.New(), Name: in.Name, Slug: in.Slug, Fields: in.Fields, Status: in.Status}
	m.byID[f.ID] = f
	return f, nil
}

func (m *memFormRepo) GetFormByID(_ context.Context, id uuid.UUID) (*form.Form, error) {
	if f, ok := m.byID[id]; ok {
		return f, nil
	}
	return nil, form.ErrFormNotFound
}

func formTestRunner() (*Runner, *memFormRepo) {
	repo := &memFormRepo{byID: map[uuid.UUID]*form.Form{}}
	svc := form.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &Runner{d: &Deps{Forms: svc}, res: &Result{}, dump: &Dump{}}, repo
}

func TestFormsImportWithoutServiceWarns(t *testing.T) {
	r := &Runner{d: &Deps{}, res: &Result{}, dump: &Dump{CF7: []CF7Form{{ID: "7", Title: "Contact"}, {ID: "8"}}}}
	if err := r.importForms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.res.Forms == nil || r.res.Forms.Found != 2 || r.res.Forms.Created != 0 {
		t.Errorf("forms result = %+v", r.res.Forms)
	}
	if len(r.res.Warnings) != 1 || !strings.Contains(r.res.Warnings[0], "no form service") {
		t.Errorf("warnings = %v", r.res.Warnings)
	}
	if r.formSlugs != nil {
		t.Error("no slugs without forms")
	}
}

func TestFormsImportNothingToDo(t *testing.T) {
	r := &Runner{d: &Deps{}, res: &Result{}, dump: &Dump{}}
	if err := r.importForms(context.Background()); err != nil || r.res.Forms != nil || len(r.res.Warnings) != 0 {
		t.Errorf("err %v, forms %v, warnings %v", err, r.res.Forms, r.res.Warnings)
	}
}

func TestFormsCreateRetriesTakenSlug(t *testing.T) {
	r, repo := formTestRunner()
	f := CF7Form{ID: "7", Hash: "abc1234", Slug: "contact", Title: "Contact", Template: `<p>[text* your-name] [submit "Send"]</p>`}
	first, err := r.createForm(context.Background(), f, MapCF7(f.Template))
	if err != nil || first.Slug != "contact" || first.Status != form.StatusActive {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := r.createForm(context.Background(), f, MapCF7(f.Template))
	if err != nil || second.Slug != "contact-2" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if len(repo.byID) != 2 || len(second.Fields) != 1 {
		t.Errorf("forms %d, fields %d", len(repo.byID), len(second.Fields))
	}
	slugs := map[string]string{}
	addFormSlug(slugs, f, second.Slug)
	if slugs["7"] != "contact-2" || slugs["abc1234"] != "contact-2" || len(slugs) != 2 {
		t.Errorf("slugs = %v", slugs)
	}
}

func TestFormsCreateGivesUpAfterManyTries(t *testing.T) {
	r, repo := formTestRunner()
	for i := 1; i <= maxFormSlugTries; i++ {
		slug := "contact"
		if i > 1 {
			slug = fmt.Sprintf("contact-%d", i)
		}
		id := uuid.New()
		repo.byID[id] = &form.Form{ID: id, Slug: slug}
	}
	f := CF7Form{ID: "7", Slug: "contact", Title: "Contact", Template: `[text name]`}
	if _, err := r.createForm(context.Background(), f, MapCF7(f.Template)); err == nil {
		t.Error("expected an error once every slug is taken")
	}
}
