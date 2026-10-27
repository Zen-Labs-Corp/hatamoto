package wpimport

import (
	"strings"
	"testing"
)

// FuzzParseWXR feeds arbitrary bytes to the WXR reader. An export is
// untrusted input from another system: any bytes may arrive, and the parser
// must return a Dump or an error, within its size limits.
func FuzzParseWXR(f *testing.F) {
	f.Add(sampleWXR)
	f.Add(`<?xml version="1.0"?><rss><channel></channel></rss>`)
	f.Add(`<rss xmlns:wp="http://wordpress.org/export/1.2/"><channel><item><wp:post_id>x</wp:post_id></item></channel></rss>`)
	f.Add(`<!DOCTYPE x [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;">]><rss><channel><title>&b;</title></channel></rss>`)
	f.Add("<html>no</html>")
	f.Add("")
	f.Fuzz(func(t *testing.T, doc string) {
		_, _ = ParseWXR(strings.NewReader(doc), WXROptions{MaxBytes: 1 << 20, MaxItems: 1000})
	})
}
