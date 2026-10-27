package supervisor

import (
	"errors"
	"testing"
)

func TestChoose(t *testing.T) {
	const sha, other = "aaa", "bbb"
	type want struct {
		boot      bool
		name      string
		run, seen string
		shaStored string
	}
	tests := []struct {
		name string
		in   ChooseInput
		st   State
		want want
	}{
		{"unwritable runs bootstrap, state untouched",
			ChooseInput{WritableErr: errors.New("read-only"), BootstrapSHA: sha, Current: "v1.0.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.0.0", BootstrapSHA256: other},
			want{true, "bootstrap", RunRelease, "v1.0.0", other}},
		{"first run, no current",
			ChooseInput{BootstrapSHA: sha, BootstrapVersion: "dev-1"},
			State{},
			want{true, "bootstrap", RunBootstrap, "", sha}},
		{"first run with current: bootstrap wins and current is marked seen",
			ChooseInput{BootstrapSHA: sha, Current: "v1.0.0", CurrentOK: true},
			State{},
			want{true, "bootstrap", RunBootstrap, "v1.0.0", sha}},
		{"bootstrap sha changed beats an applied release",
			ChooseInput{BootstrapSHA: sha, Current: "v1.2.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: other},
			want{true, "bootstrap", RunBootstrap, "v1.2.0", sha}},
		{"same sha, current valid: release runs",
			ChooseInput{BootstrapSHA: sha, Current: "v1.2.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{false, "v1.2.0", RunRelease, "v1.2.0", sha}},
		{"invalid current falls back to bootstrap",
			ChooseInput{BootstrapSHA: sha, Current: "v1.2.0"},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{true, "bootstrap", RunRelease, "v1.2.0", sha}},
		{"bootstrap kept while current unchanged",
			ChooseInput{BootstrapSHA: sha, Current: "v1.2.0", CurrentOK: true},
			State{Run: RunBootstrap, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{true, "bootstrap", RunBootstrap, "v1.2.0", sha}},
		{"hand-flipped current is honoured",
			ChooseInput{BootstrapSHA: sha, Current: "v1.1.0", CurrentOK: true},
			State{Run: RunBootstrap, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{false, "v1.1.0", RunRelease, "v1.1.0", sha}},
		{"semver-newer bootstrap beats current",
			ChooseInput{BootstrapSHA: sha, BootstrapVersion: "v1.3.0", Current: "v1.2.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{true, "bootstrap", RunBootstrap, "v1.2.0", sha}},
		{"semver-older bootstrap loses",
			ChooseInput{BootstrapSHA: sha, BootstrapVersion: "v1.1.0", Current: "v1.2.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{false, "v1.2.0", RunRelease, "v1.2.0", sha}},
		{"non-semver bootstrap is never newer",
			ChooseInput{BootstrapSHA: sha, BootstrapVersion: "legacy-hybrid-631-gabc", Current: "v1.2.0", CurrentOK: true},
			State{Run: RunRelease, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{false, "v1.2.0", RunRelease, "v1.2.0", sha}},
		{"hand-flip then newer bootstrap",
			ChooseInput{BootstrapSHA: sha, BootstrapVersion: "v2.0.0", Current: "v1.1.0", CurrentOK: true},
			State{Run: RunBootstrap, CurrentSeen: "v1.2.0", BootstrapSHA256: sha},
			want{true, "bootstrap", RunBootstrap, "v1.1.0", sha}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.st
			c := Choose(tc.in, &st)
			got := want{c.Bootstrap, c.Name, st.Run, st.CurrentSeen, st.BootstrapSHA256}
			if got != tc.want {
				t.Errorf("got %+v (%s), want %+v", got, c.Reason, tc.want)
			}
			if c.Reason == "" {
				t.Error("empty reason")
			}
		})
	}
}

// After bootstrap wins once, repeated starts keep it until something changes.
func TestChooseStable(t *testing.T) {
	st := State{}
	in := ChooseInput{BootstrapSHA: "s", BootstrapVersion: "dev", Current: "v1.0.0", CurrentOK: true}
	for i := 0; i < 3; i++ {
		if c := Choose(in, &st); !c.Bootstrap {
			t.Fatalf("start %d chose %s", i, c.Name)
		}
	}
	in.Current = "v1.0.1" // an apply
	if c := Choose(in, &st); c.Bootstrap || c.Name != "v1.0.1" {
		t.Errorf("after apply chose %+v", c)
	}
}
