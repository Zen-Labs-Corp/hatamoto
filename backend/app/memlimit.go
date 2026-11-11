package app

import (
	"log/slog"
	"math"
	"os"
	"path"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
)

// memLimitPercent is the share of a container's memory limit the Go runtime
// aims to stay under, leaving room for stacks, mappings and the OS.
const memLimitPercent = 88

// noCgroupLimit is the value from which a cgroup v1 limit means "unlimited".
const noCgroupLimit = 1 << 60

// cgroupRoot is where the cgroup hierarchy is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// defaultMemLimit is the soft memory limit to apply when the operator set no
// GOMEMLIMIT and a cgroup limits the process: memLimitPercent of the smallest
// limit on the path from the process's own cgroup up to the root. A service
// under systemd lives in a nested cgroup (no namespace), a container usually
// sees its own cgroup as the root. ok is false when there is nothing to apply.
func defaultMemLimit(env func(string) string, read func(string) ([]byte, error)) (limit int64, ok bool) {
	if env("GOMEMLIMIT") != "" {
		return 0, false
	}
	v2Path, v1Path := "/", "/"
	var haveV2, haveV1 bool
	if b, err := read("/proc/self/cgroup"); err == nil {
		v2Path, v1Path, haveV2, haveV1 = parseSelfCgroup(string(b))
	}
	switch {
	case haveV1:
		return smallestLimit(read, cgroupRoot+"/memory", "memory.limit_in_bytes", v1Path, noCgroupLimit)
	case haveV2:
		return smallestLimit(read, cgroupRoot, "memory.max", v2Path, math.MaxInt64)
	}
	// No usable /proc/self/cgroup: look at the mount root, as a container sees it.
	if limit, ok, readable := walkLimits(read, cgroupRoot, "memory.max", "/", math.MaxInt64); readable {
		return limit, ok
	}
	return smallestLimit(read, cgroupRoot+"/memory", "memory.limit_in_bytes", "/", noCgroupLimit)
}

// parseSelfCgroup reads /proc/self/cgroup lines ("id:controllers:path"): the
// cgroup v1 memory controller line and the cgroup v2 unified line ("0::path").
func parseSelfCgroup(s string) (v2Path, v1Path string, haveV2, haveV1 bool) {
	for _, line := range strings.Split(s, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		switch {
		case parts[0] == "0" && parts[1] == "":
			v2Path, haveV2 = parts[2], true
		case slices.Contains(strings.Split(parts[1], ","), "memory"):
			v1Path, haveV1 = parts[2], true
		}
	}
	return v2Path, v1Path, haveV2, haveV1
}

func smallestLimit(read func(string) ([]byte, error), base, file, cgPath string, unlimited int64) (int64, bool) {
	limit, ok, _ := walkLimits(read, base, file, cgPath, unlimited)
	return limit, ok
}

// walkLimits reads file in cgPath and in each parent up to the root and
// returns the smallest real limit. readable reports whether any file existed.
func walkLimits(read func(string) ([]byte, error), base, file, cgPath string, unlimited int64) (limit int64, ok, readable bool) {
	for dir := path.Clean("/" + cgPath); ; dir = path.Dir(dir) {
		if b, err := read(path.Join(base, dir, file)); err == nil {
			readable = true
			if l, real := percentOfLimit(b, unlimited); real && (!ok || l < limit) {
				limit, ok = l, true
			}
		}
		if dir == "/" {
			return limit, ok, readable
		}
	}
}

// percentOfLimit parses a cgroup limit file; "max", garbage, non-positive
// values and values at or above unlimited yield false.
func percentOfLimit(b []byte, unlimited int64) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 || n >= unlimited {
		return 0, false
	}
	return n / 100 * memLimitPercent, true
}

// applyDefaultMemLimit sets the runtime's soft memory limit from the cgroup
// limit unless the operator already chose one (GOMEMLIMIT).
func applyDefaultMemLimit(logger *slog.Logger) {
	limit, ok := defaultMemLimit(os.Getenv, os.ReadFile)
	if !ok || debug.SetMemoryLimit(-1) != math.MaxInt64 {
		return
	}
	debug.SetMemoryLimit(limit)
	logger.Info("memory limit set from the container limit", "bytes", limit)
}
