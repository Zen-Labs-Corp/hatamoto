package comment

import (
	"slices"
	"testing"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      DecideInput
		want    Status
		reasons []string
	}{
		{"admin with link", DecideInput{AdminAuthor: true, HasLinks: true, Trust: TrustNew}, StatusApproved, nil},
		{"admin beats spam verdict", DecideInput{AdminAuthor: true, Verdict: "spam"}, StatusApproved, nil},
		{"blocked clean", DecideInput{Trust: TrustBlocked, Verdict: "allow"}, StatusSpam, []string{ReasonBlocked}},
		{"spam verdict", DecideInput{Trust: TrustTrusted, Verdict: "spam"}, StatusSpam, []string{ReasonScore}},
		{"trusted with link", DecideInput{Trust: TrustTrusted, Verdict: "allow", HasLinks: true}, StatusPending, []string{ReasonLink}},
		{"known clean", DecideInput{Trust: TrustKnown, Verdict: "allow"}, StatusApproved, nil},
		{"new clean", DecideInput{Trust: TrustNew, Verdict: "allow"}, StatusPending, []string{ReasonFirst}},
		{"surge with trusted", DecideInput{Trust: TrustTrusted, Verdict: "allow", Surge: true}, StatusPending, []string{ReasonSurge}},
		{"hold verdict known", DecideInput{Trust: TrustKnown, Verdict: "hold"}, StatusPending, []string{ReasonScore}},
		{"all reasons", DecideInput{Trust: TrustNew, Verdict: "hold", HasLinks: true, Surge: true}, StatusPending, []string{ReasonSurge, ReasonLink, ReasonScore, ReasonFirst}},
		{"community link from new", DecideInput{Community: true, Trust: TrustNew, Verdict: "allow", HasLinks: true}, StatusApproved, nil},
		{"community new clean", DecideInput{Community: true, Trust: TrustNew, Verdict: "allow"}, StatusApproved, nil},
		{"community blocked", DecideInput{Community: true, Trust: TrustBlocked, Verdict: "allow"}, StatusSpam, []string{ReasonBlocked}},
		{"community spam verdict", DecideInput{Community: true, Trust: TrustNew, Verdict: "spam", HasLinks: true}, StatusSpam, []string{ReasonScore}},
		{"community hold verdict", DecideInput{Community: true, Trust: TrustNew, Verdict: "hold", HasLinks: true}, StatusPending, []string{ReasonScore}},
		{"community surge", DecideInput{Community: true, Trust: TrustNew, Verdict: "allow", HasLinks: true, Surge: true}, StatusPending, []string{ReasonSurge}},
		{"community surge and hold", DecideInput{Community: true, Trust: TrustNew, Verdict: "hold", Surge: true}, StatusPending, []string{ReasonSurge, ReasonScore}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, reasons := Decide(c.in)
			if got != c.want || !slices.Equal(reasons, c.reasons) {
				t.Errorf("Decide = %s %v, want %s %v", got, reasons, c.want, c.reasons)
			}
		})
	}
}
