package auditlog

import "slices"

// RetentionDays lists the periods an owner may keep audit entries for.
var RetentionDays = []int{90, 180, 365, 730, 1095}

const (
	// DefaultRetentionDays is how long admin audit entries are kept unless set.
	DefaultRetentionDays = 365
	// DefaultAgentRetentionDays is how long agent (MCP) calls are kept unless set.
	DefaultAgentRetentionDays = 90
)

// ValidRetention reports whether d is one of RetentionDays.
func ValidRetention(d int) bool { return slices.Contains(RetentionDays, d) }

// RetentionLabel is the plain label of a retention period.
func RetentionLabel(d int) string {
	switch d {
	case 90:
		return "90 days"
	case 180:
		return "180 days"
	case 365:
		return "1 year"
	case 730:
		return "2 years"
	case 1095:
		return "3 years"
	}
	return ""
}
