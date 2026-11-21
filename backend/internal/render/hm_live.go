package render

import _ "embed"

// LiveScriptName is the asset name of the static-mode comment form loader under AssetPrefix.
const LiveScriptName = "hm-live.js"

//go:embed hm_live.js
var hmLiveSrc []byte
