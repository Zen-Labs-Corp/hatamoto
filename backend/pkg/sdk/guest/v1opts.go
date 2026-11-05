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
	jsontext.AllowDuplicateNames(true),
)
