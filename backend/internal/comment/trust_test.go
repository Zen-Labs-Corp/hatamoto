package comment

import (
	"testing"
	"time"
)

func TestEvaluateTrust(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	def := Defaults().Thresholds
	cases := []struct {
		name string
		st   TrustStats
		th   Thresholds
		want TrustLevel
	}{
		{"known at exactly 3 / 7d", TrustStats{ApprovedCount: 3, FirstApprovedAt: ago(7 * day)}, def, TrustKnown},
		{"3 approved, 7d - 1ns", TrustStats{ApprovedCount: 3, FirstApprovedAt: ago(7*day - 1)}, def, TrustNew},
		{"2 approved at 8d", TrustStats{ApprovedCount: 2, FirstApprovedAt: ago(8 * day)}, def, TrustNew},
		{"no first approval", TrustStats{ApprovedCount: 9}, def, TrustNew},
		{"trusted at exactly 15 / 30d", TrustStats{ApprovedCount: 15, FirstApprovedAt: ago(30 * day)}, def, TrustTrusted},
		{"15 approved, 30d - 1ns", TrustStats{ApprovedCount: 15, FirstApprovedAt: ago(30*day - 1)}, def, TrustKnown},
		{"spam 89d ago", TrustStats{ApprovedCount: 15, FirstApprovedAt: ago(60 * day), LastSpamAt: ago(89 * day)}, def, TrustNew},
		{"spam exactly 90d ago", TrustStats{ApprovedCount: 15, FirstApprovedAt: ago(60 * day), LastSpamAt: ago(90 * day)}, def, TrustTrusted},
		{"blocked is sticky", TrustStats{Current: TrustBlocked, ApprovedCount: 99, FirstApprovedAt: ago(365 * day)}, def, TrustBlocked},
		{"vouched known is a floor", TrustStats{Current: TrustKnown}, def, TrustKnown},
		{"vouched trusted is a floor", TrustStats{Current: TrustTrusted}, def, TrustTrusted},
		{"computed above stored", TrustStats{Current: TrustKnown, ApprovedCount: 15, FirstApprovedAt: ago(30 * day)}, def, TrustTrusted},
		{"custom thresholds", TrustStats{ApprovedCount: 1, FirstApprovedAt: ago(0)}, Thresholds{KnownApproved: 1, KnownDays: 0, TrustedApproved: 2, TrustedDays: 0}, TrustKnown},
		{"custom thresholds trusted", TrustStats{ApprovedCount: 2, FirstApprovedAt: ago(0)}, Thresholds{KnownApproved: 1, KnownDays: 0, TrustedApproved: 2, TrustedDays: 0}, TrustTrusted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := EvaluateTrust(c.st, c.th, now); got != c.want {
				t.Errorf("EvaluateTrust = %s, want %s", got, c.want)
			}
		})
	}
}
