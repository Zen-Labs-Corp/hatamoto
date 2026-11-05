package guest

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// The wire between guest and host is JSON written and read with these option
// sets. They are a copy of the host's pinned sets (internal/jsonx/pinned.go in
// hatamoto core, which this module cannot import) and must stay equal to
// them: both sides keep the bytes that encoding/json v1 wrote, so a plugin
// built with an older SDK keeps working against a newer host and the other
// way round. wire_test.go in this package and wire_host_test.go in the host
// pin the same literal bytes and fail when either copy drifts.
//
// The marshal set writes nil slices and maps as null, sorts map keys, escapes
// HTML and U+2028/2029, and writes invalid UTF-8 as the replacement
// character. legacyMarshal (v1opts.go) adds v1's omitempty, time.Duration,
// byte-array and method-call behaviour, so plugin-author types marshal as they
// did under v1 (an empty raw JSON field is omitted, as in v1). The unmarshal
// set accepts what v1 accepted from the other side: case-insensitive names,
// duplicate names and invalid UTF-8.
var (
	marshalOptions = jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
		jsontext.AllowInvalidUTF8(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
		legacyMarshal,
	)
	unmarshalOptions = jsonv2.JoinOptions(
		jsonv2.MatchCaseInsensitiveNames(true),
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
	)
)

func marshal(v any) ([]byte, error) { return jsonv2.Marshal(v, marshalOptions) }

func unmarshal(in []byte, v any) error { return jsonv2.Unmarshal(in, v, unmarshalOptions) }
