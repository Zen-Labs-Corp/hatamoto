package comment

// Hold reasons: rule names only, shown in the moderation queue.
const (
	ReasonLink    = "contains a link"
	ReasonFirst   = "new commenter"
	ReasonSurge   = "surge mode"
	ReasonScore   = "spam score"
	ReasonBlocked = "blocked commenter"
)

// DecideInput is what Decide needs; Verdict is the spam bucket as a string so
// this package stays independent of the spam package.
type DecideInput struct {
	Verdict     string // "allow", "hold" or "spam"
	HasLinks    bool
	Trust       TrustLevel
	AdminAuthor bool // owner or editor replying as the site
	Surge       bool
}

// Decide maps a scored submission to the status it gets once confirmed.
// Order: an admin author is approved (links allowed); a blocked commenter or a
// spam verdict is spam (a shadow hold); otherwise every applicable hold reason
// (surge, link, score, new commenter) collects and the comment is pending; a
// clean, link-free comment from a known or trusted commenter is approved.
func Decide(in DecideInput) (Status, []string) {
	if in.AdminAuthor {
		return StatusApproved, nil
	}
	if in.Trust == TrustBlocked {
		return StatusSpam, []string{ReasonBlocked}
	}
	if in.Verdict == "spam" {
		return StatusSpam, []string{ReasonScore}
	}
	var reasons []string
	if in.Surge {
		reasons = append(reasons, ReasonSurge)
	}
	if in.HasLinks {
		reasons = append(reasons, ReasonLink)
	}
	if in.Verdict == "hold" {
		reasons = append(reasons, ReasonScore)
	}
	if in.Trust != TrustKnown && in.Trust != TrustTrusted {
		reasons = append(reasons, ReasonFirst)
	}
	if len(reasons) > 0 {
		return StatusPending, reasons
	}
	return StatusApproved, nil
}
