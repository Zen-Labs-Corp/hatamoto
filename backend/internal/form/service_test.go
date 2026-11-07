package form

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubRepo implements only what Service.CreateSubmission touches.
type stubRepo struct {
	Repository
	created []*CreateSubmissionInput
	// writes counts form writes that reached the repository.
	writes   int
	saveErr  error
	saveBase time.Time
}

func (r *stubRepo) CreateForm(_ context.Context, in *CreateFormInput) (*Form, error) {
	r.writes++
	return &Form{ID: uuid.New(), Name: in.Name, Fields: in.Fields}, nil
}

func (r *stubRepo) UpdateForm(_ context.Context, id uuid.UUID, in *UpdateFormInput) (*Form, error) {
	r.writes++
	return &Form{ID: id, Fields: in.Fields}, nil
}

func (r *stubRepo) SaveDefinition(_ context.Context, id uuid.UUID, base time.Time, in *SaveDefinitionInput) (*Form, error) {
	r.writes++
	r.saveBase = base
	if r.saveErr != nil {
		return nil, r.saveErr
	}
	return &Form{ID: id, Name: in.Name, Fields: in.Fields, UpdatedAt: base.Add(time.Microsecond)}, nil
}

func (r *stubRepo) GetFormByID(_ context.Context, id uuid.UUID) (*Form, error) {
	return &Form{ID: id, Status: StatusActive}, nil
}

func (r *stubRepo) CreateSubmission(_ context.Context, id uuid.UUID, in *CreateSubmissionInput) (*FormSubmission, error) {
	r.created = append(r.created, in)
	return &FormSubmission{ID: uuid.New(), FormID: id, Status: initialStatus(in)}, nil
}

func newStubService() (*Service, *stubRepo) {
	r := &stubRepo{}
	return NewService(r, slog.New(slog.NewTextHandler(io.Discard, nil))), r
}

func TestCreateSubmissionStatus(t *testing.T) {
	svc, repo := newStubService()
	ctx := context.Background()

	got, err := svc.CreateSubmission(ctx, uuid.New(), &CreateSubmissionInput{Status: SubmissionStatusSpam})
	if err != nil || got.Status != SubmissionStatusSpam {
		t.Fatalf("spam: %+v, %v", got, err)
	}
	got, err = svc.CreateSubmission(ctx, uuid.New(), &CreateSubmissionInput{})
	if err != nil || got.Status != SubmissionStatusNew {
		t.Fatalf("default: %+v, %v", got, err)
	}
	before := len(repo.created)
	if _, err := svc.CreateSubmission(ctx, uuid.New(), &CreateSubmissionInput{Status: "bogus"}); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("invalid status err = %v", err)
	}
	if len(repo.created) != before {
		t.Fatal("invalid status reached the repository")
	}
}

func TestInitialStatus(t *testing.T) {
	if initialStatus(&CreateSubmissionInput{}) != SubmissionStatusNew {
		t.Fatal("empty should default to new")
	}
	if initialStatus(&CreateSubmissionInput{Status: SubmissionStatusSpam}) != SubmissionStatusSpam {
		t.Fatal("spam not kept")
	}
}

func badLogicFields() [][]FormField {
	return [][]FormField{
		{{ID: "a", Type: FieldTypeText, Name: "a", Logic: &FieldLogic{FieldID: "a", Op: LogicFilled}}},
		{{ID: "a", Type: FieldTypeText, Name: "a", Logic: &FieldLogic{FieldID: "nope", Op: LogicFilled}}},
		{
			{ID: "a", Type: FieldTypeText, Name: "a", Logic: &FieldLogic{FieldID: "b", Op: LogicFilled}},
			{ID: "b", Type: FieldTypeText, Name: "b", Logic: &FieldLogic{FieldID: "a", Op: LogicFilled}},
		},
	}
}

func TestCreateAndUpdateRejectInvalidLogic(t *testing.T) {
	ctx := context.Background()
	for i, fields := range badLogicFields() {
		svc, repo := newStubService()
		if _, err := svc.CreateForm(ctx, &CreateFormInput{Name: "F", Fields: fields}); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("create %d: err = %v, want ErrInvalidDefinition", i, err)
		}
		if _, err := svc.UpdateForm(ctx, uuid.New(), &UpdateFormInput{Fields: fields}); !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("update %d: err = %v, want ErrInvalidDefinition", i, err)
		}
		if repo.writes != 0 {
			t.Errorf("case %d: invalid logic reached the repository", i)
		}
	}
}

