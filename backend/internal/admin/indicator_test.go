package admin

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/web"
)

// hxRequestRx finds an opening tag (or the start of one) that issues an htmx
// request of its own.
var hxRequestRx = regexp.MustCompile(`<[a-zA-Z][^<>]*?\bhx-(?:get|post)=[^<>]*>`)

// Every in-page htmx request that can take time shows a busy state: its
// element carries hx-indicator. Nothing is exempt today; add a comment and an
// entry here when a request is deliberately silent.
func TestEveryHTMXRequestHasAnIndicator(t *testing.T) {
	t.Parallel()

	allow := map[string]string{}
	admin := web.Admin()
	n := 0
	err := fs.WalkDir(admin, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := fs.ReadFile(admin, p)
		if err != nil {
			return err
		}
		for _, tag := range hxRequestRx.FindAllString(string(b), -1) {
			n++
			if !strings.Contains(tag, "hx-indicator=") && allow[p] == "" {
				t.Errorf("%s: htmx request without hx-indicator: %.120s", p, tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 8 {
		t.Errorf("only %d htmx requests found; the walk is broken", n)
	}
}
