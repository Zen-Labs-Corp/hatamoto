// Package schemafield holds rules about entity-schema fields that the admin
// form and the changeset gate must agree on. It imports nothing from either.
package schemafield

import "strings"

// IsImageField is the rule that makes a string field an image picker: marked
// x-media, or a uri whose name says "image" (the built-in featured_image).
func IsImageField(name, format string, xMedia bool) bool {
	return xMedia || (format == "uri" && strings.Contains(strings.ToLower(name), "image"))
}
