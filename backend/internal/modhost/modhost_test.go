package modhost

import "testing"

func TestRegisterAfterFreezePanics(t *testing.T) {
	t.Cleanup(Reset)
	Freeze()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	register("x", func() {})
}

func TestResetReopensAndClears(t *testing.T) {
	cleared := false
	onReset(func() { cleared = true })
	Freeze()
	Reset()
	if !cleared {
		t.Fatal("reset func not run")
	}
	ran := false
	register("x", func() { ran = true })
	if !ran {
		t.Fatal("register did not run fn after Reset")
	}
	resets = resets[:len(resets)-1]
}

func TestIsNil(t *testing.T) {
	var p *int
	var f func()
	var m map[string]int
	x := 1
	for name, c := range map[string]struct {
		v    any
		want bool
	}{
		"nil": {nil, true}, "ptr": {p, true}, "func": {f, true}, "map": {m, true},
		"value": {x, false}, "nonnil ptr": {&x, false},
	} {
		if got := isNil(c.v); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

func TestValidName(t *testing.T) {
	for s, want := range map[string]bool{
		"a": true, "mod_1": true, "": false, "1a": false, "A": false, "a-b": false,
		"abcdefghijklmnopqrstuvwxyzabcde": true, "abcdefghijklmnopqrstuvwxyzabcdef": false,
	} {
		if got := ValidName(s); got != want {
			t.Errorf("ValidName(%q)=%v want %v", s, got, want)
		}
	}
}
