package admin

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"time"

	"hatamoto.dev/core/internal/form"
)

// fileStorer is what the form service offers once it has a private file store.
type fileStorer interface{ FileStore() *form.FileStore }

// formFileStore is the private store of form uploads, nil when forms keep none.
func (a *Admin) formFileStore() *form.FileStore {
	if fs, ok := a.deps.Growth.Forms.(fileStorer); ok {
		return fs.FileStore()
	}
	return nil
}

// submissionFileURL is where an owner or editor downloads the file answer of a
// field. The file is named by submission and field; the store key never leaves
// the server.
func (a *Admin) submissionFileURL(sid, field string) string {
	return a.adminPath + "/forms/submissions/" + sid + "/files/" + field
}

// handleSubmissionFile sends the file a visitor uploaded to a file field, as a
// download. The reference comes from the stored submission row, never from the
// URL, and the bytes are served as an attachment the browser must not sniff or
// cache.
func (a *Admin) handleSubmissionFile(w http.ResponseWriter, r *http.Request) {
	f, s, ok := a.loadSubmission(w, r)
	if !ok {
		return
	}
	name := r.PathValue("field")
	var ref form.FileRef
	found := false
	for _, ff := range f.Fields {
		if ff.Type == form.FieldTypeFile && ff.Name == name {
			ref, found = form.FileRefFromAny(s.Data[name])
			break
		}
	}
	store := a.formFileStore()
	if !found || store == nil {
		a.fileNotFound(w, r)
		return
	}
	file, err := store.Open(f.ID, ref.ID)
	if errors.Is(err, fs.ErrNotExist) {
		a.fileNotFound(w, r)
		return
	}
	if err != nil {
		a.logger().Error("form file open", "err", err, "submission", s.ID)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The file could not be opened.")
		return
	}
	defer file.Close()
	a.audit(r, "forms.file.download", s.ID.String(), map[string]any{"form": f.ID.String(), "field": name})
	ctype := ref.Type
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": ref.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "sandbox")
	// An upload can take longer to send than the server's write timeout.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		a.logger().Warn("form file download: clear write deadline", "err", err, "submission", s.ID)
	}
	http.ServeContent(w, r, ref.Name, s.CreatedAt, file)
}

func (a *Admin) fileNotFound(w http.ResponseWriter, r *http.Request) {
	a.Error(w, r, http.StatusNotFound, "Not found", "That file does not exist.")
}
