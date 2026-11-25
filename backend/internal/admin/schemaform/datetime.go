package schemaform

import "time"

const (
	pickerMinutes = "2006-01-02T15:04"
	pickerSeconds = "2006-01-02T15:04:05"
)

// naiveToRFC3339 converts a datetime-local value to RFC 3339 in loc. Values that
// are not naive (already RFC 3339, or anything else) come back unchanged for the
// validator to judge. msg is set for a local time that does not exist (the
// clocks skip it).
func naiveToRFC3339(v string, loc *time.Location) (out, msg string) {
	for _, layout := range []string{pickerMinutes, pickerSeconds} {
		if len(v) != len(layout) {
			continue
		}
		t, err := time.ParseInLocation(layout, v, loc)
		if err != nil {
			continue
		}
		if t.Format(layout) != v {
			return "", "That time does not exist in " + loc.String() + " (the clocks change then). Pick another"
		}
		return t.Format(time.RFC3339), ""
	}
	return v, ""
}
