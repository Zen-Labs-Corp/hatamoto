package render

import (
	"fmt"
	"net/url"
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
	bad := func(r rune) bool { return unicode.IsControl(r) || strings.ContainsRune(`\"'<>?# `, r) }
	// The decoded path is checked too: %2e%2e is '..' and %5c a backslash downstream.
	d, err := url.PathUnescape(p)
	if err != nil || !strings.HasPrefix(p, "/media/") || strings.Contains(d, "..") ||
		strings.ContainsFunc(p, bad) || strings.ContainsFunc(d, func(r rune) bool { return bad(r) && r != ' ' }) {
		return ""
	}
	return p
}

// pdfRemoteHref is raw when it is an absolute https address, else "": such a
// PDF (a CDN media base URL) gets a plain link, never a frame.
func pdfRemoteHref(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 9 || !strings.EqualFold(raw[:8], "https://") || safeURL(raw) == "#" ||
		strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || r == ' ' }) {
		return ""
	}
	return raw
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
		raw := str(w.P, "src")
		x := struct {
			Src, Href, Title, Text, Name string
			Height                       int
			Frame                        bool
		}{Title: str(w.P, "title"), Text: str(w.P, "linktext"), Src: pdfSrc(raw)}
		if x.Src == "" {
			x.Href = pdfRemoteHref(raw)
		}
		if x.Src == "" && x.Href == "" && !w.Canvas {
			return nil, errWidgetEmpty // nothing to show: no empty styled box
		}
		if x.Text == "" {
			x.Text = "Download PDF"
			if x.Title != "" {
				x.Text += ": " + x.Title // told apart from the other PDFs on the page
			}
			x.Text += sizeSuffix(w, x.Src)
		}
		if x.Title == "" {
			x.Title = "PDF document"
		}
		x.Height = min(max(intProp(w.P, "height", 600, -1<<30, 1<<30), 200), 2000)
		if x.Src != "" {
			x.Name = path.Base(x.Src)
			x.Href = x.Src
			x.Frame = !w.View.NoFrames // link only in a sandboxed document
		}
		return x, nil
	})
}
