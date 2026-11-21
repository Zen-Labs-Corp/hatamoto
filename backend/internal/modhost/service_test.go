package modhost

import (
	"context"
	"testing"
)

func TestRegisterService(t *testing.T) {
	t.Cleanup(Reset)
	run := func(context.Context) error { return nil }
	RegisterService(Service{Name: "a", Run: run})
	RegisterService(Service{Name: "a", Run: run}) // several per module are fine
	RegisterService(Service{Name: "b", Run: run})
	if s := Services(); len(s) != 3 || s[2].Name != "b" {
		t.Fatalf("services: %+v", s)
	}
	mustPanic(t, "bad name", func() { RegisterService(Service{Name: "A", Run: run}) })
	mustPanic(t, "nil run", func() { RegisterService(Service{Name: "c"}) })
	Freeze()
	mustPanic(t, "frozen", func() { RegisterService(Service{Name: "d", Run: run}) })
	Reset()
	if len(Services()) != 0 {
		t.Fatal("reset did not clear")
	}
}
