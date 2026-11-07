package email

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// A user name with CRLF and the old fixed boundary must not add a MIME part.
func TestMIMEBoundaryIsNotGuessable(t *testing.T) {
	s := &Service{}
	name := "x\r\n------=_Part_0_1234567890\r\nContent-Type: text/html\r\n\r\n<phish>\r\n------=_Part_0_1234567890--\r\n"
	raw := s.buildMIMEMessage(Config{FromAddress: "a@example.test"}, []string{"b@example.test"}, "Hi", "<p>"+name+"</p>", "text "+name, "", SendOptions{})
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	parts := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		parts++
		if parts == 2 && !strings.HasPrefix(string(body), "<p>x") {
			t.Fatalf("html part replaced: %q", body)
		}
	}
	if parts != 2 {
		t.Fatalf("%d parts, want 2", parts)
	}
}
