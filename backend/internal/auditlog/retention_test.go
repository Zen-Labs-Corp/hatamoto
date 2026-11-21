package auditlog

import "testing"

func TestRetentionOptions(t *testing.T) {
	for _, d := range RetentionDays {
		if !ValidRetention(d) || RetentionLabel(d) == "" {
			t.Errorf("%d: valid=%v label=%q", d, ValidRetention(d), RetentionLabel(d))
		}
	}
	for _, d := range []int{0, -1, 30, 364, 5000} {
		if ValidRetention(d) {
			t.Errorf("%d must not be valid", d)
		}
	}
	if !ValidRetention(DefaultRetentionDays) || !ValidRetention(DefaultAgentRetentionDays) {
		t.Error("defaults must be allowed values")
	}
	if got := RetentionLabel(DefaultRetentionDays); got != "1 year" {
		t.Errorf("default label = %q", got)
	}
}
