package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestCanvasBridgeScriptHashMatchesInjectedBody(t *testing.T) {
	out := InjectCanvasBridge([]byte("<html><head></head><body></body></html>"))
	start := bytes.Index(out, []byte(canvasBridgeTag))
	if start < 0 {
		t.Fatal("bridge not injected")
	}
	body := out[start+len(canvasBridgeTag):]
	end := bytes.Index(body, []byte("</script>"))
	if end < 0 {
		t.Fatal("no closing tag")
	}
	sum := sha256.Sum256(body[:end])
	if want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; CanvasBridgeScriptHash() != want {
		t.Errorf("hash = %s, want %s", CanvasBridgeScriptHash(), want)
	}
}

// The CSP hash is computed over the embedded bytes, but the HTML parser turns CRLF into LF before the browser
// hashes the script. A CRLF checkout would make the hashes differ and the bridge would be blocked.
func TestCanvasBridgeJSHasNoCR(t *testing.T) {
	if strings.Contains(canvasBridgeJS, "\r") {
		t.Fatal("canvas_bridge.js contains CR: the browser normalises CRLF to LF before hashing, so the script-src hash would not match and the canvas would be blocked; keep the file LF-only")
	}
}