func TestCreateAndUpdateKeepLooseLegacyFields(t *testing.T) {
	svc, repo := newStubService()
	ctx := context.Background()
	loose := []FormField{{Type: FieldTypeText, Label: "Your Name", Name: "Bad Name"}}
	if _, err := svc.CreateForm(ctx, &CreateFormInput{Name: "F", Fields: loose}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.UpdateForm(ctx, uuid.New(), &UpdateFormInput{Fields: []FormField{{Type: FieldTypeText, Label: "x", Name: "Bad Name"}}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if repo.writes != 2 {
		t.Fatalf("writes = %d, want 2", repo.writes)
	}
}

func TestSaveDefinition(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 7, 10, 0, 0, 123456000, time.UTC)
	valid := &SaveDefinitionInput{Name: "F", Status: StatusActive, Fields: []FormField{{ID: "a", Type: FieldTypeText, Name: "a", Label: "A"}}}

	svc, repo := newStubService()
	f, err := svc.SaveDefinition(ctx, uuid.New(), base, valid)
	if err != nil || !repo.saveBase.Equal(base) {
		t.Fatalf("valid: %v base=%v", err, repo.saveBase)
	}
	if got, err := ParseVersion(f.Version()); err != nil || !got.Equal(base.Add(time.Microsecond)) {
		t.Fatalf("version round trip: %v %v", got, err)
	}

	for i, fields := range badLogicFields() {
		svc, repo := newStubService()
		_, err := svc.SaveDefinition(ctx, uuid.New(), base, &SaveDefinitionInput{Name: "F", Status: StatusActive, Fields: fields})
		var errs DefinitionErrors
		if !errors.As(err, &errs) || !errors.Is(err, ErrInvalidDefinition) || errs[0].FieldID == "" {
			t.Errorf("case %d: err = %v", i, err)
		}
		if repo.writes != 0 {
			t.Errorf("case %d reached the repository", i)
		}
	}

	svc, _ = newStubService()
	if _, err := svc.SaveDefinition(ctx, uuid.New(), base, &SaveDefinitionInput{Name: "F", Status: "bogus"}); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("status err = %v", err)
	}
	svc, repo = newStubService()
	repo.saveErr = ErrVersionConflict
	if _, err := svc.SaveDefinition(ctx, uuid.New(), base, valid); !errors.Is(err, ErrVersionConflict) {
		t.Errorf("conflict err = %v", err)
	}
}

func TestServiceRefusesUnsafeSuccessRedirect(t *testing.T) {
	ctx := context.Background()
	bad := "javascript:alert(1)"
	field := []FormField{{ID: "a", Type: FieldTypeText, Name: "a", Label: "A"}}
	svc, repo := newStubService()
	_, errCreate := svc.CreateForm(ctx, &CreateFormInput{Name: "F", Fields: field, Settings: FormSettings{SuccessRedirectURL: bad}})
	_, errUpdate := svc.UpdateForm(ctx, uuid.New(), &UpdateFormInput{Settings: &FormSettings{SuccessRedirectURL: bad}})
	_, errSave := svc.SaveDefinition(ctx, uuid.New(), time.Now(), &SaveDefinitionInput{Name: "F", Status: StatusActive, Fields: field, Settings: FormSettings{SuccessRedirectURL: bad}})
	for name, err := range map[string]error{"create": errCreate, "update": errUpdate, "save": errSave} {
		if !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("%s: err = %v, want ErrInvalidDefinition", name, err)
		}
	}
	if repo.writes != 0 {
		t.Errorf("%d writes reached the repository", repo.writes)
	}
	if _, err := svc.UpdateForm(ctx, uuid.New(), &UpdateFormInput{Settings: &FormSettings{SuccessRedirectURL: "/thanks"}}); err != nil {
		t.Errorf("a relative path was refused: %v", err)
	}
}
