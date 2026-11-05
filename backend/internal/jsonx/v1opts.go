package jsonx

import (
	"encoding/json" // the one reserved v1 bridge, allow-listed in scripts/check-jsonv1.sh
	jsonv2 "encoding/json/v2"
)

// legacyMarshal holds the v1 behaviours the pinned marshal set needs, taken
// from encoding/json (v2-backed since go1.27). They are the only reason this
// file imports encoding/json; nothing else here may use v1 API.
var legacyMarshal = jsonv2.JoinOptions(
	json.OmitEmptyWithLegacySemantics(true),
	json.FormatDurationAsNano(true),
	json.FormatByteArrayAsArray(true),
	json.CallMethodsWithLegacySemantics(true),
)
