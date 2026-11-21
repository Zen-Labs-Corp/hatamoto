package email

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/email/smtptest"
)

const testUnsub = "https://example.test/-/comments/unsubscribe/TOKEN"

func modData(items ...CommentModerationItem) CommentModerationData {
	return CommentModerationData{
		SiteName: "Site S", Items: items, ModerateURL: "https://example.test/admin/comments?status=pending",
		UnsubscribeURL: testUnsub,
	}
}

func replyData() CommentReplyData {
	return CommentReplyData{SiteName: "Site S", EntityTitle: "Post P", ReplierName: "Bob",
		URL: "https://example.test/p#comment-1", UnsubscribeURL: testUnsub}
}

func replyDigestData(n int) CommentReplyDigestData {
	d := CommentReplyDigestData{SiteName: "Site S", UnsubscribeURL: testUnsub}
	for i := 1; i <= n; i++ {
		d.Items = append(d.Items, CommentReplyItem{ReplierName: fmt.Sprintf("R%d", i), EntityTitle: "Post P",
			URL: fmt.Sprintf("https://example.test/p#comment-%d", i)})
	}
	return d
}

func oneMessage(t *testing.T, srv *smtptest.Server) (head, body string) {
	t.Helper()
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	head, body, _ = strings.Cut(msgs[0].Data, "\r\n\r\n")
	return head, body
}

func TestSendCommentModeration_SingleAndDigest(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	one := CommentModerationItem{AuthorName: "Ann", EntityTitle: "Post P", Held: "contains a link, first comment"}
	if err := svc.SendCommentModeration(ctx, "mod@example.test", modData(one)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendCommentModeration(ctx, "mod@example.test", modData(one, one)); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Data, "Subject: New comment waiting on Site S\r\n") ||
		!strings.Contains(msgs[0].Data, `Ann on "Post P": held because: contains a link, first comment`) {
		t.Errorf("single mail:\n%s", msgs[0].Data)
	}
	if !strings.Contains(msgs[1].Data, "Subject: 2 comments waiting on Site S\r\n") {
		t.Errorf("digest mail:\n%s", msgs[1].Data)
	}
	if len(rec.entries) != 2 || rec.entries[0].Kind != KindCommentModeration || rec.entries[1].Kind != KindCommentDigest {
		t.Fatalf("log = %+v", rec.entries)
	}
	for _, e := range rec.entries {
		if e.BodyText != "" || e.BodyHTML != "" {
			t.Errorf("body stored: %+v", e)
		}
		if len(e.Recipients) != 1 || e.Recipients[0] != "mod@example.test" {
			t.Errorf("recipients = %v", e.Recipients)
		}
	}
}

func TestSendCommentModeration_CapsItems(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	var items []CommentModerationItem
	for i := 0; i < 25; i++ {
		items = append(items, CommentModerationItem{AuthorName: "N", EntityTitle: "T", Held: "h"})
	}
	if err := svc.SendCommentModeration(context.Background(), "mod@example.test", modData(items...)); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if !strings.Contains(head, "Subject: 25 comments waiting on Site S") || !strings.Contains(body, "and 5 more") {
		t.Errorf("mail:\n%s\n%s", head, body)
	}
	if err := svc.SendCommentModeration(context.Background(), "mod@example.test", modData()); err == nil {
		t.Error("no items must be an error")
	}
}

func TestSendCommentReply_LogAndHeaders(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendCommentReply(context.Background(), "a@b.test", replyData()); err != nil {
		t.Fatal(err)
	}
	head, _ := oneMessage(t, srv)
	if !strings.Contains(head, "Subject: New reply on \"Post P\"\r\n") {
		t.Errorf("subject:\n%s", head)
	}
	e := rec.entries[0]
	if e.Kind != KindCommentReply || e.BodyText != "" || e.BodyHTML != "" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Recipients) != 1 || e.Recipients[0] != "a***@b***.test" {
		t.Errorf("recipients = %v", e.Recipients)
	}
}

func TestSendCommentReplyDigest_LogAndHeaders(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendCommentReplyDigest(context.Background(), "a@b.test", replyDigestData(3)); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if !strings.Contains(head, "Subject: 3 new replies on Site S\r\n") {
		t.Errorf("subject:\n%s", head)
	}
	for _, want := range []string{"https://example.test/p#comment-1", "https://example.test/p#comment-2", "https://example.test/p#comment-3", testUnsub} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q:\n%s", want, body)
		}
	}
	e := rec.entries[0]
	if e.Kind != KindCommentReplyDigest || e.BodyText != "" || e.BodyHTML != "" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Recipients) != 1 || e.Recipients[0] != "a***@b***.test" {
		t.Errorf("recipients = %v", e.Recipients)
	}
}

func TestSendCommentReplyDigest_CapsItems(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendCommentReplyDigest(context.Background(), "a@b.test", replyDigestData(25)); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if !strings.Contains(head, "Subject: 25 new replies on Site S") || !strings.Contains(body, "and 5 more") {
		t.Errorf("mail:\n%s\n%s", head, body)
	}
	if n := strings.Count(body, "comment-20"); n == 0 || strings.Contains(body, "comment-21") {
		t.Errorf("listed items wrong:\n%s", body)
	}
}

