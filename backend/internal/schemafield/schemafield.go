// Package schemafield holds rules about entity-schema fields that the admin
// form and the changeset gate must agree on. It imports nothing from either.
package schemafield

import "strings"

// IsImageField is the rule that makes a string field an image picker: marked
// x-media, or a uri whose name says "image" (the built-in featured_image).
func IsImageField(name, format string, xMedia bool) bool {
	return xMedia || (format == "uri" && strings.Contains(strings.ToLower(name), "image"))
}

// IsURIField reports whether a field is a uri after the admin has read its
// x-hm-field hint: "url" and "image" make a uri, the text hints ("richtext",
// "html", "code", "css") override any format, and otherwise format decides.
func IsURIField(format, xHm string) bool {
	switch xHm {
	case "url", "image":
		return true
	case "richtext", "html", "code", "css":
		return false
	}
	return format == "uri"
}

// ImageAttributeNames are the entity attribute keys the public readers take as
// an image by name, whatever the schema says about them: the SEO panel's social
// image (internal/seopanel/attrs.go), og:image (internal/server/public_pages_loader.go),
// sitemap images (internal/server/public_handlers.go) and JSON-LD images
// (internal/entity/public_repository.go). A new reader of such a key belongs here.
var ImageAttributeNames = []string{"og_image", "featured_image", "image", "photo"}

// IsImageAttributeName reports whether a public reader treats the attribute
// key as an image by name.
func IsImageAttributeName(key string) bool {
	k := strings.ToLower(key)
	for _, n := range ImageAttributeNames {
		if k == n {
			return true
		}
	}
	return false
}

// mediaPathMarkers are path fragments under which the server serves library
// files ("/media" is the default MEDIA_BASE_URL; "/uploads" is the legacy and
// import path). The percent-encoded form is included so an encoded slash does
// not hide one.
var mediaPathMarkers = []string{"/media/", "/uploads/", "%2fmedia%2f", "%2fuploads%2f"}

// MentionsMediaPath reports whether a string carries a path the media library
// is served under, alone ("/media/a.jpg", "media/a.jpg"), behind the site's own
// host or inside markup. It is deliberately broad: a false positive only makes
// a change wait for a person.
func MentionsMediaPath(s string) bool {
	l := strings.ToLower(s)
	for _, m := range mediaPathMarkers {
		if strings.Contains(l, m) || strings.HasPrefix(l, m[1:]) {
			return true
		}
	}
	return false
}
