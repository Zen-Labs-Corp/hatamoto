package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/form"
)

var testPDF = append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 200)...)

type uploadPart struct {
	field, filename string
	body            []byte
}

func newUploadServer(t *testing.T, fields ...form.FormField) (*Server, *fakeFormRepo, string) {
	t.Helper()
	maxMB := 1
	fields = append([]form.FormField{
		{ID: "n", Type: form.FieldTypeText, Name: "name", Label: "Name", Required: true},
		{ID: "cv", Type: form.FieldTypeFile, Name: "cv", Label: "CV", Validation: &form.FieldValidation{Accept: []string{"pdf", "png"}, MaxMB: &maxMB}},
	}, fields...)
	f := &form.Form{ID: uuid.New(), Slug: "t", Status: form.StatusActive, Fields: fields}
	f.Settings.StoreSubmissions = true
	repo := &fakeFormRepo{forms: map[string]*form.Form{"t": f}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	store, err := form.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := form.NewService(repo, logger)
	svc.SetFileStore(store)
	return &Server{cfg: &config.Config{}, logger: logger, formService: svc}, repo, root
}

func multipartBody(t *testing.T, text map[string]string, parts ...uploadPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range text {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range parts {
		fw, err := mw.CreateFormFile(p.field, p.filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(p.body)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func postMultipart(srv *Server, body io.Reader, ct string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/t/submit", body)
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("slug", "t")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	return rec
}

// storedFiles lists every regular file under root (final and staged).
func storedFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestFormUploadStoresFile(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	body, ct := multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "../../a\\b\r\nCV.pdf", testPDF})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(repo.subs) != 1 {
		t.Fatalf("subs = %d", len(repo.subs))
	}
	ref, ok := form.FileRefFromAny(repo.subs[0].Data["cv"])
	if !ok || !form.ValidFileID(ref.ID) || ref.Type != "application/pdf" || ref.Size != int64(len(testPDF)) {
		t.Fatalf("ref = %#v", repo.subs[0].Data["cv"])
	}
	if strings.ContainsAny(ref.Name, "/\\\r\n") {
		t.Errorf("name %q keeps path or control characters", ref.Name)
	}
	files := storedFiles(t, root)
	if len(files) != 1 || filepath.Base(files[0]) != ref.ID || filepath.Base(filepath.Dir(files[0])) != repo.forms["t"].ID.String() {
		t.Errorf("stored files = %v, want only <form>/%s", files, ref.ID)
	}
}

func TestFormUploadRefusals(t *testing.T) {
	t.Parallel()
	exe := append([]byte("MZ\x90\x00"), bytes.Repeat([]byte{0}, 100)...)
	cases := []struct {
		name string
		part uploadPart
		text map[string]string
		want string // field key expected in the 400 reply
	}{
		{"extension outside accept list", uploadPart{"cv", "cv.exe", exe}, nil, "cv"},
		{"exe renamed pdf", uploadPart{"cv", "cv.pdf", exe}, nil, "cv"},
		{"html renamed png", uploadPart{"cv", "cv.png", []byte("<html><script>alert(1)</script></html>")}, nil, "cv"},
		{"over the size limit", uploadPart{"cv", "cv.pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 1<<20)...)}, nil, "cv"},
		{"required file missing", uploadPart{}, nil, "cv"},
		{"forged ref as text", uploadPart{}, map[string]string{"cv": `{"id":"` + strings.Repeat("a", 32) + `","name":"x","size":1,"type":"application/pdf"}`}, "cv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, repo, root := newUploadServer(t)
			repo.forms["t"].Fields[1].Required = true
			text := map[string]string{"name": "Ada"}
			for k, v := range tc.text {
				text[k] = v
			}
			var parts []uploadPart
			if tc.part.field != "" {
				parts = append(parts, tc.part)
			}
			body, ct := multipartBody(t, text, parts...)
			rec := postMultipart(srv, body, ct)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"`+tc.want+`"`) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body)
			}
			if len(repo.subs) != 0 {
				t.Errorf("a submission was stored")
			}
			if left := storedFiles(t, root); len(left) != 0 {
				t.Errorf("files left behind: %v", left)
			}
		})
	}
}

func TestFormUploadFieldMessage(t *testing.T) {
	t.Parallel()
	srv, repo, _ := newUploadServer(t)
	msg := "Send a PDF or PNG, please."
	repo.forms["t"].Fields[1].Validation.Message = &msg
	body, ct := multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "cv.docx", testPDF})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), msg) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestFormUploadBodyCutOff(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	// Limit is 1 MB + 1 MiB slack; a 5 MB body is refused by its Content-Length.
	body, ct := multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "cv.pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 5<<20)...)})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Without a Content-Length the reader itself cuts the body off.
	body, ct = multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "cv.pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 5<<20)...)})
	req := httptest.NewRequest(http.MethodPost, "/x", io.NopCloser(body))
	req.ContentLength = -1
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("slug", "t")
	rec = httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(repo.subs) != 0 || len(storedFiles(t, root)) != 0 {
		t.Errorf("stored something: %d subs, files %v", len(repo.subs), storedFiles(t, root))
	}
}

func TestFormUploadJSONRefIsRefused(t *testing.T) {
	t.Parallel()
	srv, repo, _ := newUploadServer(t)
	code, out := submitJSON(t, srv, `{"name":"Ada","cv":{"id":"`+strings.Repeat("a", 32)+`","name":"x.pdf","size":1,"type":"application/pdf"}}`)
	if code != http.StatusBadRequest || out["fields"] == nil || len(repo.subs) != 0 {
		t.Fatalf("code %d out %v subs %d", code, out, len(repo.subs))
	}
}

func TestFormUploadUndeclaredAndExtraPartsDiscarded(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	body, ct := multipartBody(t, map[string]string{"name": "Ada"},
		uploadPart{"cv", "one.pdf", testPDF}, uploadPart{"cv", "two.pdf", testPDF}, uploadPart{"other", "x.pdf", testPDF})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.subs[0].Data["other"]; ok {
		t.Error("undeclared file part reached the data")
	}
	ref, _ := form.FileRefFromAny(repo.subs[0].Data["cv"])
	if ref.Name != "one.pdf" || len(storedFiles(t, root)) != 1 {
		t.Errorf("ref %+v, files %v", ref, storedFiles(t, root))
	}
}

func TestFormUploadLogicHiddenFieldDropsFile(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	repo.forms["t"].Fields[1].Logic = &form.FieldLogic{FieldID: "n", Op: "is", Value: "Grace"}
	body, ct := multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "cv.pdf", testPDF})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if _, ok := repo.subs[0].Data["cv"]; ok || len(storedFiles(t, root)) != 0 {
		t.Errorf("hidden field kept: data %v files %v", repo.subs[0].Data, storedFiles(t, root))
	}
}

func TestFormUploadNoFileFormStaysUnchanged(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	repo.forms["t"].Fields[1].Type = form.FieldTypeText
	code, _ := submitJSON(t, srv, `{"name":"Ada","cv":"plain"}`)
	if code != http.StatusCreated || repo.subs[0].Data["cv"] != "plain" || len(storedFiles(t, root)) != 0 {
		t.Fatalf("code %d data %v", code, repo.subs[0].Data)
	}
}

// failingCreateRepo makes the insert fail after the files were committed.
type failingCreateRepo struct{ *fakeFormRepo }

func (failingCreateRepo) CreateSubmission(context.Context, uuid.UUID, *form.CreateSubmissionInput) (*form.FormSubmission, error) {
	return nil, errors.New("db down")
}

func TestFormUploadFailedInsertRemovesFile(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	srv.formService = form.NewService(failingCreateRepo{repo}, srv.logger)
	store, _ := form.NewFileStore(root)
	srv.formService.SetFileStore(store)
	body, ct := multipartBody(t, map[string]string{"name": "Ada"}, uploadPart{"cv", "cv.pdf", testPDF})
	rec := postMultipart(srv, body, ct)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if left := storedFiles(t, root); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}

func TestFormUploadSpamRowKeepsFile(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	repo.forms["t"].Settings.BlockedWords = []string{"viagra"}
	body, ct := multipartBody(t, map[string]string{"name": "viagra"}, uploadPart{"cv", "cv.pdf", testPDF})
	rec := postMultipart(srv, body, ct)
	// A form word stores the row as spam: the file is kept for review.
	if rec.Code != http.StatusCreated || len(repo.subs) != 1 || repo.subs[0].Status != form.SubmissionStatusSpam {
		t.Fatalf("status %d subs %d", rec.Code, len(repo.subs))
	}
	if len(storedFiles(t, root)) != 1 {
		t.Errorf("spam row lost its file: %v", storedFiles(t, root))
	}
}

func TestAttachFormFileStoreUsesPrivateDataFolder(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	svc := form.NewService(&fakeFormRepo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	attachFormFileStore(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.FileStore() == nil {
		t.Fatal("file store not attached")
	}
	if fi, err := os.Stat(filepath.Join(config.ResolveDataDir(), "private", "form-uploads")); err != nil || !fi.IsDir() {
		t.Fatalf("private folder not created: %v", err)
	}
}
