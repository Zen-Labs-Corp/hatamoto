package form

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fileRepo is a one-form repository holding submissions in memory.
type fileRepo struct {
	Repository
	form        *Form
	subs        map[uuid.UUID]*FormSubmission
	deleted     []uuid.UUID
	deleteErr   error
	formDeleted bool
	purged      []PurgedFile
	purgeErr    error
}

func (r *fileRepo) GetFormByID(context.Context, uuid.UUID) (*Form, error) { return r.form, nil }

func (r *fileRepo) GetSubmissionByID(_ context.Context, id uuid.UUID) (*FormSubmission, error) {
	if s, ok := r.subs[id]; ok {
		return s, nil
	}
	return nil, ErrSubmissionNotFound
}

func (r *fileRepo) DeleteSubmission(_ context.Context, id uuid.UUID) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *fileRepo) DeleteForm(context.Context, uuid.UUID) error {
	r.formDeleted = true
	return nil
}

func (r *fileRepo) PurgeExpiredSubmissions(context.Context) (int64, []PurgedFile, error) {
	return int64(len(r.purged)), r.purged, r.purgeErr
}

type fileFixture struct {
	svc   *Service
	repo  *fileRepo
	store *FileStore
	root  string
	form  *Form
}

func newFileFixture(t *testing.T) *fileFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "uploads")
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &Form{ID: uuid.New(), Fields: []FormField{
		{ID: "a", Name: "cv", Type: FieldTypeFile},
		{ID: "b", Name: "photo", Type: FieldTypeFile},
		{ID: "c", Name: "note", Type: FieldTypeText},
	}}
	repo := &fileRepo{form: f, subs: map[uuid.UUID]*FormSubmission{}}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetFileStore(store)
	return &fileFixture{svc: svc, repo: repo, store: store, root: root, form: f}
}

// put stores a file and returns its ref as a jsonb round trip would hand it back.
func (fx *fileFixture) put(t *testing.T) map[string]any {
	t.Helper()
	st, err := fx.store.Stage(strings.NewReader("data"), 100)
	if err != nil {
		t.Fatal(err)
	}
	id, err := fx.store.Commit(fx.form.ID, st)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": id, "name": "a.pdf", "size": float64(4), "type": "application/pdf"}
}

func (fx *fileFixture) exists(ref map[string]any) bool {
	f, err := fx.store.Open(fx.form.ID, ref["id"].(string))
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func (fx *fileFixture) addSub(data map[string]any) uuid.UUID {
	id := uuid.New()
	fx.repo.subs[id] = &FormSubmission{ID: id, FormID: fx.form.ID, Data: data}
	return id
}

func TestDeleteSubmissionRemovesFiles(t *testing.T) {
	t.Parallel()
	for _, status := range []string{SubmissionStatusNew, SubmissionStatusSpam} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			fx := newFileFixture(t)
			cv, photo, other := fx.put(t), fx.put(t), fx.put(t)
			id := fx.addSub(map[string]any{"cv": cv, "photo": photo, "note": "hi"})
			fx.repo.subs[id].Status = status
			fx.addSub(map[string]any{"cv": other})
			if err := fx.svc.DeleteSubmission(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if fx.exists(cv) || fx.exists(photo) {
				t.Error("files of the deleted submission remain")
			}
			if !fx.exists(other) {
				t.Error("another submission's file was removed")
			}
		})
	}
}

func TestDeleteSubmissionToleratesMissingFile(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	cv := fx.put(t)
	if err := fx.store.Delete(fx.form.ID, cv["id"].(string)); err != nil {
		t.Fatal(err)
	}
	id := fx.addSub(map[string]any{"cv": cv})
	if err := fx.svc.DeleteSubmission(context.Background(), id); err != nil {
		t.Fatalf("missing file must not be an error: %v", err)
	}
	if len(fx.repo.deleted) != 1 {
		t.Fatal("row not deleted")
	}
}

