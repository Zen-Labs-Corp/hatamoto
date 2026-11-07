package admin

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/form"
)

// filesForms is the fake forms store with a private file store, as form.Service
// offers once one is set.
type filesForms struct {
	*fakeForms
	store *form.FileStore
}

func (f filesForms) FileStore() *form.FileStore { return f.store }

const pdfBody = "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"

// fileRig is a form with a text and a file field, and one submission whose file
// is stored. The path of the download route is returned.
func fileRig(t *testing.T) (g *rig, fm *form.Form, sub *form.FormSubmission, store *form.FileStore, path string) {
	t.Helper()
	store, err := form.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ff := newFakeForms()
	g = newRig(t, func(d *Deps) { d.Growth.Forms = filesForms{ff, store} })
	fm = ff.addForm("Apply",
		form.FormField{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name"},
		form.FormField{ID: "f", Type: form.FieldTypeFile, Label: "CV", Name: "cv", Validation: &form.FieldValidation{Accept: []string{"pdf"}}},
	)
	staged, err := store.Stage(strings.NewReader(pdfBody), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.Commit(fm.ID, staged)
	if err != nil {
		t.Fatal(err)
	}
	ref := form.FileRef{ID: id, Name: "résumé \"v2\".pdf", Type: "application/pdf", Size: int64(len(pdfBody))}
	sub = ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{
		"name": "Ada",
		"cv":   map[string]any{"id": ref.ID, "name": ref.Name, "type": ref.Type, "size": float64(ref.Size)},
	})
	return g, fm, sub, store, "/admin/forms/submissions/" + sub.ID.String() + "/files/cv"
}

func TestSubmissionFileDownload(t *testing.T) {
	t.Parallel()

	g, _, _, _, path := fileRig(t)
	for _, token := range []string{"owner-token", "editor-token"} {
		rec := g.do("GET", path, token, nil, nil)
		if rec.Code != http.StatusOK || rec.Body.String() != pdfBody {
			t.Fatalf("%s: %d %q", token, rec.Code, rec.Body.String())
		}
		h := rec.Header()
		if h.Get("Content-Type") != "application/pdf" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" || h.Get("Content-Security-Policy") != "sandbox" {
			t.Errorf("%s headers: %v", token, h)
		}
		if cd := h.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || strings.Contains(cd, "inline") {
			t.Errorf("%s disposition: %q", token, cd)
		}
	}
}

func TestSubmissionFileAnonymousGetsNothing(t *testing.T) {
	t.Parallel()

	g, _, _, _, path := fileRig(t)
	rec := g.do("GET", path, "", nil, nil)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "%PDF") {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}

func TestSubmissionFileWrongTargetsAre404(t *testing.T) {
	t.Parallel()

	g, fm, sub, store, _ := fileRig(t)
	base := "/admin/forms/submissions/"
	for name, path := range map[string]string{
		"unknown submission": base + uuid.NewString() + "/files/cv",
		"not a file field":   base + sub.ID.String() + "/files/name",
		"unknown field":      base + sub.ID.String() + "/files/nope",
		"a storage key":      base + sub.ID.String() + "/files/" + fm.ID.String(),
		"bad id":             base + "x/files/cv",
	} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "%PDF") {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// the file is gone from disk (retention, a restore): a page, not a crash
	ref, _ := form.FileRefFromAny(sub.Data["cv"])
	if err := store.Delete(fm.ID, ref.ID); err != nil {
		t.Fatal(err)
	}
	if rec := g.do("GET", base+sub.ID.String()+"/files/cv", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing file: %d", rec.Code)
	}
}

func TestSubmissionFileForgedRefStaysInsideTheStore(t *testing.T) {
	t.Parallel()

	g, _, sub, _, path := fileRig(t)
	sub.Data["cv"] = map[string]any{"id": "../../../etc/passwd", "name": "x.pdf", "type": "application/pdf"}
	if rec := g.do("GET", path, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("forged ref: %d", rec.Code)
	}
}

func TestSubmissionFileDrawerLinkAndCSV(t *testing.T) {
	t.Parallel()

	g, fm, sub, _, path := fileRig(t)
	drawer := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/submissions?s="+sub.ID.String(), "owner-token", nil, nil)
	body := drawer.Body.String()
	if drawer.Code != 200 || !strings.Contains(body, `href="`+path+`"`) || !strings.Contains(body, "(") {
		t.Errorf("drawer lacks the download link %s: %d", path, drawer.Code)
	}
	if strings.Contains(body, `href="/admin/forms/submissions/`+sub.ID.String()+`/files/name"`) {
		t.Error("a text answer got a download link")
	}

	rec := g.do("GET", "/admin/forms/submissions/export.csv?form="+fm.ID.String(), "owner-token", nil, nil)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if got := rows[1][4]; !strings.HasPrefix(got, "résumé \"v2\".pdf (") || strings.Contains(got, sub.Data["cv"].(map[string]any)["id"].(string)) {
		t.Errorf("csv cell = %q", got)
	}
}
