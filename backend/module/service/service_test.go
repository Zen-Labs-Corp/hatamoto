package service_test

import (
	"context"
	"testing"

	"hatamoto.dev/core/module/service"
)

func panics(fn func()) (p bool) {
	defer func() { p = recover() != nil }()
	fn()
	return
}

func TestRegister(t *testing.T) {
	t.Cleanup(service.ResetForTest)
	run := func(context.Context) error { return nil }
	service.Register("reports", run)
	if s := service.ServicesForTest(); len(s) != 1 || s[0].Name != "reports" {
		t.Fatalf("services: %+v", s)
	}
	if !panics(func() { service.Register("Bad", run) }) {
		t.Error("invalid name did not panic")
	}
	if !panics(func() { service.Register("x", nil) }) {
		t.Error("nil run did not panic")
	}
	service.FreezeForTest()
	if !panics(func() { service.Register("late", run) }) {
		t.Error("register after freeze did not panic")
	}
}
