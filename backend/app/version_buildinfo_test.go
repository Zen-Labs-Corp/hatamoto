package app

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersionCommit(t *testing.T) {
	vcs := []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}}
	coreDep := func(v string, r *debug.Module) *debug.Module {
		return &debug.Module{Path: corePath, Version: v, Replace: r}
	}
	tests := []struct {
		name         string
		bi           debug.BuildInfo
		wantV, wantC string
	}{
		{"core is main", debug.BuildInfo{Main: debug.Module{Path: corePath, Version: "v1.4.0"}, Settings: vcs}, "v1.4.0", "abc123"},
		{"core devel", debug.BuildInfo{Main: debug.Module{Path: corePath, Version: "(devel)"}, Settings: vcs}, "", "abc123"},
		{"wrapper main uses core dep", debug.BuildInfo{
			Main:     debug.Module{Path: "example.com/wrapper", Version: "v0.2.0"},
			Deps:     []*debug.Module{{Path: "example.com/other", Version: "v9.9.9"}, coreDep("v1.4.0", nil)},
			Settings: vcs,
		}, "v1.4.0", ""},
		{"wrapper follows replace", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/wrapper", Version: "v0.2.0"},
			Deps: []*debug.Module{coreDep("v1.4.0", &debug.Module{Path: "example.com/fork", Version: "v1.4.1"})},
		}, "v1.4.1", ""},
		{"wrapper with local replace", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/wrapper", Version: "v0.2.0"},
			Deps: []*debug.Module{coreDep("v1.4.0", &debug.Module{Path: "../core"})},
		}, "", ""},
		{"wrapper without core dep", debug.BuildInfo{Main: debug.Module{Path: "example.com/wrapper", Version: "v0.2.0"}, Settings: vcs}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, c := moduleVersionCommit(&tc.bi)
			if v != tc.wantV || c != tc.wantC {
				t.Errorf("got (%q, %q), want (%q, %q)", v, c, tc.wantV, tc.wantC)
			}
		})
	}
}
