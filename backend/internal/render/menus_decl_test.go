package render

import (
	"testing"
	"testing/fstest"
)

func loadMenusTheme(t *testing.T, manifest string) *Theme {
	t.Helper()
	th, err := LoadTheme(Chain{fstest.MapFS{"theme.json": {Data: []byte(manifest)}}})
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	return th
}

func TestThemeMenuLocationsValid(t *testing.T) {
	th := loadMenusTheme(t, `{"name":"x","menus":{"follow":{"title":"Follow","default":[{"label":"Blog","archive":"post"}]}}}`)
	if len(th.MenuLocations) != 1 || th.MenuLocations[0].Name != "follow" || th.MenuLocations[0].Title != "Follow" || len(th.MenuLocations[0].Default) != 1 {
		t.Fatalf("MenuLocations = %+v", th.MenuLocations)
	}
}

func TestThemeMenuLocationsInvalidIsDropped(t *testing.T) {
	th := loadMenusTheme(t, `{"name":"x","menus":{"Bad Name":{}}}`)
	if th.MenuLocations != nil {
		t.Fatalf("invalid block kept: %+v", th.MenuLocations)
	}
	if th.Name != "x" {
		t.Fatalf("theme not loaded: %q", th.Name)
	}
	if loadMenusTheme(t, `{"name":"x"}`).MenuLocations != nil {
		t.Fatal("absent block must be nil")
	}
}
