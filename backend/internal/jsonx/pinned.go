package jsonx

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Pinned option sets keep the historic wire bytes of plugin and webhook
// payloads. They use v2 and jsontext options, plus the v1 legacy options kept
// in v1opts.go (the single allow-listed encoding/json import).
//
// The marshal set reproduces v1 output for nil slices and maps (null), sorted
// map keys, HTML and U+2028/2029 escaping, invalid UTF-8, omitempty (v1
// semantics: ints, bools and structs are never "empty" as in v2), time.Duration
// (nanoseconds), [N]byte (number array), pointer-receiver MarshalJSON on
// non-addressable values and, by default, []byte, time.Time and floats.
// Invalid UTF-8 becomes the raw replacement character bytes EF BF BD, not the
// six-character escape \ufffd that pre-go1.27 toolchains wrote.
//
// It does NOT reproduce this v1 behaviour (TestPinnedKnownDivergences):
//
//   - two fields of one struct with the same JSON name: v1 dropped both, v2
//     returns an error. Rename one.
//
// The unmarshal set accepts what v1 accepted from untrusted senders:
// case-insensitive names, duplicate names and invalid UTF-8. It differs in
// two ways. v2 name folding also ignores "_" and "-", so {"user_id":1,
// "userid":2} sets user_id to 2 where v1 set 1 (rename the field or keep such
// pairs out of pinned input types). And v1 merged into an already populated
// destination; decode into a zero value.
var (
	PinnedMarshal = jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
		jsontext.AllowInvalidUTF8(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
		legacyMarshal,
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
