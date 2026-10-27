package render

import (
	"crypto/sha256"
	"encoding/base64"
	"sync"
)

var (
	canvasBridgeHashOnce sync.Once
	canvasBridgeHash     string
)

// CanvasBridgeScriptHash returns the CSP hash source ('sha256-...') of the
// exact script body InjectCanvasBridge places inside the bridge <script> tag,
// so a CSP can allow the bridge and no other script.
func CanvasBridgeScriptHash() string {
	canvasBridgeHashOnce.Do(func() {
		sum := sha256.Sum256([]byte(canvasBridgeJS))
		canvasBridgeHash = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	})
	return canvasBridgeHash
}
