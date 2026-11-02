package admin

import (
	"strings"
	"testing"
)

func TestGapFieldHelp(t *testing.T) {
	if got := businessUI["additional_properties.unit_text"].Help; got == "" {
		t.Error("property unit has no help")
	}
	if got := localeUI()["catalog_name"].Help; !strings.Contains(got, "default locale") {
		t.Errorf("card catalogue help = %q", got)
	}
}
