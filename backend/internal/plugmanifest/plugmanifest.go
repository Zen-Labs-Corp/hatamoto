// Package plugmanifest decodes plugin.json the way the plugin host does. It is
// a leaf so the host, the package gate and the server read one manifest
// identically.
package plugmanifest

import (
	"encoding/json/jsontext"
	"reflect"
	"unicode/utf8"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/jsonx/keynorm"
	"hatamoto.dev/sdk"
)

var shape = keynorm.ShapeOf(reflect.TypeOf(sdk.Manifest{}))

// Shape lists the canonical member names of plugin.json.
func Shape() *keynorm.Shape { return shape }

// Decode reads a plugin manifest by the v1 rules: member names match by case
// only, the last of several names that differ just by case wins, and names
// that match a field only after ignoring "_" or "-" (allowedHosts for
// allowed_hosts) are ignored. The v2 folding of those would give two readers
// different manifests.
func Decode(raw []byte, m *sdk.Manifest) error {
	norm, _, err := keynorm.Normalise(ReplaceInvalidUTF8(raw), shape)
	if err != nil {
		return err
	}
	return jsonx.Unmarshal(norm, m, jsonx.Stored, jsontext.AllowDuplicateNames(true))
}

// ReplaceInvalidUTF8 turns every invalid byte into U+FFFD, as v1 did.
func ReplaceInvalidUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	out := make([]byte, 0, len(b)+8)
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			out = append(out, "\ufffd"...)
		} else {
			out = append(out, b[:n]...)
		}
		b = b[n:]
	}
	return out
}
