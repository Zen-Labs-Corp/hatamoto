package media

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strings"
)

// mimeToExts maps each ALLOWED sniffed MIME type to the file extension(s)
// permitted for it. It is the one allowlist of the library. image/svg+xml is
// stored only after SanitizeSVG has rewritten the bytes.
var mimeToExts = map[string][]string{
	"image/jpeg":      {".jpg", ".jpeg"},
	"image/png":       {".png"},
	"image/gif":       {".gif"},
	"image/webp":      {".webp"},
	"image/avif":      {".avif"},
	svgMIME:           {".svg"},
	"application/pdf": {".pdf"},
	"video/mp4":       {".mp4"},
	"video/webm":      {".webm"},
}

// SniffLen is how many leading bytes the sniffers look at.
const SniffLen = 1024

const svgMIME = "image/svg+xml"

// SniffUpload is the one sniff of every upload path: an svg root element
// (after a BOM, whitespace, an xml declaration and comments) is image/svg+xml,
// anything else is http.DetectContentType. AVIF is not recognised here, so an
// AVIF original is still refused.
func SniffUpload(head []byte) string {
	if hasSVGRoot(head) {
		return svgMIME
	}
	return http.DetectContentType(head)
}

// SniffContentType is SniffUpload plus AVIF, which Go does not know. It is for
// serving and restoring stored files.
func SniffContentType(head []byte) string {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if brand := string(head[8:12]); brand == "avif" || brand == "avis" {
			return "image/avif"
		}
	}
	return SniffUpload(head)
}

// hasSVGRoot reports whether the first element of head is <svg. The extension
// is never consulted: a .svg full of HTML is not an svg.
func hasSVGRoot(head []byte) bool {
	b := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	for {
		b = bytes.TrimLeft(b, " \t\r\n")
		switch {
		case bytes.HasPrefix(b, []byte("<?")):
			i := bytes.Index(b, []byte("?>"))
			if i < 0 {
				return false
			}
			b = b[i+2:]
		case bytes.HasPrefix(b, []byte("<!--")):
			i := bytes.Index(b, []byte("-->"))
			if i < 0 {
				return false
			}
			b = b[i+3:]
		default:
			if len(b) < 5 || !bytes.EqualFold(b[:4], []byte("<svg")) {
				return false
			}
			switch b[4] {
			case ' ', '\t', '\r', '\n', '>', '/':
				return true
			}
			return false
		}
	}
}

// readSVG reads a whole svg (at most MaxSVGBytes+1) and sanitises it.
func readSVG(r io.Reader) ([]byte, []string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxSVGBytes+1))
	if err != nil {
		return nil, nil, err
	}
	return SanitizeSVG(raw)
}

// AllowedExtensionsForMIME returns the extensions permitted for a sniffed MIME
// type and whether the type is allowed at all.
func AllowedExtensionsForMIME(mimeType string) ([]string, bool) {
	exts, ok := mimeToExts[mimeType]
	return exts, ok
}

// ExtensionMatchesMIME reports whether a filename extension is consistent with
// the sniffed MIME type. It cross-validates content against the declared name
// so a .png full of JPEG bytes, or a .php renamed .jpg, is rejected. ext is
// matched case-insensitively and must include the leading dot.
func ExtensionMatchesMIME(ext, mimeType string) bool {
	exts, ok := mimeToExts[mimeType]
	if !ok {
		return false
	}
	ext = strings.ToLower(ext)
	return slices.Contains(exts, ext)
}

// ImageTypeAndExtensionAllowed reports whether the sniffed type and the
// extension are each an allowed image type, even when they do not pair.
// Resized copies of WebP uploads written by older versions are JPEG bytes
// under a .webp name; they must still restore. Only images qualify: html,
// svg, pdf and video never pass through here.
func ImageTypeAndExtensionAllowed(ext, mimeType string) bool {
	if !strings.HasPrefix(mimeType, "image/") {
		return false
	}
	if _, ok := mimeToExts[mimeType]; !ok {
		return false
	}
	ext = strings.ToLower(ext)
	if mimeType == svgMIME || ext == ".svg" { // svg pairs only with itself
		return false
	}
	for m, exts := range mimeToExts {
		if strings.HasPrefix(m, "image/") && slices.Contains(exts, ext) {
			return true
		}
	}
	return false
}