func TestSendCommentReplyDigest_Empty(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendCommentReplyDigest(context.Background(), "a@b.test", replyDigestData(0)); err == nil {
		t.Error("no items must be an error")
	}
	if len(srv.Messages()) != 0 {
		t.Error("mail was sent")
	}
}

func TestSendCommentReplyDigest_Injection(t *testing.T) {
	const evil = "Eve\r\nBcc: x@example.test"
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	d := replyDigestData(2)
	d.Items[0].ReplierName = evil
	d.Items[1].EntityTitle = "<script>t"
	d.Items[1].ReplierName = "<script>r"
	if err := svc.SendCommentReplyDigest(context.Background(), "a@b.test", d); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if strings.Contains(head, "Bcc:") || strings.Contains(head, "Eve") {
		t.Errorf("name reached the header block:\n%s", head)
	}
	if !strings.Contains(body, "Eve  Bcc: x@example.test") || strings.Contains(body, "\r\nBcc:") {
		t.Errorf("name not folded in the body:\n%s", body)
	}
	_, html, ok := strings.Cut(body, "text/html")
	if !ok || strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("HTML not escaped:\n%s", html)
	}
}

func TestSendCommentReplyDigest_BadRecipientAndSubject(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{})
	svc := newSink(t, sinkConfig(srv, SecurityNone))
	for _, to := range []string{"a@b.test\r\nBcc: c@d.test", "Name <a@b.test>", "<a@b.test>", ""} {
		if err := svc.SendCommentReplyDigest(context.Background(), to, replyDigestData(2)); err == nil {
			t.Errorf("digest to %q: want an error", to)
		}
	}
	if len(srv.Messages()) != 0 {
		t.Error("mail was sent")
	}
	d := replyDigestData(2)
	d.SiteName = "T\r\nBcc: x@example.test"
	if err := svc.SendCommentReplyDigest(context.Background(), "a@b.test", d); err != nil {
		t.Fatal(err)
	}
	head, _ := oneMessage(t, srv)
	if strings.Contains(head, "Bcc:") || !strings.Contains(head, "Subject: New replies to your comments\r\n") {
		t.Errorf("subject did not fall back:\n%s", head)
	}
}

func TestCommentNotify_UnsubscribeEverywhere(t *testing.T) {
	for name, send := range map[string]func(*Service) error{
		"moderation": func(s *Service) error {
			return s.SendCommentModeration(context.Background(), "m@example.test", modData(CommentModerationItem{AuthorName: "A", EntityTitle: "T", Held: "h"}))
		},
		"reply": func(s *Service) error { return s.SendCommentReply(context.Background(), "a@b.test", replyData()) },
		"reply digest": func(s *Service) error {
			return s.SendCommentReplyDigest(context.Background(), "a@b.test", replyDigestData(2))
		},
	} {
		srv := smtptest.Start(t, smtptest.Options{})
		svc := newSink(t, sinkConfig(srv, SecurityNone))
		if err := send(svc); err != nil {
			t.Fatal(err)
		}
		head, body := oneMessage(t, srv)
		if !strings.Contains(head, "List-Unsubscribe: <"+testUnsub+">\r\n") ||
			!strings.Contains(head, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n") {
			t.Errorf("%s: headers:\n%s", name, head)
		}
		if n := strings.Count(body, testUnsub); n < 2 {
			t.Errorf("%s: unsubscribe URL in body %d times, want text and HTML", name, n)
		}
	}
}

func TestCommentNotify_NameInjection(t *testing.T) {
	const evil = "Eve\r\nBcc: x@example.test"
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	if err := svc.SendCommentModeration(ctx, "m@example.test", modData(CommentModerationItem{AuthorName: evil, EntityTitle: "T", Held: "h"})); err != nil {
		t.Fatal(err)
	}
	d := replyData()
	d.ReplierName = evil
	if err := svc.SendCommentReply(ctx, "a@b.test", d); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	for _, m := range msgs {
		head, body, _ := strings.Cut(m.Data, "\r\n\r\n")
		if strings.Contains(head, "Bcc:") || strings.Contains(head, "Eve") {
			t.Errorf("name reached the header block:\n%s", head)
		}
		if !strings.Contains(body, "Eve  Bcc: x@example.test") {
			t.Errorf("space-folded name missing from the body:\n%s", body)
		}
		if strings.Contains(body, "\r\nBcc:") {
			t.Errorf("line starting with Bcc in the body:\n%s", body)
		}
	}
}

func TestCommentNotify_NameLimits(t *testing.T) {
	if got := notifyName(""); got != "Someone" {
		t.Errorf("empty = %q", got)
	}
	if got := notifyName(" \r\n\t "); got != "Someone" {
		t.Errorf("blank = %q", got)
	}
	if got := notifyName(strings.Repeat("é", 200)); len([]rune(got)) != 80 {
		t.Errorf("runes = %d", len([]rune(got)))
	}
}

