package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func postNative(srv *Server, body *bytes.Buffer, ct string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/forms/t/submit", body)
	req.Header.Set("Content-Type", ct)
	req.SetPathValue("slug", "t")
	rec := httptest.NewRecorder()
	srv.handlePublicFormPost(rec, req)
	return rec
}

func TestFormPostMultipartStoresFile(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	body, ct := multipartBody(t, map[string]string{"name": "Ada", "_redirect": "/apply"}, uploadPart{"cv", "cv.pdf", testPDF})
	rec := postNative(srv, body, ct)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/apply?submitted=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	ref, ok := form.FileRefFromAny(repo.subs[0].Data["cv"])
	if !ok || ref.Name != "cv.pdf" || len(storedFiles(t, root)) != 1 {
		t.Fatalf("ref %+v files %v", ref, storedFiles(t, root))
	}
	if _, leaked := repo.subs[0].Data["_redirect"]; leaked {
		t.Error("_redirect leaked into data")
	}
}

func TestFormPostMultipartRefusals(t *testing.T) {
	t.Parallel()
	exe := append([]byte("MZ\x90\x00"), bytes.Repeat([]byte{0}, 100)...)
	for name, part := range map[string]uploadPart{
		"wrong type":    {"cv", "cv.exe", exe},
		"sniff differs": {"cv", "cv.pdf", exe},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, repo, root := newUploadServer(t)
			body, ct := multipartBody(t, map[string]string{"name": "Ada", "_redirect": "/apply"}, part)
			rec := postNative(srv, body, ct)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/apply?form_error=1" {
				t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
			}
			if len(repo.subs) != 0 || len(storedFiles(t, root)) != 0 {
				t.Errorf("stored something: %d subs, %v", len(repo.subs), storedFiles(t, root))
			}
		})
	}
}

func TestFormPostMultipartHoneypotStoresNothing(t *testing.T) {
	t.Parallel()
	srv, repo, root := newUploadServer(t)
	body, ct := multipartBody(t, map[string]string{"name": "bot", "hm_website": "x"}, uploadPart{"cv", "cv.pdf", testPDF})
	rec := postNative(srv, body, ct)
	if rec.Code != http.StatusSeeOther || len(repo.subs) != 0 || len(storedFiles(t, root)) != 0 {
		t.Fatalf("got %d subs %d files %v", rec.Code, len(repo.subs), storedFiles(t, root))
	}
}

func TestFormatFieldValueFile(t *testing.T) {
	t.Parallel()
	ref := form.FileRef{ID: strings.Repeat("a", 32), Name: "cv.pdf", Size: 245760, Type: "application/pdf"}
	fromJSON := map[string]any{"id": ref.ID, "name": "cv.pdf", "size": float64(245760), "type": "application/pdf"}
	for _, v := range []any{ref, fromJSON} {
		if got := formatFieldValue(v); got != "cv.pdf (240 KB)" {
			t.Errorf("formatFieldValue(%T) = %q", v, got)
		}
	}
	// With storage off the answer has no id; the name still shows.
	if got := formatFieldValue(form.FileRef{Name: "cv.pdf", Size: 10}); got != "cv.pdf (10 B)" {
		t.Errorf("no-id ref = %q", got)
	}
}

func TestFormViewFileField(t *testing.T) {
	t.Parallel()
	mb := 5
	v := formView(&form.Form{Slug: "apply", Fields: []form.FormField{
		{Name: "cv", Label: "CV", Type: form.FieldTypeFile, Validation: &form.FieldValidation{Accept: []string{"pdf", "docx"}, MaxMB: &mb}},
		{Name: "name", Label: "Name", Type: form.FieldTypeText},
	}})
	if got := v.Fields[0]; got.Accept != ".pdf,.docx" || got.Hint != ".pdf, .docx · 5 MB" {
		t.Errorf("file field = %+v", got)
	}
	if got := v.Fields[1]; got.Accept != "" || got.Hint != "" {
		t.Errorf("text field carries file props: %+v", got)
	}
	if !v.HasFile() {
		t.Error("HasFile = false")
	}
}
