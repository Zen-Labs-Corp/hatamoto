package email

import (
	"context"
	"fmt"
	"strings"
)

// Outcomes of a review decision mail.
const (
	ReviewApproved   = "approved"
	ReviewSentBack   = "sent_back"
	ReviewNotApplied = "not_applied"
)

// ReviewDecisionData fills review_decision.html. Outcome is one of the
// Review* constants; URL is the admin page of the change (no token in it).
type ReviewDecisionData struct {
	SiteName  string
	EntryName string
	Reviewer  string
	Comment   string
	URL       string
	Outcome   string
}

// reviewDecisionView is what the template shows.
type reviewDecisionView struct {
	Heading, Lead, Comment, Reviewer, URL, Button string
}

// SendReviewDecision mails the requester of a change what the reviewer
// decided: approved (live), sent back, or not applied (the entry changed
// meanwhile). It is a no-op when the service is unconfigured, like every send.
func (s *Service) SendReviewDecision(ctx context.Context, to string, d ReviewDecisionData) error {
	site, entry := notifyText(d.SiteName), notifyText(d.EntryName)
	reviewer := strings.TrimSpace(notifyText(d.Reviewer))
	v := reviewDecisionView{Comment: strings.TrimSpace(d.Comment), Reviewer: reviewer, URL: d.URL}
	var subject string
	switch d.Outcome {
	case ReviewApproved:
		subject = "Your change to " + entry + " is live"
		v.Heading = "Your change is live"
		v.Lead = fmt.Sprintf("Your change to %s on %s was approved and published.", entry, site)
		v.Button = "Open the change"
	case ReviewSentBack:
		subject = "Your change to " + entry + " was sent back"
		v.Heading = "Your change was sent back"
		v.Lead = fmt.Sprintf("Your change to %s on %s was sent back. Nothing went live. Edit it and send it again.", entry, site)
		v.Button = "Open the change"
	case ReviewNotApplied:
		subject = "Your change to " + entry + " could not be applied"
		v.Heading = "Your change could not be applied"
		v.Lead = fmt.Sprintf("Your change to %s on %s was approved, but the entry changed in the meantime, so it was not applied. Your version is still saved with the change.", entry, site)
		v.Button = "Open the change"
	default:
		return fmt.Errorf("unknown review outcome %q", d.Outcome)
	}
	html, err := s.render("review_decision.html", v)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(v.Lead + "\n")
	if v.Comment != "" {
		who := "Comment"
		if reviewer != "" {
			who = reviewer + " wrote"
		}
		fmt.Fprintf(&b, "\n%s:\n%s\n", who, v.Comment)
	}
	fmt.Fprintf(&b, "\n%s\n", d.URL)
	return s.sendAuth(ctx, KindReviewDecision, to, site, headerOr(subject, "Your change was reviewed"), html, b.String())
}
