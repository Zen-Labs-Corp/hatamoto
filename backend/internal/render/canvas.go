package render

import (
	"bytes"
	_ "embed"
	"regexp"
)

// canvasBridgeTag is the opening tag of the injected script; it is what the
// double-injection guard looks for (page content may contain the bare id).
const canvasBridgeTag = `<script id="hm-canvas-bridge">`

//go:embed canvas_bridge.js
var canvasBridgeJS string

var (
	// The next character must close the tag or start attributes, so <header> is not <head>.
	headOpen = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)
	htmlOpen = regexp.MustCompile(`(?i)<html(?:\s[^>]*)?>`)
)

// InjectCanvasBridge inserts the canvas bridge <script> as the first thing in
// <head>, so it runs before any page script and can latch its private port.
// Without a head it goes after the <html> open tag, then before the last
// </body>. It is a no-op (returns html unchanged) when the document has none
// of these or already carries the bridge.
func InjectCanvasBridge(html []byte) []byte {
	if bytes.Contains(html, []byte(canvasBridgeTag)) {
		return html
	}
	i := -1
	if loc := headOpen.FindIndex(html); loc != nil {
		i = loc[1]
	} else if loc := htmlOpen.FindIndex(html); loc != nil {
		i = loc[1]
	} else {
		i = bytes.LastIndex(html, []byte("</body>"))
	}
	if i < 0 {
		return html
	}
	tag := canvasBridgeTag + canvasBridgeJS + "</script>\n"
	out := make([]byte, 0, len(html)+len(tag))
	out = append(out, html[:i]...)
	out = append(out, tag...)
	return append(out, html[i:]...)
}
