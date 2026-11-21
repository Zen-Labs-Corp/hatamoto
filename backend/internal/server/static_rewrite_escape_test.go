package server

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/statichost"
)

// A hostile origin that bypassed the holder must still not break out of an
// attribute: the HTML sinks escape it.
func TestStaticRewriteEscapesOrigins(t *testing.T) {
	t.Parallel()
	const evil = `https://a"onfocus="x`
	x := staticRewrite{
		hs:   statichost.StaticHosting{Static: true, Live: evil, Site: evil + ".s"},
		live: func(p string) bool { return p == "/forms/x" },
	}
	out := string(x.page([]byte(`<html><head></head><body><form action="/forms/x"></form><a href="/about">a</a></body></html>`)))
	if strings.Contains(out, `"onfocus="x`) || !strings.Contains(out, "&#34;onfocus=&#34;x") {
		t.Errorf("origin not escaped:\n%s", out)
	}
	if got := string(liveFormActions([]byte(`<form action="/-/comments/1">`), evil)); strings.Contains(got, `"onfocus="x`) {
		t.Errorf("comment form action not escaped: %s", got)
	}
}