func TestDeleteSubmissionIgnoresForgedIDsAndMalformedRefs(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	victim := fx.put(t)
	outside := filepath.Join(filepath.Dir(fx.root), "secret")
	if err := os.WriteFile(outside, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Path-shaped ids, and a valid id without a name or type, delete nothing.
	id := fx.addSub(map[string]any{
		"cv":    map[string]any{"id": "../../secret", "name": "x", "type": "t"},
		"photo": map[string]any{"id": "../" + victim["id"].(string), "name": "x", "type": "t"},
		"note":  map[string]any{"id": victim["id"]},
	})
	if err := fx.svc.DeleteSubmission(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if !fx.exists(victim) {
		t.Error("a malformed ref deleted a file")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("file outside the store removed: %v", err)
	}
}

// Field names are not stable: a file stored under a key that is no longer a
// file field (renamed, retyped or removed) is still deleted with its row.
func TestDeleteSubmissionRemovesFilesOfFieldsThatChanged(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	renamed, retyped := fx.put(t), fx.put(t)
	id := fx.addSub(map[string]any{"old_cv": renamed, "note": retyped})
	if err := fx.svc.DeleteSubmission(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if fx.exists(renamed) || fx.exists(retyped) {
		t.Error("files under a changed field remain")
	}
}

func TestDeleteSubmissionRowFirstAndKeepsFilesOnRowError(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	cv := fx.put(t)
	id := fx.addSub(map[string]any{"cv": cv})
	boom := errors.New("db down")
	fx.repo.deleteErr = boom
	if err := fx.svc.DeleteSubmission(context.Background(), id); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if !fx.exists(cv) {
		t.Error("file deleted although the row delete failed")
	}
}

func TestDeleteSubmissionUnknownID(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	if err := fx.svc.DeleteSubmission(context.Background(), uuid.New()); !errors.Is(err, ErrSubmissionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteSubmissionWithoutFileStoreTouchesNoFiles(t *testing.T) {
	t.Parallel()
	repo := &fileRepo{}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.DeleteSubmission(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if len(repo.deleted) != 1 {
		t.Fatal("row not deleted")
	}
}

func TestDeleteFormRemovesItsFiles(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	a, b := fx.put(t), fx.put(t)
	otherForm := uuid.New()
	st, err := fx.store.Stage(strings.NewReader("x"), 10)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := fx.store.Commit(otherForm, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.DeleteForm(context.Background(), fx.form.ID); err != nil {
		t.Fatal(err)
	}
	if !fx.repo.formDeleted || fx.exists(a) || fx.exists(b) {
		t.Error("form delete left rows or files")
	}
	if f, err := fx.store.Open(otherForm, otherID); err != nil {
		t.Errorf("another form's file removed: %v", err)
	} else {
		f.Close()
	}
}

func TestPurgeExpiredSubmissionsRemovesReturnedFiles(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	a, b := fx.put(t), fx.put(t)
	keep := fx.put(t)
	fx.repo.purged = []PurgedFile{
		{FormID: fx.form.ID, FileID: a["id"].(string)},
		{FormID: fx.form.ID, FileID: b["id"].(string)},
		{FormID: fx.form.ID, FileID: strings.Repeat("0", 32)}, // already gone
	}
	n, err := fx.svc.PurgeExpiredSubmissions(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if fx.exists(a) || fx.exists(b) {
		t.Error("purged files remain")
	}
	if !fx.exists(keep) {
		t.Error("unreferenced file removed")
	}
}

func TestPurgeExpiredSubmissionsDeletesFilesOfRowsGoneBeforeError(t *testing.T) {
	t.Parallel()
	fx := newFileFixture(t)
	a := fx.put(t)
	boom := errors.New("batch failed")
	fx.repo.purged = []PurgedFile{{FormID: fx.form.ID, FileID: a["id"].(string)}}
	fx.repo.purgeErr = boom
	if _, err := fx.svc.PurgeExpiredSubmissions(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if fx.exists(a) {
		t.Error("file of an already deleted row was kept")
	}
}
