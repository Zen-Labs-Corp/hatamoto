package admin

import "strings"

// devBuildLabel is how the admin names a build that is not a release.
const devBuildLabel = "dev build"

// displayVersion shows a version with a single leading v, "" for none.
func displayVersion(v string) string {
	if v = strings.TrimLeft(strings.TrimSpace(v), "vV"); v == "" {
		return ""
	}
	return "v" + v
}

// displayAppVersion is the running build as the shell's colophon shows it:
// "v1.4.2" for a release (a pre-release such as "v1.5.0-rc.1" included), and
// "dev build" for an unstamped binary, `go run`, or a Makefile build between
// tags (scripts/version.sh stamps those "vX.Y.Z-dev.N").
func displayAppVersion(raw string) string {
	v := displayVersion(raw)
	if v == "" || v == "vdev" || v == "v(devel)" {
		return devBuildLabel
	}
	core, pre, _ := strings.Cut(v[1:], "-")
	if !isVersionCore(core) {
		return devBuildLabel
	}
	for _, id := range strings.Split(pre, ".") {
		if id == "dev" {
			return devBuildLabel
		}
	}
	return v
}

// isVersionCore reports whether s is MAJOR.MINOR.PATCH in digits.
func isVersionCore(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}

// AppVersion is the running build for the shell's colophon. A View built by
// hand (no Admin behind it) reads as a dev build.
func (v *View) AppVersion() string {
	if v.appVersion == "" {
		return devBuildLabel
	}
	return v.appVersion
}
