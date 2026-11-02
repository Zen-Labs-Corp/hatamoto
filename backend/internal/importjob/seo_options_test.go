package importjob

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionsSEOLiveReadDefaultsOn(t *testing.T) {
	raw, err := json.Marshal(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"no_seo_live":false`) {
		t.Fatalf("options JSON = %s", raw)
	}
	var legacy Options
	if err := json.Unmarshal([]byte(`{"types":null,"drafts":true,"media":true,"menus":true,"keep_patterns":false,"keep_edited":false}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.NoSEOLive {
		t.Fatal("legacy stored options must keep the live read on")
	}
	if DefaultOptions().NoSEOLive {
		t.Fatal("DefaultOptions must keep the live read on")
	}
}

func TestOptionsCarryNoSEOFile(t *testing.T) {
	raw, _ := json.Marshal(DefaultOptions())
	for _, bad := range []string{"seo_file", "Values", "SEOFile"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("options JSON %s must not carry the settings file (%s)", raw, bad)
		}
	}
}

func TestPhasesIncludeSEOAfterSchemas(t *testing.T) {
	if Phases[1] != "settings" || Phases[2] != "schemas" || Phases[3] != "seo" {
		t.Fatalf("Phases = %v", Phases)
	}
}
