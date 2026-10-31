package render

import (
	"bytes"
	_ "embed"
)

//go:embed hm_track.js
var hmTrackSrc []byte

// trackScript renders the core beacon: the API prefix rewrite applies as for
// hm.js, and engage=false removes engagement events (the owner's opt-out).
func trackScript(engage bool) []byte {
	flag := "false"
	if engage {
		flag = "true"
	}
	return rewriteAPIPrefix(bytes.ReplaceAll(hmTrackSrc, []byte("__HM_ENGAGE__"), []byte(flag)))
}
