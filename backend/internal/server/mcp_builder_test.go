package server

import "testing"

func TestBuilderStylesheet(t *testing.T) {
	page := `<html><head><style id="hm-builder-css">.a{color:red}</style><style>x</style></head></html>`
	if got := builderStylesheet(page); got != ".a{color:red}" {
		t.Errorf("got %q", got)
	}
	if got := builderStylesheet("<html></html>"); got != "" {
		t.Errorf("no block: %q", got)
	}
}
