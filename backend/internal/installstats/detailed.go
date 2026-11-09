package installstats

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

// The limits of the Detailed body. The receiver enforces the same rules and
// answers 422 to a body that breaks one, so the sender drops what would not
// pass instead of sending it.
const (
	maxDetailedBytes = 16 << 10
	maxPlugins       = 100
	maxOtherPlugins  = 1000
	maxErrorKeys     = 100
	maxCount         = 1_000_000
	maxSinceHours    = 10_000
	defaultWindow    = 24 * time.Hour
)

var (
	nameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	osArchRE  = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

// PluginInfo is one installed plugin. FirstParty is the origin decision
// (signed by a first-party key, not self-built); Sources makes it so this
// package needs no knowledge of signing.
type PluginInfo struct {
	Name, Version string
	Enabled       bool
	FirstParty    bool
}

// ThemeInfo is the active theme.
type ThemeInfo struct{ Name, Version string }

// Sources is where the Detailed body gets its facts.
type Sources interface {
	Plugins(ctx context.Context) ([]PluginInfo, error)
	Theme(ctx context.Context) (ThemeInfo, error)
	PostgresMajor(ctx context.Context) (int, error)
	InstallStart(ctx context.Context) (time.Time, error)
	UpdateCounts(ctx context.Context, since time.Time) (failed, rolledBack int64, err error)
	WorkerOn() bool
}

// BuildInput is everything BuildDetailed needs.
type BuildInput struct {
	Install, Version, Channel string
	Src                       Sources
	Counts                    Counts
	WindowStart               *time.Time // start of the counts; nil reports 0 hours
	Now                       time.Time
}

type detailedPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
}

type detailedTheme struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type detailedHealth struct {
	SinceHours       int64            `json:"since_hours"`
	Panics           int64            `json:"panics"`
	HTTP5xx          int64            `json:"http_5xx"`
	RenderErrors     int64            `json:"render_errors"`
	PluginErrors     map[string]int64 `json:"plugin_errors"`
	UpdateFailed     int64            `json:"update_failed"`
	UpdateRolledBack int64            `json:"update_rolled_back"`
	MigrationFailed  int64            `json:"migration_failed"`
}

// detailedBody is the Detailed request body; field order is the key order of
// the contract.
type detailedBody struct {
	Install      string           `json:"install"`
	Version      string           `json:"version"`
	Channel      string           `json:"channel"`
	Level        string           `json:"level"`
	OS           string           `json:"os"`
	Arch         string           `json:"arch"`
	Postgres     int              `json:"postgres"`
	AgeMonths    int              `json:"age_months"`
	Worker       bool             `json:"worker"`
	Theme        detailedTheme    `json:"theme"`
	Plugins      []detailedPlugin `json:"plugins"`
	OtherPlugins int              `json:"other_plugins"`
	Health       detailedHealth   `json:"health"`
}

