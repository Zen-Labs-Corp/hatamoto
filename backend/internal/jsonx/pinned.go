package jsonx

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Pinned option sets keep the historic wire bytes of plugin and webhook
// payloads. They use v2 and jsontext options only (no v1 import).
//
// The marshal set reproduces v1 output for nil slices and maps (null), sorted
// map keys, HTML and U+2028/2029 escaping, invalid UTF-8 (replaced) and, by
// default, []byte, time.Time and floats. It does NOT reproduce four v1
// legacy behaviours; pinned types must avoid them (see pinned_test.go):
//
//   - omitempty on int and bool: tag the field omitzero instead.
//   - pointer-receiver MarshalJSON on a non-addressable value: pass a pointer.
//   - time.Duration: write it through a typed marshaler.
//   - [N]byte: write it through a typed marshaler.
//
// The unmarshal set accepts what v1 accepted from untrusted senders:
// case-insensitive names, duplicate names and invalid UTF-8. Known gap: v1
// merged into an already populated destination; decode into a zero value.
var (
	PinnedMarshal = jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
		jsontext.AllowInvalidUTF8(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
	)
	PinnedUnmarshal = jsonv2.JoinOptions(
		jsonv2.MatchCaseInsensitiveNames(true),
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
	)
)

// MarshalPinned encodes v with PinnedMarshal.
func MarshalPinned(v any) ([]byte, error) {
	return jsonv2.Marshal(v, PinnedMarshal)
}

// UnmarshalPinned decodes in into v with PinnedUnmarshal. Trailing data after
// the first value is still an error.
func UnmarshalPinned(in []byte, v any) error {
	return jsonv2.Unmarshal(in, v, PinnedUnmarshal)
}
