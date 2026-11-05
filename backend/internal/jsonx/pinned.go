package jsonx

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Pinned option sets keep the historic wire bytes of plugin and webhook
// payloads. They use v2 and jsontext options only (no v1 import).
//
// The marshal set reproduces v1 output for nil slices and maps (null), sorted
// map keys, HTML and U+2028/2029 escaping, invalid UTF-8 and, by default,
// []byte, time.Time and floats. Invalid UTF-8 becomes the raw replacement
// character bytes EF BF BD, not the six-character escape \ufffd that
// pre-go1.27 toolchains wrote.
//
// It does NOT reproduce these v1 behaviours; pinned types must avoid them
// (each is a case of TestPinnedKnownDivergences, with its fix). Known as of
// go1.27.3:
//
//   - omitempty on int and bool: tag the field omitzero instead.
//   - omitempty on a field that encodes as {}, "" or null (a struct whose
//     fields are all omitted, a non-nil pointer to "", a MarshalJSON result
//     of {}, "" or null): v2 omits it, v1 wrote it. Tag it omitzero, or drop
//     omitempty.
//   - pointer-receiver MarshalJSON on a non-addressable value: pass a pointer.
//   - time.Duration: write it through a typed marshaler.
//   - [N]byte: write it through a typed marshaler.
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
