package designcheck

import (
	"os"
	"strings"
	"testing"
)

// With a provider that hides Host, Port must not sit alone in half a row.
func TestEmailPairIsOneColumnWithoutHost(t *testing.T) {
	t.Parallel()

	css, err := os.ReadFile("../../../web/admin/css/partials/emailfields.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), `.hm-pair:has([data-email-only="custom"]) { grid-template-columns: minmax(0, 1fr); }`) {
		t.Error("emailfields.css must collapse the Host/Port pair when Host is hidden")
	}
}
