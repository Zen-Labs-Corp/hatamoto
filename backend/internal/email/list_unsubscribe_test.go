package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func sendWithUnsub(t *testing.T, unsub string) (*smtptest.Server, error) {
	t.Helper()
	srv := smtptest.Start(t, smtptest.Options{})
	svc := newSink(t, sinkConfig(srv, SecurityNone))
	_, err := svc.SendTrackedWith(context.Background(), SendOptions{ListUnsubscribe: unsub},
		[]string{"v@example.test"}, "Subject", "<p>h</p>", "t")
	return srv, err
}

func TestListUnsubscribe_Headers(t *testing.T) {
	const u = "https://example.test/-/comments/unsubscribe/TOKEN"
	srv, err := sendWithUnsub(t, u)
	if err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	head, _, _ := strings.Cut(msgs[0].Data, "\r\n\r\n")
	if n := strings.Count(head, "List-Unsubscribe: <"+u+">\r\n"); n != 1 {
		t.Errorf("List-Unsubscribe count = %d:\n%s", n, head)
	}
	if n := strings.Count(head, "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"); n != 1 {
		t.Errorf("List-Unsubscribe-Post count = %d:\n%s", n, head)
	}
}

func TestListUnsubscribe_AbsentWhenEmpty(t *testing.T) {
	srv, err := sendWithUnsub(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if data := srv.Messages()[0].Data; strings.Contains(data, "List-Unsubscribe") {
		t.Errorf("unexpected header:\n%s", data)
	}
}

func TestListUnsubscribe_Rejected(t *testing.T) {
	for _, bad := range []string{
		"https://a.test/x\r\nBcc: x@example.test",
		"<https://a.test/x>",
		"https://a.test/x,https://b.test",
		"javascript:x",
		"/x",
		"https://a b",
		"https://user@a.test/x",
		"https:///x",
		"https://a.test/" + strings.Repeat("a", 2100),
	} {
		srv, err := sendWithUnsub(t, bad)
		if !errors.Is(err, ErrBadHeader) {
			t.Errorf("%q: err = %v, want ErrBadHeader", bad, err)
		}
		if len(srv.Messages()) != 0 {
			t.Errorf("%q: message was delivered", bad)
		}
	}
}
