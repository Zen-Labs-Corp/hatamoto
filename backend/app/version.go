package app

import (
	"fmt"
	"io"
	"runtime/debug"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

// Version and Commit are set at build time:
//
//	-ldflags "-X hatamoto.dev/core/app.Version=v1.0.0 -X hatamoto.dev/core/app.Commit=abc1234"
//
// When unset they fall back to the module version and VCS revision that
// `go build` stamps into the binary.
var (
	Version string
	Commit  string
)

func buildInfo() (version, commit string) {
	version, commit = Version, Commit
	if bi, ok := debug.ReadBuildInfo(); ok {
		if version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
		if commit == "" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					commit = s.Value
				}
			}
		}
	}
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	return version, commit
}

func printVersion(w io.Writer) {
	v, c := buildInfo()
	fmt.Fprintf(w, "hatamoto %s (commit %s, sdk %s)\n", v, c, sdk.Version)
}

// versionInfo is the shape of `hatamoto version --json`; the release tooling
// reads it to build a release record.
type versionInfo struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	SDK        string `json:"sdk"`
	SchemaHead int64  `json:"schema_head"`
	// SDKCompatibleWith lists earlier SDK versions this build still serves.
	SDKCompatibleWith []string `json:"sdk_compatible_with,omitempty"`
	// RemovedPermissions lists the plugin permissions this build refuses.
	RemovedPermissions []string `json:"removed_permissions,omitempty"`
}

func printVersionJSON(w io.Writer) error {
	head, err := migrations.Head()
	if err != nil {
		return err
	}
	v, c := buildInfo()
	removed := make([]string, 0, len(sdk.RemovedPermissions()))
	for _, p := range sdk.RemovedPermissions() {
		removed = append(removed, string(p))
	}
	return jsonx.Encode(w, versionInfo{Version: v, Commit: c, SDK: sdk.Version, SchemaHead: head, SDKCompatibleWith: sdk.CompatibleWith, RemovedPermissions: removed}, jsonx.Output)
}
