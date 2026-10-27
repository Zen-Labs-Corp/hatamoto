package updater

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestEffectiveDefaultsAndEnvPrecedence(t *testing.T) {
	got := Settings{}.Effective(env(nil))
	if got.Mode != ModeAuto || got.Channel != "stable" {
		t.Fatalf("defaults: %+v", got)
	}
	row := Settings{Mode: ModeNotify, Channel: "edge"}
	if got := row.Effective(env(nil)); got.Mode != ModeNotify || got.Channel != "edge" {
		t.Fatalf("row values lost: %+v", got)
	}
	got = row.Effective(env(map[string]string{EnvMode: "off", EnvChannel: "stable"}))
	if got.Mode != ModeOff || got.Channel != "stable" {
		t.Fatalf("env must win: %+v", got)
	}
	got = row.Effective(env(map[string]string{EnvMode: "bogus", EnvChannel: "nightly"}))
	if got.Mode != ModeOff || got.Channel != "edge" {
		t.Fatalf("an invalid mode must fail closed to off, an invalid channel is ignored: %+v", got)
	}
}

func TestEffectiveCanaryChannel(t *testing.T) {
	if got := (Settings{}).Effective(env(map[string]string{EnvChannel: "canary"})); got.Channel != "canary" {
		t.Fatalf("env canary: %+v", got)
	}
	if got := (Settings{Channel: "canary"}).Effective(env(map[string]string{EnvChannel: "nightly"})); got.Channel != "canary" {
		t.Fatalf("invalid env must leave the row alone: %+v", got)
	}
	if err := (&Settings{Channel: "canary"}).Validate(); err != nil {
		t.Fatalf("canary must validate: %v", err)
	}
}

func TestEffectiveModeSynonyms(t *testing.T) {
	for v, want := range map[string]string{
		"off": ModeOff, "false": ModeOff, "0": ModeOff, "no": ModeOff, "disabled": ModeOff, "OFF": ModeOff, " False ": ModeOff,
		"on": ModeAuto, "auto": ModeAuto, "true": ModeAuto, "1": ModeAuto, "Auto": ModeAuto,
		"notify": ModeNotify, "NOTIFY": ModeNotify,
		"nope": ModeOff, "of": ModeOff, "disable": ModeOff, "2": ModeOff,
	} {
		for _, row := range []string{"", ModeAuto, ModeNotify} {
			if got := (Settings{Mode: row}).Effective(env(map[string]string{EnvMode: v})); got.Mode != want {
				t.Errorf("%q over row %q: mode %q, want %q", v, row, got.Mode, want)
			}
		}
	}
	if got := (Settings{Mode: ModeNotify}).Effective(env(map[string]string{EnvMode: " "})); got.Mode != ModeNotify {
		t.Errorf("blank value must leave the row alone: %q", got.Mode)
	}
}

func TestSettingsValidateAndClamp(t *testing.T) {
	h := func(n int) *int { return &n }
	for _, tc := range []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"empty", Settings{}, true},
		{"full", Settings{Mode: "auto", Channel: "edge", WindowStart: h(22), WindowEnd: h(0), Pin: "v1.4", DeferDays: 3}, true},
		{"bad mode", Settings{Mode: "sometimes"}, false},
		{"bad channel", Settings{Channel: "nightly"}, false},
		{"hour 24", Settings{WindowStart: h(24)}, false},
		{"hour -1", Settings{WindowEnd: h(-1)}, false},
		{"pin no v", Settings{Pin: "1.4"}, false},
		{"pin major only", Settings{Pin: "v1"}, false},
		{"pin patch", Settings{Pin: "v1.4.2"}, false},
	} {
		if err := tc.s.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	for in, want := range map[int]int{-3: 0, 0: 0, 14: 14, 99: 14} {
		s := Settings{DeferDays: in}
		if err := s.Validate(); err != nil || s.DeferDays != want {
			t.Errorf("defer %d -> %d (%v), want %d", in, s.DeferDays, err, want)
		}
		if got := (Settings{DeferDays: in}).Effective(env(nil)).DeferDays; got != want {
			t.Errorf("effective defer %d -> %d, want %d", in, got, want)
		}
	}
}
