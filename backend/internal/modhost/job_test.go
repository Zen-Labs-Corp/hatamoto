package modhost

import (
	"context"
	"testing"
	"time"
)

func TestRegisterJob(t *testing.T) {
	t.Cleanup(Reset)
	run := func(context.Context) error { return nil }
	RegisterJob(Job{Module: "example", Name: "example", Every: time.Hour, Run: run})
	RegisterJob(Job{Module: "example", Name: "example.daily", Every: MinJobEvery, Run: run})
	got := Jobs()
	if len(got) != 2 || got[1].ID() != "module:example:example.daily" {
		t.Fatalf("jobs: %+v", got)
	}
	mustPanic(t, "duplicate", func() { RegisterJob(Job{Module: "example", Name: "example", Every: time.Hour, Run: run}) })
	mustPanic(t, "bad module", func() { RegisterJob(Job{Module: "Ex", Name: "a", Every: time.Hour, Run: run}) })
	for _, n := range []string{"", "A", "1a", "a b", "a/b"} {
		mustPanic(t, "bad name "+n, func() { RegisterJob(Job{Module: "example", Name: n, Every: time.Hour, Run: run}) })
	}
	mustPanic(t, "too frequent", func() { RegisterJob(Job{Module: "example", Name: "fast", Every: 59 * time.Second, Run: run}) })
	mustPanic(t, "nil run", func() { RegisterJob(Job{Module: "example", Name: "nil", Every: time.Hour}) })
	Freeze()
	mustPanic(t, "frozen", func() { RegisterJob(Job{Module: "example", Name: "late", Every: time.Hour, Run: run}) })
	Reset()
	if len(Jobs()) != 0 {
		t.Fatal("reset did not clear")
	}
}
