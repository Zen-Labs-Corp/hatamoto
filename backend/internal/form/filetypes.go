package form

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/media"
)

// fileKind says how a stored upload is checked beyond its sniffed type.
type fileKind int

const (
	kindBinary fileKind = iota // sniffed type must be one of mimes
	kindText                   // text/plain and no NUL byte
	kindOOXML                  // application/zip holding [Content_Types].xml
)

type fileType struct {
	mimes []string
	kind  fileKind
	// mime is the type stored and served for the extension.
	mime string
}

// fileTypes is the allow-list of extensions a file field can accept. It
// deliberately leaves out anything a browser would run or render inline
// (svg, html, xml, js, exe): an upload is only ever served as an attachment,
// but the table is the first line of defence.
var fileTypes = map[string]fileType{
	"pdf":  {mimes: []string{"application/pdf"}, kind: kindBinary, mime: "application/pdf"},
	"png":  {mimes: []string{"image/png"}, kind: kindBinary, mime: "image/png"},
	"jpg":  {mimes: []string{"image/jpeg"}, kind: kindBinary, mime: "image/jpeg"},
	"jpeg": {mimes: []string{"image/jpeg"}, kind: kindBinary, mime: "image/jpeg"},
	"gif":  {mimes: []string{"image/gif"}, kind: kindBinary, mime: "image/gif"},
	"webp": {mimes: []string{"image/webp"}, kind: kindBinary, mime: "image/webp"},
	"zip":  {mimes: []string{"application/zip"}, kind: kindBinary, mime: "application/zip"},
	"txt":  {kind: kindText, mime: "text/plain"},
	"csv":  {kind: kindText, mime: "text/csv"},
	"md":   {kind: kindText, mime: "text/markdown"},
	"json": {kind: kindText, mime: "application/json"},
	"docx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	"xlsx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"pptx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
}

// ValidAcceptExt reports whether ext (lowercase, no dot) may appear in a file
// field's accept list.
func ValidAcceptExt(ext string) bool {
	_, ok := fileTypes[ext]
	return ok
}

// AcceptExts lists the allowed extensions in a stable order, for the designer
// and for messages.
func AcceptExts() []string {
	return []string{"pdf", "png", "jpg", "jpeg", "gif", "webp", "txt", "csv", "md", "json", "docx", "xlsx", "pptx", "zip"}
}

// ErrFileType is returned by CheckUpload when the content does not match the
// extension, or the extension is not in the table.
var ErrFileType = errors.New("file content does not match its type")

// sniffLen is how much of the file the content sniff reads.
const sniffLen = 1024

// CheckUpload checks size bytes of r against ext (lowercase, no dot): the
// sniffed content type must be one the extension allows (a .pdf that is an
// executable or HTML fails), text types must be text without NUL bytes, and
// Office types must be a zip holding [Content_Types].xml. It returns the
// content type to store and serve for the extension. A mismatch returns
// ErrFileType.
func CheckUpload(ext string, r io.ReaderAt, size int64) (string, error) {
	ft, ok := fileTypes[ext]
	if !ok {
		return "", ErrFileType
	}
	head := make([]byte, min(size, sniffLen))
	if _, err := r.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(head) == 0 {
		return "", ErrFileType
	}
	sniffed := media.SniffContentType(head)
	switch ft.kind {
	case kindBinary:
		if !slices.Contains(ft.mimes, sniffed) {
			return "", ErrFileType
		}
	case kindText:
		if !strings.HasPrefix(sniffed, "text/plain") {
			return "", ErrFileType
		}
		nul, err := containsNUL(r, size)
		if err != nil {
			return "", err
		}
		if nul {
			return "", ErrFileType
		}
	case kindOOXML:
		if sniffed != "application/zip" {
			return "", ErrFileType
		}
		zr, err := zip.NewReader(r, size)
		if err != nil {
			return "", ErrFileType
		}
		if !slices.ContainsFunc(zr.File, func(f *zip.File) bool { return f.Name == "[Content_Types].xml" }) {
			return "", ErrFileType
		}
	}
	return ft.mime, nil
}

func containsNUL(r io.ReaderAt, size int64) (bool, error) {
	buf := make([]byte, 32*1024)
	sr := io.NewSectionReader(r, 0, size)
	for {
		n, err := sr.Read(buf)
		if bytes.IndexByte(buf[:n], 0) >= 0 {
			return true, nil
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}
