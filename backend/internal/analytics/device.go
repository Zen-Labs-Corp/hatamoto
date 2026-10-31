package analytics

import "strings"

// DeviceClass buckets a user agent into mobile, tablet, desktop or other. It
// is approximate by design and returns only the class, never the string.
func DeviceClass(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "ipad"), strings.Contains(l, "tablet"),
		strings.Contains(l, "android") && !strings.Contains(l, "mobile"):
		return "tablet"
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipod"),
		strings.Contains(l, "mobile"), strings.Contains(l, "android"):
		return "mobile"
	case strings.Contains(l, "windows"), strings.Contains(l, "macintosh"),
		strings.Contains(l, "x11"), strings.Contains(l, "cros"), strings.Contains(l, "linux"):
		return "desktop"
	}
	return "other"
}
