package render

import (
	"bytes"
	_ "embed"
	"html/template"
)

// TrackScriptName is the asset name of the core beacon under AssetPrefix.
const TrackScriptName = "hm-track.js"

// trackTag is the script tag spliced into every non-editor public page.
const trackTag = template.HTML(`<script src="` + AssetPrefix + TrackScriptName + `" defer></script>`)

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
