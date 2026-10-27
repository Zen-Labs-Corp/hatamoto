package entity

import (
	"encoding/json"
	"testing"
)

func TestURLRoutingDetectsPathChangingSettings(t *testing.T) {
	base := json.RawMessage(`{"url_pattern":"/author/{slug}","archive_path":"/team","archive_title":"Team"}`)
	same := json.RawMessage(`{"archive_title":"Other title","url_pattern":"/author/{slug}"}`)
	moved := json.RawMessage(`{"url_pattern":"/team/{slug}"}`)
	override := json.RawMessage(`{"url_pattern":"/author/{slug}","overrides":[{"attribute":"on_team","equals":"true","url_pattern":"/team/{slug}"}]}`)
	if urlRouting(base) != urlRouting(same) {
		t.Error("settings that do not move entities count as a routing change")
	}
	for name, s := range map[string]json.RawMessage{"pattern": moved, "override": override} {
		if urlRouting(base) == urlRouting(s) {
			t.Errorf("a changed %s is not seen", name)
		}
	}
	if urlRouting(nil) != urlRouting(json.RawMessage(`{}`)) {
		t.Error("no settings and empty settings differ")
	}
}