// BuildDetailed returns the Detailed body. An error means no valid body can be
// made (a source failed, or a value is out of range); the caller sends
// nothing then.
func BuildDetailed(ctx context.Context, in BuildInput) ([]byte, error) {
	if in.Src == nil {
		return nil, errors.New("detailed stats: no sources")
	}
	pg, err := in.Src.PostgresMajor(ctx)
	if err != nil {
		return nil, fmt.Errorf("detailed stats: postgres version: %w", err)
	}
	if pg < 9 || pg > 99 {
		return nil, fmt.Errorf("detailed stats: postgres major %d out of range", pg)
	}
	theme, err := in.Src.Theme(ctx)
	if err != nil {
		return nil, fmt.Errorf("detailed stats: theme: %w", err)
	}
	themeVersion := stripBuild(theme.Version)
	if !nameRE.MatchString(theme.Name) || !validVersion(themeVersion) {
		return nil, errors.New("detailed stats: active theme name or version is not reportable")
	}
	plugins, err := in.Src.Plugins(ctx)
	if err != nil {
		return nil, fmt.Errorf("detailed stats: plugins: %w", err)
	}
	if !osArchRE.MatchString(runtime.GOOS) || !osArchRE.MatchString(runtime.GOARCH) {
		return nil, errors.New("detailed stats: unreportable os or arch")
	}
	since := in.Now.Add(-defaultWindow)
	if in.WindowStart != nil {
		since = *in.WindowStart
	}
	failed, rolledBack, err := in.Src.UpdateCounts(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("detailed stats: update counts: %w", err)
	}
	listed, other := listPlugins(plugins)
	age := 0
	if start, err := in.Src.InstallStart(ctx); err == nil {
		age = ageBucket(monthsBetween(start, in.Now))
	}
	var sinceHours int64
	if in.WindowStart != nil {
		sinceHours = clamp(int64(in.Now.Sub(*in.WindowStart)/time.Hour), maxSinceHours)
	}
	c := in.Counts
	body := detailedBody{
		Install: in.Install, Version: in.Version, Channel: in.Channel, Level: string(LevelDetailed),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Postgres: pg, AgeMonths: age, Worker: in.Src.WorkerOn(),
		Theme:        detailedTheme{Name: theme.Name, Version: themeVersion},
		Plugins:      listed,
		OtherPlugins: int(min(max(int64(other), 0), maxOtherPlugins)),
		Health: detailedHealth{
			SinceHours: sinceHours,
			Panics:     clamp(c.Panics, maxCount), HTTP5xx: clamp(c.HTTP5xx, maxCount),
			RenderErrors: clamp(c.RenderErrors, maxCount), PluginErrors: collapseErrors(c.PluginErrors, listed),
			UpdateFailed: clamp(failed, maxCount), UpdateRolledBack: clamp(rolledBack, maxCount),
			MigrationFailed: clamp(c.MigrationFailed, maxCount),
		},
	}
	for {
		out, err := jsonx.MarshalPinned(body)
		if err != nil {
			return nil, fmt.Errorf("detailed stats: encode: %w", err)
		}
		if len(out) <= maxDetailedBytes {
			return out, nil
		}
		// Long names can push 100 plugins past the limit: move the tail of
		// the list to the plain count until the body fits.
		if len(body.Plugins) == 0 {
			return nil, errors.New("detailed stats: body too large")
		}
		keep := len(body.Plugins) - min(len(body.Plugins), 10)
		body.OtherPlugins = int(min(int64(body.OtherPlugins+len(body.Plugins)-keep), maxOtherPlugins))
		body.Plugins = body.Plugins[:keep]
		body.Health.PluginErrors = collapseErrors(c.PluginErrors, body.Plugins)
	}
}

// listPlugins splits the installed plugins into the named, sorted list (first
// party only, valid name and version, at most maxPlugins) and the count of all
// the others. A build suffix is cut from a version; nothing else is rewritten.
func listPlugins(all []PluginInfo) (listed []detailedPlugin, other int) {
	listed = []detailedPlugin{}
	var named []detailedPlugin
	for _, p := range all {
		v := stripBuild(p.Version)
		if !p.FirstParty || !nameRE.MatchString(p.Name) || !validVersion(v) {
			other++
			continue
		}
		named = append(named, detailedPlugin{Name: p.Name, Version: v, Enabled: p.Enabled})
	}
	sort.Slice(named, func(i, j int) bool { return named[i].Name < named[j].Name })
	if len(named) > maxPlugins {
		other += len(named) - maxPlugins
		named = named[:maxPlugins]
	}
	return append(listed, named...), other
}

// collapseErrors keeps the counts of listed plugins and sums every other name
// under "other"; the result has at most maxErrorKeys keys.
func collapseErrors(errs map[string]int64, listed []detailedPlugin) map[string]int64 {
	known := make(map[string]bool, len(listed))
	for _, p := range listed {
		known[p.Name] = true
	}
	out := map[string]int64{}
	var rest int64
	for name, n := range errs {
		if n <= 0 {
			continue
		}
		if known[name] && name != otherKey {
			out[name] = clamp(n, maxCount)
		} else {
			rest += n
		}
	}
	if len(out) >= maxErrorKeys { // keep a slot for "other"
		names := make([]string, 0, len(out))
		for name := range out {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names[maxErrorKeys-1:] {
			rest += out[name]
			delete(out, name)
		}
	}
	if rest > 0 {
		out[otherKey] = clamp(rest, maxCount)
	}
	return out
}

func clamp(n, hi int64) int64 { return min(max(n, 0), hi) }

// stripBuild cuts semver build metadata, which the receiver does not accept.
func stripBuild(v string) string {
	v, _, _ = strings.Cut(v, "+")
	return v
}

func validVersion(v string) bool { return len(v) <= 64 && versionRE.MatchString(v) }

// ageBuckets are the install ages that may be reported, in months.
var ageBuckets = []int{0, 1, 3, 6, 12, 24, 36}

// ageBucket returns the largest bucket not above months.
func ageBucket(months int) int {
	b := 0
	for _, a := range ageBuckets {
		if a <= months {
			b = a
		}
	}
	return b
}

// monthsBetween is the whole months from start to now; never negative.
func monthsBetween(start, now time.Time) int {
	start, now = start.UTC(), now.UTC()
	m := (now.Year()-start.Year())*12 + int(now.Month()-start.Month())
	if now.Day() < start.Day() {
		m--
	}
	return max(m, 0)
}