func TestCommentNotify_SubjectFallbacks(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	evil := "T\r\nBcc: x@example.test"

	d := replyData()
	d.EntityTitle = evil
	if err := svc.SendCommentReply(ctx, "a@b.test", d); err != nil {
		t.Fatal(err)
	}
	m := modData(CommentModerationItem{AuthorName: "A", EntityTitle: "T", Held: "h"})
	m.SiteName = evil
	if err := svc.SendCommentModeration(ctx, "m@example.test", m); err != nil {
		t.Fatal(err)
	}
	m2 := modData(CommentModerationItem{AuthorName: "A", EntityTitle: "T", Held: "h"}, CommentModerationItem{AuthorName: "B", EntityTitle: "T", Held: "h"})
	m2.SiteName = evil
	if err := svc.SendCommentModeration(ctx, "m@example.test", m2); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 3 {
		t.Fatalf("messages = %d", len(msgs))
	}
	for i, want := range []string{"New reply to your comment", "New comment waiting", "Comments waiting"} {
		head, _, _ := strings.Cut(msgs[i].Data, "\r\n\r\n")
		if strings.Contains(head, "Bcc:") {
			t.Errorf("mail %d: injected header:\n%s", i, head)
		}
		if !strings.Contains(head, "Subject: "+want+"\r\n") {
			t.Errorf("mail %d: subject did not fall back to %q:\n%s", i, want, head)
		}
	}
}

func TestCommentNotify_BadRecipient(t *testing.T) {
	for _, to := range []string{"a@b.test\r\nBcc: c@d.test", "Name <a@b.test>", "<a@b.test>", ""} {
		srv := smtptest.Start(t, smtptest.Options{})
		svc := newSink(t, sinkConfig(srv, SecurityNone))
		if err := svc.SendCommentReply(context.Background(), to, replyData()); err == nil {
			t.Errorf("reply to %q: want an error", to)
		}
		if err := svc.SendCommentModeration(context.Background(), to, modData(CommentModerationItem{AuthorName: "A", EntityTitle: "T", Held: "h"})); err == nil {
			t.Errorf("moderation to %q: want an error", to)
		}
		if len(srv.Messages()) != 0 {
			t.Errorf("%q: mail was sent", to)
		}
	}
}

func TestCommentNotify_EscapesHTML(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	if err := svc.SendCommentModeration(ctx, "m@example.test", modData(CommentModerationItem{AuthorName: "<script>a", EntityTitle: "<script>t", Held: "<script>h"})); err != nil {
		t.Fatal(err)
	}
	d := replyData()
	d.ReplierName, d.EntityTitle = "<script>r", "<script>e"
	if err := svc.SendCommentReply(ctx, "a@b.test", d); err != nil {
		t.Fatal(err)
	}
	for _, m := range srv.Messages() {
		_, body, _ := strings.Cut(m.Data, "\r\n\r\n")
		_, html, ok := strings.Cut(body, "text/html")
		if !ok {
			t.Fatalf("no HTML part:\n%s", body)
		}
		if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
			t.Errorf("HTML not escaped:\n%s", html)
		}
	}
}

func surgeData() CommentSurgeData {
	return CommentSurgeData{SiteName: "Site S", Limit: 30, Window: 10 * time.Minute,
		ModerateURL: "https://example.test/admin/comments?status=pending"}
}

func TestSendCommentSurge(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendCommentSurge(context.Background(), "owner@example.test", surgeData()); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if !strings.Contains(head, "Comments on Site S are in surge mode") {
		t.Errorf("subject:\n%s", head)
	}
	if strings.Contains(head, "List-Unsubscribe") {
		t.Errorf("unsubscribe header on a site alarm:\n%s", head)
	}
	for _, want := range []string{"More than 30 comments arrived in 10 minutes", "/comments?status=pending"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if len(rec.entries) != 1 {
		t.Fatalf("log = %+v", rec.entries)
	}
	e := rec.entries[0]
	if e.Kind != KindCommentSurge || e.BodyText != "" || e.BodyHTML != "" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Recipients) != 1 || e.Recipients[0] != "owner@example.test" {
		t.Errorf("recipients = %v", e.Recipients)
	}
}

func TestSendCommentSurge_SiteNameInjection(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	d := surgeData()
	d.SiteName = "S\r\nBcc: x@example.test"
	if err := svc.SendCommentSurge(ctx, "owner@example.test", d); err != nil {
		t.Fatal(err)
	}
	d.SiteName = "<b>S</b>"
	d.Window = time.Minute
	if err := svc.SendCommentSurge(ctx, "owner@example.test", d); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	head, _, _ := strings.Cut(msgs[0].Data, "\r\n\r\n")
	if strings.Contains(head, "Bcc:") || !strings.Contains(head, "Subject: Comments are in surge mode\r\n") {
		t.Errorf("header not sanitised:\n%s", head)
	}
	if !strings.Contains(msgs[1].Data, "&lt;b&gt;S&lt;/b&gt;") || !strings.Contains(msgs[1].Data, "1 minute") {
		t.Errorf("html not escaped or window text wrong:\n%s", msgs[1].Data)
	}
}
