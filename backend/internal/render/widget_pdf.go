package render

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// pdfSrc is the address of a PDF of this site's media library, or "" when src
// is anything else (a remote URL, another scheme, a path outside /media/, a
// query or fragment, traversal). Only the library is framed: the media
// handler sends the headers that let the site's own pages frame a PDF.
func pdfSrc(src string) string {
	p := sitePath(src)
	if !strings.HasPrefix(p, "/media/") || strings.Contains(p, "..") ||
		strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || strings.ContainsRune(`\"'<>?# `, r) }) {
		return ""
	}
	return p
}

// sizeSuffix is " (2.4 MB)" for a PDF the data source knows the size of, else "".
func sizeSuffix(w *widgetCtx, src string) string {
	s, ok := w.E.opts.Data.(FileSizer)
	if !ok || src == "" {
		return ""
	}
	n, ok := s.FileSize(src)
	switch {
	case !ok || n <= 0:
		return ""
	case n >= 1<<20:
		return fmt.Sprintf(" (%.1f MB)", float64(n)/(1<<20))
	}
	return fmt.Sprintf(" (%d KB)", max(n/(1<<10), 1))
}

func init() {
	registerWidget("pdf", func(w *widgetCtx) (any, error) {
		w.Class = "hm-pdf"
		x := struct {
			Src, Title, Text, Name string
			Height                 int
		}{Title: str(w.P, "title"), Text: str(w.P, "linktext"), Src: pdfSrc(str(w.P, "src"))}
		if x.Title == "" {
			x.Title = "PDF document"
		}
		x.Height = min(max(intProp(w.P, "height", 600, -1<<30, 1<<30), 200), 2000)
		if x.Src != "" {
			x.Name = path.Base(x.Src)
		}
		if x.Text == "" {
			x.Text = "Download PDF" + sizeSuffix(w, x.Src)
		}
		return x, nil
	})
}
