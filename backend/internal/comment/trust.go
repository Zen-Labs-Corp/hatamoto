package comment

import "time"

// TrustStats is what trust is computed from (the commenters row).
type TrustStats struct {
	Current         TrustLevel // stored level: blocked is sticky, a vouched level is a floor
	ApprovedCount   int
	FirstApprovedAt *time.Time
	LastSpamAt      *time.Time
}

// EvaluateTrust computes a commenter's level. Blocked is sticky, a stored
// known or trusted level is a floor (a vouch), and the boundaries are
// inclusive ("at least"). Blocked is never computed from stats.
func EvaluateTrust(st TrustStats, th Thresholds, now time.Time) TrustLevel {
	if st.Current == TrustBlocked {
		return TrustBlocked
	}
	aged := func(days int) bool {
		return st.FirstApprovedAt != nil && !st.FirstApprovedAt.After(now.Add(-time.Duration(days)*24*time.Hour))
	}
	computed := TrustNew
	switch {
	case st.ApprovedCount >= th.TrustedApproved && aged(th.TrustedDays) &&
		(st.LastSpamAt == nil || !st.LastSpamAt.After(now.Add(-SpamQuietPeriod))):
		computed = TrustTrusted
	case st.ApprovedCount >= th.KnownApproved && aged(th.KnownDays) && st.LastSpamAt == nil:
		computed = TrustKnown
	}
	if st.Current.rank() > computed.rank() {
		return st.Current
	}
	return computed
}
