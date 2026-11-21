package app

import (
	"fmt"
	"io"
	"runtime/debug"

	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/migrations"
	"hatamoto.dev/sdk"
)

// Version and Commit are set at build time:
//
//	-ldflags "-X hatamoto.dev/core/app.Version=v1.0.0 -X hatamoto.dev/core/app.Commit=abc1234"
//
// When unset they fall back to the module version and VCS revision that
// `go build` stamps into the binary. A wrapper binary (see package edition)
// should set both with -ldflags for release builds.
var (
	Version string
	Commit  string
)

const corePath = "hatamoto.dev/core"

func buildInfo() (version, commit string) {
	version, commit = Version, Commit
	if bi, ok := debug.ReadBuildInfo(); ok {
		bv, bc := moduleVersionCommit(bi)
		if version == "" {
			version = bv
		}
		if commit == "" {
			commit = bc
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

// moduleVersionCommit returns core's version and commit as recorded in bi.
// Either may be empty. When core is the main module they come from the main
// module and the VCS settings. In a wrapper binary the main module is the
// wrapper, so the version comes from the core dependency (following a
// replace) and the VCS revision, which belongs to the wrapper, is ignored.
func moduleVersionCommit(bi *debug.BuildInfo) (version, commit string) {
	if bi.Main.Path == corePath {
		version = bi.Main.Version
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
			}
		}
	} else {
		for _, d := range bi.Deps {
			if d.Path != corePath {
				continue
			}
			if d.Replace != nil {
				d = d.Replace
			}
			version = d.Version
		}
	}
	if version == "(devel)" {
		version = ""
	}
	return version, commit
}

func printVersion(w io.Writer) {
	v, c := buildInfo()
	fmt.Fprintf(w, "hatamoto %s (commit %s, sdk %s) (%s)\n", v, c, sdk.Version, editionstate.Name())
}

// versionInfo is the shape of `hatamoto version --json`; the release tooling
// reads it to build a release record.
type versionInfo struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	SDK        string `json:"sdk"`
	SchemaHead int64  `json:"schema_head"`
	Edition    string `json:"edition"`
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
	return jsonx.Encode(w, versionInfo{Version: v, Commit: c, SDK: sdk.Version, SchemaHead: head, Edition: editionstate.Name(), SDKCompatibleWith: sdk.CompatibleWith, RemovedPermissions: removed}, jsonx.Output)
}
