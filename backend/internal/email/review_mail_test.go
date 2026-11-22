package email

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/runenv"
)

func reviewData(outcome string) ReviewDecisionData {
	return ReviewDecisionData{SiteName: "Site S", EntryName: "Pricing", Reviewer: "Robin", Comment: "Fix the <b>title</b>.",
		URL: "https://example.test/admin/review/7", Outcome: outcome}
}

func TestSendReviewDecision(t *testing.T) {
	cases := []struct{ outcome, subject, lead string }{
		{ReviewApproved, "Your change to Pricing is live", "was approved and published"},
		{ReviewSentBack, "Your change to Pricing was sent back", "Nothing went live"},
		{ReviewNotApplied, "Your change to Pricing could not be applied", "was not applied"},
	}
	for _, c := range cases {
		t.Run(c.outcome, func(t *testing.T) {
			svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
			if err := svc.SendReviewDecision(context.Background(), "req@example.test", reviewData(c.outcome)); err != nil {
				t.Fatal(err)
			}
			head, body := oneMessage(t, srv)
			if !strings.Contains(head, "Subject: "+c.subject+"\r\n") {
				t.Errorf("subject:\n%s", head)
			}
			for _, want := range []string{c.lead, "Robin wrote", "Fix the ", "https://example.test/admin/review/7"} {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q:\n%s", want, body)
				}
			}
			if _, html, _ := strings.Cut(body, "text/html"); !strings.Contains(html, "Fix the &lt;b&gt;title&lt;/b&gt;.") || strings.Contains(html, "<b>title</b>") {
				t.Errorf("the comment is not escaped in the HTML part:\n%s", html)
			}
			if len(rec.entries) != 1 {
				t.Fatalf("log = %+v", rec.entries)
			}
			e := rec.entries[0]
			if e.Kind != KindReviewDecision || e.BodyText != "" || e.BodyHTML != "" {
				t.Errorf("entry = %+v", e)
			}
			if len(e.Recipients) != 1 || e.Recipients[0] != "req@example.test" {
				t.Errorf("recipients = %v", e.Recipients)
			}
		})
	}
}

func TestSendReviewDecisionUnknownOutcome(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SendReviewDecision(context.Background(), "a@example.test", reviewData("maybe")); err == nil {
		t.Fatal("want an error for an unknown outcome")
	}
}

func TestSendReviewDecisionIsANoOpWhenUnconfigured(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SendReviewDecision(context.Background(), "a@example.test", reviewData(ReviewApproved)); err != nil {
		t.Error(err)
	}
}

func TestReviewDecisionKindStorage(t *testing.T) {
	if KindReviewDecision.StoresBody() {
		t.Error("a review decision carries a reviewer's comment: its body is not stored")
	}
	if !KindReviewDecision.StoresRecipients() {
		t.Error("the requester is staff: the recipient is kept")
	}
}

func TestReviewDecisionSuppressedInStaging(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendReviewDecision(context.Background(), "req@example.test", reviewData(ReviewApproved)); err != nil {
		t.Fatal(err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != StatusSuppressed || rec.entries[0].Kind != KindReviewDecision {
		t.Fatalf("rows = %+v, want one suppressed review_decision", rec.entries)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("staging sent a mail")
	}
}
