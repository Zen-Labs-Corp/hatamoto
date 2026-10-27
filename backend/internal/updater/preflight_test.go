package updater

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/release"
)

func TestSDKBlockers(t *testing.T) {
	rec := func(sdkVer string, compat ...string) *release.Record {
		return &release.Record{Version: "v1.0.1", SDK: sdkVer, SDKCompatibleWith: compat}
	}
	hello := func(rng string) []NamedRange { return []NamedRange{{Kind: "plugin", Name: "hello", Range: rng}} }
	for _, tc := range []struct {
		name    string
		rec     *release.Record
		ranges  []NamedRange
		blocked bool
		mention string
	}{
		{"met by sdk", rec("0.10.0"), hello("^0.10"), false, ""},
		{"unmet, no list", rec("0.10.0"), hello("^0.8"), true, "plugin hello needs SDK ^0.8 (target v1.0.1 has 0.10.0)"},
		{"unmet by sdk, met by compat", rec("0.10.0", "0.8.0", "0.9.0"), hello("^0.8"), false, ""},
		{"unmet by both", rec("0.10.0", "0.9.0"), hello("^0.8"), true, "^0.8"},
		{"newer range than target", rec("0.10.0", "0.9.0"), hello("^0.11"), true, "^0.11"},
		{"invalid range blocks", rec("0.10.0"), hello("not a range"), true, "cannot be checked"},
		{"invalid target sdk blocks", rec("bogus"), hello("^0.10"), true, "cannot be checked"},
		{"invalid compat entry ignored", rec("0.10.0", "bogus"), hello("^0.8"), true, "^0.8"},
		{"theme named", rec("0.10.0"), []NamedRange{{Kind: "theme", Name: "calm", Range: "^0.11"}}, true, "theme calm"},
		{"none", rec("0.10.0"), nil, false, ""},
		{"only the broken one reported", rec("0.10.0"), append(hello("^0.10"), NamedRange{Kind: "plugin", Name: "old", Range: "^0.2"}), true, "plugin old"},
	} {
		got := SDKBlockers(tc.rec, tc.ranges)
		if (len(got) > 0) != tc.blocked {
			t.Errorf("%s: %v", tc.name, got)
			continue
		}
		if tc.blocked && (len(got) != 1 || !strings.Contains(got[0], tc.mention)) {
			t.Errorf("%s: %v, want %q", tc.name, got, tc.mention)
		}
	}
}

func TestDiskBlocker(t *testing.T) {
	free := func(n uint64) func(string) (uint64, error) {
		return func(string) (uint64, error) { return n, nil }
	}
	const size = 50 << 20
	if r := DiskBlocker(free(3*size), "/d", size); r != "" {
		t.Errorf("exactly 3x must pass: %s", r)
	}
	if r := DiskBlocker(free(3*size-1), "/d", size); !strings.Contains(r, "needs 150 MiB") {
		t.Errorf("just under 3x: %q", r)
	}
	if r := DiskBlocker(func(string) (uint64, error) { return 0, errors.New("no") }, "/d", size); r == "" {
		t.Error("a failed read must block")
	}
}

func TestFreeDiskReadsRealDirectory(t *testing.T) {
	got, err := FreeDisk(t.TempDir())
	if err != nil || got == 0 {
		t.Fatalf("%d, %v", got, err)
	}
	if _, err := FreeDisk("/definitely/not/here"); err == nil {
		t.Fatal("missing dir must error")
	}
}
