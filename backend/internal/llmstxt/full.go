package llmstxt

import (
	"fmt"
	"strings"
)

// Page is one block of /llms-full.txt.
type Page struct {
	Name    string
	Path    string // base-relative
	Updated string // YYYY-MM-DD
	Body    string // Markdown
	Locale  string
}

// FullStats describes a finished /llms-full.txt.
type FullStats struct {
	Pages     int // blocks written
	Left      int // pages left out
	Truncated bool
	Bytes     int
}

// FullWriter builds /llms-full.txt block by block under a byte cap.
type FullWriter struct {
	base     string
	capBytes int
	reserve  int
	buf      strings.Builder
	pages    int
	left     int
	full     bool
}

// NewFullWriter starts the file with its header. The room for the truncation
// line is held back, so the finished file never exceeds capBytes.
func NewFullWriter(site Site, capBytes int) *FullWriter {
	w := &FullWriter{base: strings.TrimRight(site.BaseURL, "/"), capBytes: capBytes}
	w.reserve = len(w.truncationLine(1 << 30))
	w.buf.WriteString("# " + OneLine(site.Name, 0) + ": full text\n")
	if s := OneLine(site.Summary, 0); s != "" {
		w.buf.WriteString("\n> " + s + "\n")
	}
	return w
}

// Add appends p unless its block would pass the cap; it returns false once the
// file is full, and every later page is only counted as left out. A block is
// never cut in half.
func (w *FullWriter) Add(p Page) bool {
	if w.full {
		w.left++
		return false
	}
	var b strings.Builder
	b.WriteString("\n# " + OneLine(p.Name, 0) + "\n\nURL: " + w.base + p.Path + "\nUpdated: " + p.Updated + "\n")
	if body := strings.TrimSpace(p.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	b.WriteString("\n---\n")
	if w.buf.Len()+b.Len()+w.reserve > w.capBytes {
		w.full = true
		w.left++
		return false
	}
	w.buf.WriteString(b.String())
	w.pages++
	return true
}

// Finish ends the file. remaining is the number of pages never offered to Add
// (the caller stopped loading once Add returned false).
func (w *FullWriter) Finish(remaining int) ([]byte, FullStats) {
	left := w.left + remaining
	if left > 0 {
		w.buf.WriteString(w.truncationLine(left))
	}
	return []byte(w.buf.String()), FullStats{Pages: w.pages, Left: left, Truncated: left > 0, Bytes: w.buf.Len()}
}

func (w *FullWriter) truncationLine(left int) string {
	return fmt.Sprintf("\nThis file was truncated at %d KB. %d more pages are left out; see %s/sitemap.xml for the full list.\n",
		w.capBytes/1024, left, w.base)
}
