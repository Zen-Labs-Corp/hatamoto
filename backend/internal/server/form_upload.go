package server

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/form"
)

const (
	// maxFormTextBytes caps the text parts of a multipart submit in total.
	maxFormTextBytes = 1 << 20
	// uploadSlackBytes is what a multipart body may add to the file limits:
	// boundaries, part headers and the text parts.
	uploadSlackBytes = 1 << 20
	// uploadDeadline is how long a request with files may take to read and
	// answer; the server-wide 30 s would cut a large upload on a slow link.
	uploadDeadline = 5 * time.Minute
)

// uploadedFile is one file part held in the private store's staging area until
// the submission passes validation and the spam check.
type uploadedFile struct {
	staged *form.Staged
	ref    form.FileRef // ID stays empty until the file is committed
}

// uploads are the staged files of one request by field name.
type uploads map[string]*uploadedFile

// discard removes every staged file that was not committed.
func (u uploads) discard() {
	for _, f := range u {
		f.staged.Discard()
	}
}

// pendingFileID stands in for the generated id while a file is staged, so the
// answer passes Validate (which only takes refs with an id).
const pendingFileID = "00000000000000000000000000000000"

// readMultipartSubmit streams a multipart/form-data submit. Text parts come
// back as post (control fields included); each file part of a declared file
// field is staged and checked against the field's accept list, size limit and
// content. A file part for any other name, and a second part for the same
// field, is discarded. A part that breaks a file rule fails the request with
// the field's own message and leaves nothing staged.
//
// The body is cut at the sum of the file limits plus a small slack (never
// beyond the site ceiling), so an oversize upload is not read to the end.
func (s *Server) readMultipartSubmit(w http.ResponseWriter, r *http.Request, f *form.Form) (url.Values, uploads, error) {
	fileFields := map[string]*form.FormField{}
	budget := int64(0)
	for i := range f.Fields {
		if fld := &f.Fields[i]; fld.Type == form.FieldTypeFile && fld.Name != "" {
			fileFields[fld.Name] = fld
			budget += int64(fld.FileMaxMB()) << 20
		}
	}
	budget = min(budget, int64(form.MaxFileUploadMB)<<20) + uploadSlackBytes
	if len(fileFields) > 0 {
		rc := http.NewResponseController(w)
		deadline := time.Now().Add(uploadDeadline)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline)
	}
	if r.ContentLength > budget {
		return nil, nil, errRequestTooLarge
	}
	r.Body = http.MaxBytesReader(w, r.Body, budget)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, errInvalidBody
	}

	post := url.Values{}
	files := uploads{}
	errs := map[string]string{}
	textLeft := int64(maxFormTextBytes)
	fail := func(err error) (url.Values, uploads, error) {
		files.discard()
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, nil, errRequestTooLarge
		}
		return nil, nil, errInvalidBody
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		name := part.FormName()
		if part.FileName() == "" {
			b, err := io.ReadAll(io.LimitReader(part, textLeft+1))
			if err != nil {
				return fail(err)
			}
			if textLeft -= int64(len(b)); textLeft < 0 {
				files.discard()
				return nil, nil, errRequestTooLarge
			}
			// A browser sends an empty text part for an unchosen file input;
			// any other text for a file field stays so Validate refuses it.
			if _, isFile := fileFields[name]; !isFile || len(b) > 0 {
				post.Add(name, string(b))
			}
			continue
		}
		fld, ok := fileFields[name]
		if !ok || files[name] != nil || errs[name] != "" {
			if _, err := io.Copy(io.Discard, part); err != nil {
				return fail(err)
			}
			continue
		}
		up, msg, err := s.stagePart(f, fld, part)
		if err != nil {
			return fail(err)
		}
		if msg != "" {
			errs[name] = msg
		} else if up != nil {
			files[name] = up
		}
	}
	if len(errs) > 0 {
		files.discard()
		return nil, nil, &submitError{status: http.StatusBadRequest, msg: "validation failed", fields: errs}
	}
	return post, files, nil
}

var (
	errInvalidBody     = &submitError{status: http.StatusBadRequest, msg: "invalid request body"}
	errRequestTooLarge = &submitError{status: http.StatusRequestEntityTooLarge, msg: "request too large"}
)

// stagePart stages one file part of fld. It returns the upload, or the
// field's message when a file rule fails, or an error when the body breaks.
// An empty file counts as no upload.
func (s *Server) stagePart(f *form.Form, fld *form.FormField, part *multipart.Part) (*uploadedFile, string, error) {
	store := s.formService.FileStore()
	if store == nil {
		return nil, "", errors.New("file store is not configured")
	}
	name := form.SanitizeName(part.FileName())
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	if fld.Validation == nil || !slices.Contains(fld.Validation.Accept, ext) {
		return nil, fileMessage(fld, "is not an accepted file type. Allowed: "+fld.FileAccept()+"."), nil
	}
	st, err := store.Stage(part, int64(fld.FileMaxMB())<<20)
	switch {
	case errors.Is(err, form.ErrFileTooLarge):
		return nil, fileMessage(fld, "must be smaller than "+strconv.Itoa(fld.FileMaxMB())+" MB."), nil
	case err != nil:
		return nil, "", err
	}
	if st.Size == 0 {
		st.Discard()
		return nil, "", nil
	}
	mime, err := st.Check(ext)
	if err != nil {
		st.Discard()
		if errors.Is(err, form.ErrFileType) {
			return nil, fileMessage(fld, "does not look like a "+strings.ToUpper(ext)+" file."), nil
		}
		return nil, "", err
	}
	return &uploadedFile{staged: st, ref: form.FileRef{Name: name, Size: st.Size, Type: mime}}, "", nil
}

// fileMessage is the field's own message when the editor set one, else its
// label followed by the default sentence.
func fileMessage(fld *form.FormField, def string) string {
	if v := fld.Validation; v != nil && v.Message != nil && strings.TrimSpace(*v.Message) != "" {
		return *v.Message
	}
	return fld.Label + " " + def
}

// commitUploads moves the staged files to their final place and puts the
// generated id into each answer. With dropped (the form stores no answers)
// the files are discarded and the answer keeps only name, size and type. On
// an error the files committed so far are removed again.
func (s *Server) commitUploads(f *form.Form, files uploads, data map[string]interface{}, dropped bool) ([]string, error) {
	var committed []string
	for name, up := range files {
		ref := up.ref
		if dropped {
			up.staged.Discard()
			data[name] = ref
			continue
		}
		id, err := s.formService.FileStore().Commit(f.ID, up.staged)
		if err != nil {
			s.removeUploads(f, committed)
			return nil, err
		}
		committed = append(committed, id)
		ref.ID = id
		data[name] = ref
	}
	return committed, nil
}

// removeUploads deletes committed files of a submission that was not stored.
func (s *Server) removeUploads(f *form.Form, ids []string) {
	for _, id := range ids {
		if err := s.formService.FileStore().Delete(f.ID, id); err != nil {
			s.logger.Warn("failed to remove uploaded file", "error", err, "form_id", f.ID.String())
		}
	}
}
