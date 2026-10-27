package builder

import (
	"errors"
	"strings"
	"testing"
)

func withExt(t *testing.T, ss ...ExtSource) {
	t.Helper()
	SetExtSources(ss)
	t.Cleanup(func() { SetExtSources(nil) })
}

var testExt = []ExtSource{
	{Plugin: "weather", Name: "temp", Label: "Temperature", Kind: ExtValue, Class: BindText},
	{Plugin: "weather", Name: "link", Label: "Docs link", Kind: ExtValue, Class: BindURL},
	{Plugin: "weather", Name: "snap", Label: "Snapshot", Kind: ExtValue, Class: BindImage},
	{Plugin: "weather", Name: "forecast", Label: "Forecast", Kind: ExtItems},
}

func extBindDoc(kind, prop, src string) *Doc {
	return &Doc{V: 2, Nodes: []*Node{{ID: "n1", T: kind, B: map[string]string{prop: src}}}}
}

func TestExtBindClassMatrix(t *testing.T) {
	withExt(t, testExt...)
	for _, c := range []struct {
		name, kind, prop, src string
		ok                    bool
	}{
		{"text source on text prop", "heading", "text", "ext.weather.temp", true},
		{"url source on text prop", "heading", "text", "ext.weather.link", true},
		{"url source on button href", "button", "href", "ext.weather.link", true},
		{"text source on button href refused", "button", "href", "ext.weather.temp", false},
		{"image source on image src", "image", "src", "ext.weather.snap", true},
		{"text source on image src refused", "image", "src", "ext.weather.temp", false},
		{"items source on a prop refused", "heading", "text", "ext.weather.forecast", false},
		{"unregistered accepted", "heading", "text", "ext.gone.thing", true},
		{"unregistered fits url prop", "button", "href", "ext.gone.thing", true},
		{"uppercase refused", "heading", "text", "ext.Weather.temp", false},
		{"short plugin refused", "heading", "text", "ext.w.temp", false},
		{"missing name refused", "heading", "text", "ext.weather", false},
		{"extra segment refused", "heading", "text", "ext.weather.temp.x", false},
		{"loop item look-alike refused", "heading", "text", "ext.loop.item.title", false},
	} {
		err := extBindDoc(c.kind, c.prop, c.src).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error does not wrap ErrInvalid: %v", c.name, err)
		}
	}
}

func TestExtVocabulary(t *testing.T) {
	withExt(t, testExt...)
	var got []BindSource
	for _, s := range BindSources() {
		if strings.HasPrefix(s.Source, "ext.") {
			got = append(got, s)
		}
	}
	if len(got) != 3 || got[0].Group != "weather" || got[0].Label != "Docs link" || got[0].Class != BindURL {
		t.Fatalf("ext bind sources = %+v (items sources must not appear)", got)
	}
	for _, s := range SourcesFor(BindText, false) {
		if s.Source == "ext.weather.temp" {
			goto textOK
		}
	}
	t.Fatal("SourcesFor(text) misses ext.weather.temp")
textOK:
	for _, s := range SourcesFor(BindURL, false) {
		if s.Source == "ext.weather.temp" {
			t.Fatal("SourcesFor(url) offers a text source")
		}
	}
	if n := len(ExtSources()); n != 4 {
		t.Fatalf("ExtSources = %d", n)
	}
}

func TestSetExtSourcesDropsBadEntries(t *testing.T) {
	withExt(t,
		ExtSource{Plugin: "Bad", Name: "x", Kind: ExtValue, Class: BindText},
		ExtSource{Plugin: "okay", Name: "x", Kind: "weird"},
		ExtSource{Plugin: "okay", Name: "x", Kind: ExtValue, Class: "html"},
		ExtSource{Plugin: "okay", Name: "list", Kind: ExtItems, Class: BindText},
	)
	got := ExtSources()
	if len(got) != 1 || got[0].ID() != "ext.okay.list" || got[0].Class != "" {
		t.Fatalf("ExtSources = %+v", got)
	}
}

func TestLoopFromSource(t *testing.T) {
	for _, c := range []struct {
		name string
		p    map[string]any
		ok   bool
	}{
		{"source", map[string]any{"from": "source", "source": "ext.weather.forecast"}, true},
		{"source with limit", map[string]any{"from": "source", "source": "ext.weather.forecast", "limit": 6, "order": "name"}, true},
		{"source missing", map[string]any{"from": "source"}, false},
		{"source empty", map[string]any{"from": "source", "source": ""}, false},
		{"source ignored under query", map[string]any{"from": "query", "source": "ext.weather.forecast"}, true},
		{"loop item shaped", map[string]any{"from": "source", "source": "loop.item.title"}, false},
		{"uppercase", map[string]any{"from": "source", "source": "ext.Weather.forecast"}, false},
		{"not registered still valid", map[string]any{"from": "source", "source": "ext.gone.list"}, true},
		{"too long", map[string]any{"from": "source", "source": "ext." + strings.Repeat("a", 41) + ".x"}, false},
	} {
		err := loopDoc(c.p).Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestExtValueSourceWithoutClassIsText(t *testing.T) {
	SetExtSources([]ExtSource{{Plugin: "weather", Name: "temp", Label: "T", Kind: ExtValue}})
	t.Cleanup(func() { SetExtSources(nil) })
	for _, s := range BindSources() {
		if s.Source == "ext.weather.temp" {
			if s.Class != BindText {
				t.Fatalf("class = %q, want text", s.Class)
			}
			return
		}
	}
	t.Fatal("a value source without class was dropped")
}
