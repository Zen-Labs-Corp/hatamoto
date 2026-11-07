package form

import (
	"fmt"
	"strings"
)

const (
	// MaxFileUploadMB is the site ceiling for one uploaded file.
	MaxFileUploadMB = 25
	// DefaultFileMaxMB is the limit of a file field that sets none.
	DefaultFileMaxMB = 10
)

// FileRef is the answer to a file field as stored in a submission's data.
// ID is an opaque generated id (never a path or the client's filename); Name
// is the sanitised display name; Type is the sniffed content type.
//
// Only the server makes a FileRef, after it has stored an upload: Validate
// refuses every other kind of value for a file field.
type FileRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

// String is the display form used by CSV, email and the drawer, for example
// "report.pdf (240 KB)". It never contains a path or URL.
func (r FileRef) String() string {
	return r.Name + " (" + formatFileSize(r.Size) + ")"
}

func formatFileSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", (n+512)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// FileRefFromAny reads a stored answer back into a FileRef. It accepts a
// FileRef or the map[string]any a jsonb column decodes to, and reports false
// for anything else or a ref without an id.
func FileRefFromAny(v any) (FileRef, bool) {
	switch x := v.(type) {
	case FileRef:
		return x, x.ID != ""
	case *FileRef:
		if x == nil {
			return FileRef{}, false
		}
		return *x, x.ID != ""
	case map[string]any:
		var r FileRef
		r.ID, _ = x["id"].(string)
		r.Name, _ = x["name"].(string)
		r.Type, _ = x["type"].(string)
		switch n := x["size"].(type) {
		case float64:
			r.Size = int64(n)
		case int64:
			r.Size = n
		case int:
			r.Size = int64(n)
		}
		return r, r.ID != ""
	}
	return FileRef{}, false
}

// FileMaxMB is the field's size limit in MB: its MaxMB, else the default,
// never above the site ceiling.
func (f FormField) FileMaxMB() int {
	n := DefaultFileMaxMB
	if f.Validation != nil && f.Validation.MaxMB != nil {
		n = *f.Validation.MaxMB
	}
	return min(max(n, 1), MaxFileUploadMB)
}

// FileAccept is the value for an <input accept> attribute, for example
// ".pdf,.docx".
func (f FormField) FileAccept() string {
	if f.Validation == nil {
		return ""
	}
	exts := make([]string, 0, len(f.Validation.Accept))
	for _, e := range f.Validation.Accept {
		exts = append(exts, "."+e)
	}
	return strings.Join(exts, ",")
}
