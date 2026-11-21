package adminpages_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/adminpages"
)

// These tests share package state, so none of them runs in parallel.

func reset(t *testing.T) {
	t.Helper()
	adminpages.ResetForTest()
	t.Cleanup(adminpages.ResetForTest)
}

func examplePage(id string) adminpages.Page {
	return adminpages.Page{
		ID: id, Label: "Example", Desc: "An example page", Icon: "puzzle",
		Templates: fstest.MapFS{},
		Routes: []adminpages.Route{
			{Method: "GET", Handle: func(c adminpages.Context) error {
				return c.Render(200, "example", "Example", map[string]any{"SaveURL": c.URL("save")})
			}},
			{Method: "POST", Path: "/erase", Sudo: true, Handle: func(c adminpages.Context) error {
				dc, ok := c.CheckDanger(adminpages.DangerConfirm{
					Phrase: "erase",
					Body:   []adminpages.DangerText{{Parts: []adminpages.DangerPart{{Text: "Erase"}}}},
				})
				if !ok {
					return c.Render(422, "confirm", "Erase", map[string]any{"Danger": dc})
				}
				c.Redirect("")
				return nil
			}},
		},
	}
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if recover() == nil {
			t.Fatal("expected a panic")
		}
	}()
	fn()
}

func TestRegisterThroughFacade(t *testing.T) {
	reset(t)
	adminpages.Register(examplePage("example"))
	adminpages.RegisterBanner("example", func(context.Context) ([]adminpages.Banner, error) {
		return []adminpages.Banner{{Kind: "info", Message: "Hello", Href: "/settings/modules/example"}}, nil
	})
}

func TestRegisterPanicsThroughFacade(t *testing.T) {
	reset(t)
	adminpages.Register(examplePage("example"))
	mustPanic(t, func() { adminpages.Register(examplePage("example")) })
	mustPanic(t, func() { adminpages.Register(examplePage("Bad ID")) })
	mustPanic(t, func() { adminpages.RegisterBanner("example", nil) })
	adminpages.FreezeForTest()
	mustPanic(t, func() { adminpages.Register(examplePage("late")) })
	mustPanic(t, func() {
		adminpages.RegisterBanner("late", func(context.Context) ([]adminpages.Banner, error) { return nil, nil })
	})
}

// TestDangerTypesFieldSet pins the fields of the re-exported UI types: they are
// public API, so a rename or removal in core's UI package must fail here.
func TestDangerTypesFieldSet(t *testing.T) {
	cases := []struct {
		typ  reflect.Type
		want string
	}{
		{reflect.TypeOf(adminpages.DangerConfirm{}), "ID:string Title:string Phrase:string Prompt:string Action:string Back:string Label:string CancelLabel:string Tone:string CSRF:string Error:string Body:[]ui.DangerText Hidden:[]ui.ConfirmHidden"},
		{reflect.TypeOf(adminpages.DangerText{}), "Parts:[]ui.DangerPart"},
		{reflect.TypeOf(adminpages.DangerPart{}), "Text:string Code:bool Strong:bool"},
		{reflect.TypeOf(adminpages.ConfirmHidden{}), "Name:string Value:string"},
	}
	for _, c := range cases {
		var fields []string
		for i := range c.typ.NumField() {
			f := c.typ.Field(i)
			fields = append(fields, f.Name+":"+f.Type.String())
		}
		if got := strings.Join(fields, " "); got != c.want {
			t.Errorf("%s fields changed:\n got %s\nwant %s", c.typ.Name(), got, c.want)
		}
	}
}
