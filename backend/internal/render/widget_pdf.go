package render

import (
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
			x.Text = "Download PDF"
		}
		return x, nil
	})
}
