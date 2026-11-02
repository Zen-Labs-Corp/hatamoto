package server

import (
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/render"
)

func TestThemeDeclaresLocation(t *testing.T) {
	s := &Server{}
	if s.themeDeclaresLocation("footer") {
		t.Fatal("nil engine declares a location")
	}
	engine, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	theme := fstest.MapFS{"theme.json": {Data: []byte(`{"name":"x","menus":{"footer":{"title":"Footer"}}}`)}}
	if err := engine.SetTheme(theme); err != nil {
		t.Fatal(err)
	}
	s.renderEngine = engine
	if !s.themeDeclaresLocation("footer") || s.themeDeclaresLocation("header") {
		t.Fatalf("declared = %+v", engine.MenuLocations())
	}
}
