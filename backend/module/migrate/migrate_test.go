package migrate_test

import (
	"errors"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/module/migrate"
)

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return
}

func TestRegister(t *testing.T) {
	t.Cleanup(migrate.ResetForTest)
	migrate.Register("reports", fstest.MapFS{})
	if all := migrate.AllForTest(); len(all) != 1 || all[0].Module != "reports" {
		t.Fatalf("registered: %+v", all)
	}
	if !panics(func() { migrate.Register("reports", fstest.MapFS{}) }) {
		t.Error("duplicate did not panic")
	}
	if !panics(func() { migrate.Register("Bad", fstest.MapFS{}) }) {
		t.Error("invalid name did not panic")
	}
	if !panics(func() { migrate.Register("nilfs", nil) }) {
		t.Error("nil fs did not panic")
	}
	migrate.FreezeForTest()
	if !panics(func() { migrate.Register("late", fstest.MapFS{}) }) {
		t.Error("register after freeze did not panic")
	}
}

func TestErr(t *testing.T) {
	t.Cleanup(migrate.ResetForTest)
	if migrate.Err("x") != nil {
		t.Fatal("unexpected error")
	}
	boom := errors.New("boom")
	migrate.SetErrForTest("x", boom)
	if migrate.Err("x") != boom {
		t.Fatal("error not returned")
	}
}
