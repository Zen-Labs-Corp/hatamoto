package updater

import (
	"context"
	"fmt"

	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/sdk"
)

// DiskFactor is how many times the artifact size must be free under the data
// directory before an update starts (download plus staged copy plus slack).
const DiskFactor = 3

// NamedRange is the SDK range one enabled plugin or the active theme declares.
type NamedRange struct {
	Kind  string // "plugin" or "theme"
	Name  string
	Range string
}

// PluginSource lists the SDK ranges of the enabled plugins and the active
// theme. The adapter over the package store lives with the server wiring.
type PluginSource interface {
	EnabledSDKRanges(ctx context.Context) ([]NamedRange, error)
}

// ImportProbe reports whether a content import is running right now.
type ImportProbe interface {
	ImportRunning(ctx context.Context) (bool, error)
}

// SDKBlockers returns one sentence per plugin or theme the target release
// would break: its range is met neither by rec.SDK nor by any version in
// rec.SDKCompatibleWith. An invalid range or version blocks (fail closed).
func SDKBlockers(rec *release.Record, ranges []NamedRange) []string {
	var out []string
	for _, r := range ranges {
		ok, err := sdk.Satisfies(rec.SDK, r.Range)
		if err != nil {
			out = append(out, fmt.Sprintf("%s %s has an SDK range %q that cannot be checked against %s (%v)", r.Kind, r.Name, r.Range, rec.Version, err))
			continue
		}
		for _, c := range rec.SDKCompatibleWith {
			if ok {
				break
			}
			ok, _ = sdk.Satisfies(c, r.Range)
		}
		if !ok {
			out = append(out, fmt.Sprintf("%s %s needs SDK %s (target %s has %s)", r.Kind, r.Name, r.Range, rec.Version, rec.SDK))
		}
	}
	return out
}

// DiskBlocker returns a reason when dir has less than DiskFactor times
// artifactSize free, or "" when it has enough. free reads the free bytes
// (FreeDisk in production); a failure to read blocks.
func DiskBlocker(free func(dir string) (uint64, error), dir string, artifactSize int64) string {
	got, err := free(dir)
	if err != nil {
		return fmt.Sprintf("cannot read free disk space under %s: %v", dir, err)
	}
	need := uint64(max(artifactSize, 0)) * DiskFactor
	if got < need {
		return fmt.Sprintf("needs %d MiB free under %s, has %d MiB", need>>20, dir, got>>20)
	}
	return ""
}
