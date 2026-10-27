package form

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// stubRepo implements only what Service.CreateSubmission touches.
type stubRepo struct {
	Repository
	created []*CreateSubmissionInput
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
