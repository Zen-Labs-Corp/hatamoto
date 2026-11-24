package email

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func sendCommentConfirmation(t *testing.T, data CommentConfirmationData) string {
	t.Helper()
	srv := smtptest.Start(t, smtptest.Options{})
	svc := newSink(t, sinkConfig(srv, SecurityNone))
	if err := svc.SendCommentConfirmation(context.Background(), "v@example.test", data); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	return msgs[0].Data
}

func TestSendCommentConfirmation_NoCommenterText(t *testing.T) {
	data := sendCommentConfirmation(t, CommentConfirmationData{
		SiteName: "Site S", EntityTitle: "Post P", URL: "https://example.test/-/comments/confirm/TOKEN", ExpiresIn: "24 hours",
	})
	for _, want := range []string{"Subject: Confirm your comment on Site S", "Site S", "Post P", "https://example.test/-/comments/confirm/TOKEN"} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
	if strings.ContainsAny(data, "\u2013\u2014") {
		t.Error("the mail must not contain dashes used as punctuation")
	}

	var fields []string
	typ := reflect.TypeOf(CommentConfirmationData{})
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	if want := []string{"SiteName", "EntityTitle", "URL", "ExpiresIn"}; !reflect.DeepEqual(fields, want) {
		t.Errorf("fields = %v, want exactly %v (no commenter-written text may reach the mail)", fields, want)
	}
}

func TestSendCommentConfirmation_HeaderInjection(t *testing.T) {
	data := sendCommentConfirmation(t, CommentConfirmationData{
		SiteName: "S\r\nBcc: evil@example.test", EntityTitle: "P", URL: "https://example.test/c", ExpiresIn: "24 hours",
	})
	head, _, _ := strings.Cut(data, "\r\n\r\n")
	if strings.Contains(head, "Bcc:") {
		t.Errorf("injected header:\n%s", head)
	}
	if !strings.Contains(head, "Subject: Confirm your comment\r\n") {
		t.Errorf("subject did not fall back:\n%s", head)
	}
}

func TestSendCommentConfirmation_EscapesTitle(t *testing.T) {
	data := sendCommentConfirmation(t, CommentConfirmationData{
		SiteName: "S", EntityTitle: "<script>", URL: "https://example.test/c", ExpiresIn: "24 hours",
	})
	if !strings.Contains(data, "&lt;script&gt;") {
		t.Errorf("title not escaped in the HTML part:\n%s", data)
	}
	if strings.Contains(data, "<script>") && strings.Contains(strings.ReplaceAll(data, `"<script>"`, ""), "<script>") {
		t.Errorf("raw <script> in the HTML part:\n%s", data)
	}
}

func TestSendCommentConfirmation_TitleQuotedPlainly(t *testing.T) {
	data := sendCommentConfirmation(t, CommentConfirmationData{
		SiteName: "S", EntityTitle: `Say "hi"`, URL: "https://example.test/-/comments/confirm/TOKEN", ExpiresIn: "24 hours",
	})
	if !strings.Contains(data, `comment on "Say "hi"" at S`) {
		t.Errorf("text part must not escape the title:\n%s", data)
	}
}

func TestSendCommentSignIn(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{})
	svc := newSink(t, sinkConfig(srv, SecurityNone))
	err := svc.SendCommentSignIn(context.Background(), "v@example.test", CommentSignInData{
		SiteName: "Site S", URL: "https://example.test/-/comments/signin/TOKEN", ExpiresIn: "24 hours",
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	for _, want := range []string{"Subject: Sign in to Site S", "https://example.test/-/comments/signin/TOKEN", "24 hours"} {
		if !strings.Contains(msgs[0].Data, want) {
			t.Errorf("message lacks %q:\n%s", want, msgs[0].Data)
		}
	}
	if strings.ContainsAny(msgs[0].Data, "\u2013\u2014") {
		t.Error("the mail must not contain dashes used as punctuation")
	}
}
