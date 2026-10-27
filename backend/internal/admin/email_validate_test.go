package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/email"
)

func emailForm(kv ...string) url.Values {
	v := url.Values{
		"provider": {"custom"}, "host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"username": {"user"}, "password": {"pw"}, "from": {"hello@example.com"}, "from-name": {"Example"},
	}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

func errIDs(errs []ui.FieldError) string {
	ids := make([]string, len(errs))
	for i, e := range errs {
		ids[i] = e.ID
	}
	return strings.Join(ids, ",")
}

var storedCustom = EmailSettingsView{
	Saved: true, Provider: "custom", Host: "smtp.example.com", Port: 587, Security: email.SecurityStarttls,
	Username: "user", FromAddress: "hello@example.com", PasswordSaved: true,
}

func TestValidateEmail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		form     url.Values
		stored   EmailSettingsView
		readable bool
		wantIDs  string // comma list of error field IDs, "" = valid
		check    func(t *testing.T, in EmailInput, echo emailFormEcho)
	}{
		{name: "custom with typed password", form: emailForm(), check: func(t *testing.T, in EmailInput, echo emailFormEcho) {
			if in.Host != "smtp.example.com" || in.Port != 587 || in.Security != email.SecurityStarttls || in.Password != "pw" || echo.KeepPassword {
				t.Fatalf("in=%+v echo=%+v", in, echo)
			}
		}},
		{name: "ses defaults region and derives host", form: emailForm("provider", "ses", "host", "ignored.example.com", "port", "", "security", ""),
			check: func(t *testing.T, in EmailInput, echo emailFormEcho) {
				if in.SESRegion != "us-east-1" || in.Host != "email-smtp.us-east-1.amazonaws.com" || in.Port != 587 || in.Security != email.SecurityStarttls {
					t.Fatalf("in=%+v", in)
				}
				if echo.SESRegion != "us-east-1" {
					t.Fatalf("echo region = %q", echo.SESRegion)
				}
			}},
		{name: "ses chosen region", form: emailForm("provider", "ses", "ses-region", "eu-west-1"),
			check: func(t *testing.T, in EmailInput, _ emailFormEcho) {
				if in.Host != "email-smtp.eu-west-1.amazonaws.com" {
					t.Fatalf("host = %q", in.Host)
				}
			}},
		{name: "ses unknown region", form: emailForm("provider", "ses", "ses-region", "mars-1"), wantIDs: "ses-region"},
		{name: "gmail derives host, allows 465 TLS", form: emailForm("provider", "gmail", "host", "evil.example.com", "port", "465", "security", "tls"),
			check: func(t *testing.T, in EmailInput, _ emailFormEcho) {
				if in.Host != "smtp.gmail.com" || in.Port != 465 || in.Security != email.SecurityTLS {
					t.Fatalf("in=%+v", in)
				}
			}},
		{name: "unknown provider", form: emailForm("provider", "postmark"), wantIDs: "provider"},
		{name: "custom needs host", form: emailForm("host", " "), wantIDs: "host"},
		{name: "host with slash", form: emailForm("host", "smtp.example.com/x"), wantIDs: "host"},
		{name: "port zero", form: emailForm("port", "0"), wantIDs: "port"},
		{name: "port too big", form: emailForm("port", "70000"), wantIDs: "port"},
		{name: "port not a number", form: emailForm("port", "abc"), wantIDs: "port"},
		{name: "custom needs port", form: emailForm("port", ""), wantIDs: "port"},
		{name: "unknown security", form: emailForm("security", "ssl"), wantIDs: "security"},
		{name: "none on public host", form: emailForm("security", "none"), wantIDs: "security"},
		{name: "none on localhost", form: emailForm("host", "localhost", "port", "1025", "security", "none")},
		{name: "none on 127.0.0.1", form: emailForm("host", "127.0.0.1", "port", "1025", "security", "none")},
		{name: "none on 127.0.0.2 fails at validation", form: emailForm("host", "127.0.0.2", "port", "1025", "security", "none"), wantIDs: "security"},
		{name: "none on LOCALHOST. is normalised", form: emailForm("host", "LOCALHOST.", "port", "1025", "security", "none"),
			check: func(t *testing.T, in EmailInput, _ emailFormEcho) {
				if in.Host != "localhost" {
					t.Fatalf("host = %q, want localhost", in.Host)
				}
			}},
		{name: "none on ::1", form: emailForm("host", "::1", "port", "1025", "security", "none")},
		{name: "no credentials at all", form: emailForm("username", "", "password", ""),
			check: func(t *testing.T, in EmailInput, echo emailFormEcho) {
				if in.Username != "" || in.Password != "" || echo.KeepPassword {
					t.Fatalf("in=%+v echo=%+v", in, echo)
				}
			}},
		{name: "password without username", form: emailForm("username", ""), wantIDs: "username"},
		{name: "username with no password and nothing saved", form: emailForm("password", ""), wantIDs: "password"},

		// ISC-15 / ISC-16
		{name: "blank password keeps saved when host and username unchanged", form: emailForm("password", ""),
			stored: storedCustom, readable: true,
			check: func(t *testing.T, in EmailInput, echo emailFormEcho) {
				if !echo.KeepPassword || in.Password != "" {
					t.Fatalf("in=%+v echo=%+v", in, echo)
				}
			}},
		{name: "blank password with changed host is refused", form: emailForm("password", "", "host", "other.example.com"),
			stored: storedCustom, readable: true, wantIDs: "password"},
		{name: "blank password with changed username is refused", form: emailForm("password", "", "username", "someone-else"),
			stored: storedCustom, readable: true, wantIDs: "password"},
		{name: "blank password with unreadable saved password is refused", form: emailForm("password", ""),
			stored: storedCustom, readable: false, wantIDs: "password"},
		{name: "blank password with no saved password is refused", form: emailForm("password", ""),
			stored: EmailSettingsView{Saved: true, Host: "smtp.example.com", Username: "user"}, readable: true, wantIDs: "password"},
		{name: "ses blank password keeps when derived host unchanged",
			form:   emailForm("provider", "ses", "password", "", "username", "AKIA"),
			stored: EmailSettingsView{Saved: true, Provider: "ses", Host: "email-smtp.us-east-1.amazonaws.com", Username: "AKIA", PasswordSaved: true}, readable: true,
			check: func(t *testing.T, _ EmailInput, echo emailFormEcho) {
				if !echo.KeepPassword {
					t.Fatal("expected keep")
				}
			}},
		{name: "ses region change refuses blank password",
			form:   emailForm("provider", "ses", "ses-region", "eu-west-1", "password", "", "username", "AKIA"),
			stored: EmailSettingsView{Saved: true, Provider: "ses", Host: "email-smtp.us-east-1.amazonaws.com", Username: "AKIA", PasswordSaved: true}, readable: true,
			wantIDs: "password"},

		{name: "from needs @", form: emailForm("from", "nope"), wantIDs: "from"},
		{name: "from empty", form: emailForm("from", ""), wantIDs: "from"},
		{name: "from header injection", form: emailForm("from", "a@b.c\r\nBcc: x@y.z"), wantIDs: "from"},
		{name: "from name header injection", form: emailForm("from-name", "A\nBcc: x@y.z"), wantIDs: "from-name"},
		{name: "username header injection", form: emailForm("username", "u\r\nX: y"), wantIDs: "username"},
		{name: "blank from name allowed", form: emailForm("from-name", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, echo, errs := validateEmail(tc.form, tc.stored, tc.readable)
			if got := errIDs(errs); got != tc.wantIDs {
				t.Fatalf("error ids = %q, want %q (%+v)", got, tc.wantIDs, errs)
			}
			if !echo.Posted {
				t.Error("echo.Posted must be true")
			}
			if tc.check != nil && tc.wantIDs == "" {
				tc.check(t, in, echo)
			}
		})
	}
}

func TestValidateEmailPasswordMessage(t *testing.T) {
	t.Parallel()

	_, _, errs := validateEmail(emailForm("password", ""), EmailSettingsView{}, false)
	if len(errs) != 1 || errs[0].ID != "password" || errs[0].Message != "Enter the password for this host and username." {
		t.Fatalf("errs = %+v", errs)
	}
}
