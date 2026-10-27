package media

import (
	"fmt"
	"testing"
)

func TestLocalStoreURLMatchesLegacyFormula(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"tenant/abc.png",
		"tenant/abc_300.jpg",
		legacyMediaTenantDir + "/0f1e.webp",
	} {
		legacy := fmt.Sprintf("%s/%s", "/media", key)
		if got := store.URL(key); got != legacy || got != "/media/"+key {
			t.Errorf("URL(%q) = %q, legacy %q", key, got, legacy)
		}
	}
}
