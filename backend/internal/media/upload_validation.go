package media

import "slices"

import "strings"

// mimeToExts maps each ALLOWED sniffed MIME type to the file extension(s)
// permitted for it. image/svg+xml is intentionally absent: SVG can carry
// script, so it is never accepted regardless of extension.
var mimeToExts = map[string][]string{
	"image/jpeg":      {".jpg", ".jpeg"},
	"image/png":       {".png"},
	"image/gif":       {".gif"},
	"image/webp":      {".webp"},
	"application/pdf": {".pdf"},
	"video/mp4":       {".mp4"},
	"video/webm":      {".webm"},
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
	for m, exts := range mimeToExts {
		if strings.HasPrefix(m, "image/") && slices.Contains(exts, ext) {
			return true
		}
	}
	return false
}
