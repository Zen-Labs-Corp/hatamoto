package entity

import (
	"encoding/json/jsontext"
	"testing"
)

func TestURLRoutingDetectsPathChangingSettings(t *testing.T) {
	base := jsontext.Value(`{"url_pattern":"/author/{slug}","archive_path":"/team","archive_title":"Team"}`)
	same := jsontext.Value(`{"archive_title":"Other title","url_pattern":"/author/{slug}"}`)
	moved := jsontext.Value(`{"url_pattern":"/team/{slug}"}`)
	override := jsontext.Value(`{"url_pattern":"/author/{slug}","overrides":[{"attribute":"on_team","equals":"true","url_pattern":"/team/{slug}"}]}`)
	if urlRouting(base) != urlRouting(same) {
		t.Error("settings that do not move entities count as a routing change")
	}
	for name, s := range map[string]jsontext.Value{"pattern": moved, "override": override} {
		if urlRouting(base) == urlRouting(s) {
			t.Errorf("a changed %s is not seen", name)
		}
	}
	if urlRouting(nil) != urlRouting(jsontext.Value(`{}`)) {
		t.Error("no settings and empty settings differ")
	}
}
