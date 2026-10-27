package admin

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/siteconfig"
)

// ttlPresets are the lifetimes the dropdown offers; the server accepts any
// whole number of seconds from config.MinPageCacheTTL to config.MaxPageCacheTTL.
var ttlPresets = []time.Duration{
	5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour,
	2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	48 * time.Hour, 5 * cacheDay, 30 * cacheDay,
}

// cacheDay is the unit of the longest presets.
const cacheDay = 24 * time.Hour

// intervalPresets are the preload intervals the dropdown offers.
var intervalPresets = []time.Duration{
	15 * time.Minute, 30 * time.Minute, time.Hour,
	2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	48 * time.Hour, 5 * cacheDay, 30 * cacheDay,
}

const maxCacheMB = 102400

// validatePerformance reads the Performance form. While the cache is Off the
// size and preload controls are disabled in the page (so not posted); their
// stored values are kept.
func validatePerformance(form url.Values, stored PageCacheSettings) (PageCacheSettings, []ui.FieldError) {
	var errs []ui.FieldError
	bad := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }
	out := stored

	switch form.Get("enabled") {
	case "on":
		out.Enabled = true
	case "off":
		out.Enabled = false
	default:
		bad("page-cache", "Page cache", "Choose On or Off.")
	}

	ttl := strings.TrimSpace(form.Get("ttl"))
	n, err := strconv.Atoi(ttl)
	min, max := int(config.MinPageCacheTTL/time.Second), int(config.MaxPageCacheTTL/time.Second)
	if err != nil || n < min || n > max {
		bad("ttl", "Keep pages for", "Choose a time from 30 seconds to 30 days.")
	} else {
		out.TTLSeconds = n
	}

	if !out.Enabled {
		return out, errs
	}

	// A key absent from the form (its control was disabled) keeps the stored
	// value; one posted empty means no limit.
	if !form.Has("max_mb") {
		// keep stored
	} else if mb := strings.TrimSpace(form.Get("max_mb")); mb == "" {
		out.MaxMB = 0
	} else if n, err := strconv.Atoi(mb); err != nil || n < 1 || n > maxCacheMB {
		bad("max-mb", "Max cache size", "Enter a whole number of MB from 1 to 102400, or leave it empty for no limit.")
	} else {
		out.MaxMB = n
	}

	switch preload := form.Get("preload"); {
	case !form.Has("preload"):
		// keep stored
	case preload == "on":
		out.PreloadEnabled = true
	case preload == "off":
		out.PreloadEnabled = false
	default:
		bad("preload", "Preload", "Choose On or Off.")
	}

	if out.PreloadMode == "" {
		out.PreloadMode = siteconfig.PreloadModeMissing
	}
	switch mode := form.Get("mode"); {
	case !form.Has("mode"):
		// keep stored
	case mode == siteconfig.PreloadModeMissing || mode == siteconfig.PreloadModeAll:
		out.PreloadMode = mode
	default:
		bad("preload-mode", "Pages to build", "Choose Only missing or Refresh all.")
	}
	switch after := form.Get("after_changes"); {
	case !form.Has("after_changes"):
		// keep stored
	case after == "on":
		out.PreloadAfterChanges = true
	case after == "off":
		out.PreloadAfterChanges = false
	default:
		bad("preload-after-changes", "After content changes", "Choose On or Off.")
	}

	if out.PreloadEnabled && !form.Has("interval") && out.PreloadIntervalSeconds == 0 {
		bad("preload-interval", "Preload every", "Choose one of the listed intervals.")
	} else if out.PreloadEnabled && form.Has("interval") {
		n, err := strconv.Atoi(strings.TrimSpace(form.Get("interval")))
		if err != nil || !(isIntervalPreset(n) || (n > 0 && n == stored.PreloadIntervalSeconds)) {
			bad("preload-interval", "Preload every", "Choose one of the listed intervals.")
		} else {
			out.PreloadIntervalSeconds = n
		}
	}
	return out, errs
}

func isIntervalPreset(seconds int) bool {
	for _, d := range intervalPresets {
		if int(d/time.Second) == seconds {
			return true
		}
	}
	return false
}
