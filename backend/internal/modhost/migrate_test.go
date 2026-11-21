package modhost

import (
	"errors"
	"testing"
	"testing/fstest"
)

func TestRegisterMigrations(t *testing.T) {
	t.Cleanup(Reset)
	RegisterMigrations("b", fstest.MapFS{})
	RegisterMigrations("a", fstest.MapFS{})
	all := AllMigrations()
	if len(all) != 2 || all[0].Module != "b" || all[1].Module != "a" {
		t.Fatalf("order: %+v", all)
	}
	mustPanic(t, "dup", func() { RegisterMigrations("a", fstest.MapFS{}) })
	mustPanic(t, "bad name", func() { RegisterMigrations("A-b", fstest.MapFS{}) })
	mustPanic(t, "nil fs", func() { RegisterMigrations("c", nil) })
	Freeze()
	mustPanic(t, "frozen", func() { RegisterMigrations("d", fstest.MapFS{}) })
}

func TestMigrationErr(t *testing.T) {
	t.Cleanup(Reset)
	boom := errors.New("boom")
	SetMigrationErr("a", boom)
	if MigrationErr("a") != boom || MigrationErr("b") != nil {
		t.Fatal("err not recorded per module")
	}
	SetMigrationErr("a", nil)
	if MigrationErr("a") != nil {
		t.Fatal("err not cleared")
	}
	SetMigrationErr("a", boom)
	Reset()
	if MigrationErr("a") != nil {
		t.Fatal("reset did not clear")
	}
}
