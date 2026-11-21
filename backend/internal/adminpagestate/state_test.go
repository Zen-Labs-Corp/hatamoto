package adminpagestate

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// These tests share package state, so none of them runs in parallel.

func ok(Context) error { return nil }

func validPage(id string) Page {
	return Page{
		ID: id, Label: "Example", Templates: fstest.MapFS{},
		Routes: []Route{{Method: "GET", Handle: ok}},
	}
}

func resetAfter(t *testing.T) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("no panic, want %q", want)
		}
		msg, _ := r.(string)
		if !strings.HasPrefix(msg, "adminpages: ") || !strings.Contains(msg, want) {
			t.Fatalf("panic %v, want adminpages: ...%q", r, want)
		}
	}()
	fn()
}

func TestRegisterRejectsInvalidPages(t *testing.T) {
	resetAfter(t)
	mut := func(f func(*Page)) Page { p := validPage("good"); f(&p); return p }
	cases := map[string]Page{
		"invalid page id":              validPage("Bad_ID"),
		"invalid page id \"\"":         validPage(""),
		"no label":                     mut(func(p *Page) { p.Label = " " }),
		"no templates":                 mut(func(p *Page) { p.Templates = nil }),
		"templates not usable":         mut(func(p *Page) { p.Templates = (*fstest.MapFS)(nil) }),
		"no GET route":                 mut(func(p *Page) { p.Routes = []Route{{Method: "POST", Handle: ok}} }),
		"not GET or POST":              mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "PUT", Handle: ok}) }),
		"nil handler":                  mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "POST", Path: "/x"}) }),
		"invalid route path \"x\"":     mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "POST", Path: "x", Handle: ok}) }),
		"invalid route path \"/../x\"": mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "POST", Path: "/../x", Handle: ok}) }),
		"conflicts with pattern": mut(func(p *Page) {
			p.Routes = append(p.Routes, Route{Method: "GET", Path: "/{id}", Handle: ok}, Route{Method: "GET", Path: "/{name}", Handle: ok})
		}),
		"matches the same requests": mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "GET", Handle: ok}) }),
		"bad route pattern":         mut(func(p *Page) { p.Routes = append(p.Routes, Route{Method: "POST", Path: "/{", Handle: ok}) }),
	}
	for want, p := range cases {
		mustPanic(t, want, func() { Register(p) })
	}
	if n := len(Pages()); n != 0 {
		t.Fatalf("%d pages registered after panics", n)
	}
}

func TestRegisterDuplicateID(t *testing.T) {
	resetAfter(t)
	Register(validPage("one"))
	mustPanic(t, "registered twice", func() { Register(validPage("one")) })
}

func TestRegisterBannerInvalid(t *testing.T) {
	resetAfter(t)
	fn := func(context.Context) ([]Banner, error) { return nil, nil }
	RegisterBanner("one", fn)
	mustPanic(t, "invalid banner name", func() { RegisterBanner("Bad Name", fn) })
	mustPanic(t, "registered twice", func() { RegisterBanner("one", fn) })
	mustPanic(t, "nil func", func() { RegisterBanner("two", nil) })
}

func TestFreezeBlocksRegistration(t *testing.T) {
	resetAfter(t)
	Freeze()
	mustPanic(t, "init function", func() { Register(validPage("late")) })
	mustPanic(t, "init function", func() {
		RegisterBanner("late", func(context.Context) ([]Banner, error) { return nil, nil })
	})
}

func TestPagesCopyOrderAndReset(t *testing.T) {
	resetAfter(t)
	Register(validPage("b"))
	Register(validPage("a"))
	got := Pages()
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("order = %+v", got)
	}
	got[0].ID = "mutated"
	got[1].Routes[0].Path = "/mutated"
	if again := Pages(); again[0].ID != "b" || again[1].Routes[0].Path != "" {
		t.Fatalf("Pages leaked its storage: %+v", again)
	}
	RegisterBanner("x", func(context.Context) ([]Banner, error) { return nil, nil })
	if len(Banners()) != 1 {
		t.Fatalf("banners = %d", len(Banners()))
	}
	Reset()
	if len(Pages()) != 0 || len(Banners()) != 0 {
		t.Fatal("Reset left state behind")
	}
}
