package app

import (
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// memLimitPercent is the share of a container's memory limit the Go runtime
// aims to stay under, leaving room for stacks, mappings and the OS.
const memLimitPercent = 88

// noCgroupLimit is the value from which a cgroup v1 limit means "unlimited".
const noCgroupLimit = 1 << 60

// defaultMemLimit is the soft memory limit to apply when the operator set no
// GOMEMLIMIT and a cgroup limits the process: memLimitPercent of the limit.
// ok is false when there is nothing to apply.
func defaultMemLimit(env func(string) string, read func(string) ([]byte, error)) (limit int64, ok bool) {
	if env("GOMEMLIMIT") != "" {
		return 0, false
	}
	if b, err := read("/sys/fs/cgroup/memory.max"); err == nil {
		return percentOfLimit(b, math.MaxInt64)
	}
	if b, err := read("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		return percentOfLimit(b, noCgroupLimit)
	}
	return 0, false
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
