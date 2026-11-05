package guest

import (
	"encoding/json" // options only, allow-listed in scripts/check-jsonv1.sh
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// legacyMarshal holds the v1 behaviours that plugin-author types still rely
// on when the guest marshals them (call arguments, run summaries, results).
// It mirrors the host's internal/jsonx/v1opts.go and is the only reason this
// package imports encoding/json; nothing else here may use v1 API.
var legacyMarshal = jsonv2.JoinOptions(
	json.OmitEmptyWithLegacySemantics(true),
	json.FormatDurationAsNano(true),
	json.FormatByteArrayAsArray(true),
	json.CallMethodsWithLegacySemantics(true),
	json.StringifyWithLegacySemantics(true),
	jsontext.AllowDuplicateNames(true),
)

// legacyUnmarshal is the read-side counterpart: author types stored or sent
// as v1 wrote them (a time.Duration as nanoseconds, a [N]byte as a number
// array, a ",string" field as a quoted scalar) decode again, names fold case
// only (not "_" or "-") and a populated destination is merged into, as in v1.
var legacyUnmarshal = jsonv2.JoinOptions(
	json.FormatDurationAsNano(true),
	json.FormatByteArrayAsArray(true),
	json.StringifyWithLegacySemantics(true),
	json.CallMethodsWithLegacySemantics(true),
	json.UnmarshalArrayFromAnyLength(true),
	json.MatchCaseSensitiveDelimiter(true),
	json.MergeWithLegacySemantics(true),
)
