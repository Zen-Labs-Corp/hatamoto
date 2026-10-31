package render

import (
	"strings"
	"testing"
)

func TestHmTrackScriptEngageFlag(t *testing.T) {
	on, off := string(trackScript(true)), string(trackScript(false))
	if !strings.Contains(on, "var ENGAGE = true;") || !strings.Contains(off, "var ENGAGE = false;") {
		t.Fatal("ENGAGE flag not substituted")
	}
	if strings.Contains(on, "__HM_ENGAGE__") || strings.Contains(off, "__HM_ENGAGE__") {
		t.Fatal("placeholder left in script")
	}
}

func TestHmTrackScriptPrefixRewrite(t *testing.T) {
	SetPublicAPIPrefix("/x/api")
	t.Cleanup(func() { SetPublicAPIPrefix("") })
	js := string(trackScript(true))
	if !strings.Contains(js, "'/x/api/track'") || strings.Contains(js, "/api/v1/track") {
		t.Fatal("API prefix not applied")
	}
}

func TestHmTrackScriptNoPersistentIdentifiers(t *testing.T) {
	js := string(trackScript(true))
	for _, bad := range []string{"document.cookie", "localStorage", "userAgent", "screen.", "document.referrer + ", "indexedDB"} {
		if strings.Contains(js, bad) {
			t.Errorf("beacon must not use %q", bad)
		}
	}
	if !strings.Contains(js, "type: 'text/plain'") || !strings.Contains(js, "credentials: 'omit'") {
		t.Error("beacon must stay a simple, credential-less request")
	}
}
