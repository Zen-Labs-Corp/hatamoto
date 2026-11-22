package agentpolicystate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakePolicy struct {
	d     Decision
	err   error
	panic bool
	calls int
}

func (f *fakePolicy) Decide(context.Context, Request) (Decision, error) {
	f.calls++
	if f.panic {
		panic("boom")
	}
	return f.d, f.err
}

func use(t *testing.T, p Policy) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
	Register(p)
}

func TestDecideDefaultAllow(t *testing.T) {
	Reset()
	if Active() {
		t.Fatal("active with nothing registered")
	}
	d, err := Decide(context.Background(), Request{})
	if err != nil || d.Outcome != OutcomeAllow {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestDecideOutcomes(t *testing.T) {
	for _, o := range []Outcome{OutcomeAllow, OutcomeRequireReview, OutcomeDeny} {
		use(t, &fakePolicy{d: Decision{Outcome: o, Reason: "r"}})
		if !Active() {
			t.Fatal("not active")
		}
		d, err := Decide(context.Background(), Request{})
		if err != nil || d.Outcome != o || d.Reason != "r" {
			t.Fatalf("outcome %d: got %+v, %v", o, d, err)
		}
	}
}

func TestDecideFailsClosed(t *testing.T) {
	cases := map[string]*fakePolicy{
		"error":   {err: errors.New("x")},
		"panic":   {panic: true},
		"zero":    {},
		"invalid": {d: Decision{Outcome: 9}},
	}
	for name, p := range cases {
		use(t, p)
		d, err := Decide(context.Background(), Request{})
		if !errors.Is(err, ErrPolicy) || d != (Decision{}) {
			t.Errorf("%s: got %+v, %v", name, d, err)
		}
	}
}

func TestDecideReasonClamp(t *testing.T) {
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeDeny, Reason: "  a\nb  "}})
	if d, _ := Decide(context.Background(), Request{}); d.Reason != "a b" {
		t.Errorf("reason %q", d.Reason)
	}
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeDeny, Reason: " \n "}})
	if d, _ := Decide(context.Background(), Request{}); d.Reason != "no reason given" {
		t.Errorf("reason %q", d.Reason)
	}
	use(t, &fakePolicy{d: Decision{Outcome: OutcomeDeny, Reason: strings.Repeat("é", 500)}})
	if d, _ := Decide(context.Background(), Request{}); len([]rune(d.Reason)) != 200 {
		t.Errorf("reason has %d runes", len([]rune(d.Reason)))
	}
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: no panic", name)
		}
	}()
	f()
}

func TestRegisterPanics(t *testing.T) {
	t.Cleanup(Reset)
	Reset()
	mustPanic(t, "nil", func() { Register(nil) })
	mustPanic(t, "typed nil", func() { Register((*fakePolicy)(nil)) })
	Register(&fakePolicy{})
	mustPanic(t, "twice", func() { Register(&fakePolicy{}) })
	Reset()
	Freeze()
	mustPanic(t, "after freeze", func() { Register(&fakePolicy{}) })
}

func TestDeniedErrorUnwraps(t *testing.T) {
	var err error = &DeniedError{Reason: "no"}
	if !errors.Is(err, ErrDenied) || err.Error() != "refused by the agent policy: no" {
		t.Fatalf("got %v", err)
	}
}
