package config

import (
	"strings"
	"testing"
)

func smtpEnv(t *testing.T, env map[string]string) *Config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef") // gitleaks:allow (test value)
	for _, k := range []string{"SMTP_HOST", "SMTP_FROM", "SMTP_SECURITY", "SMTP_USERNAME", "SMTP_PASSWORD"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSMTPSecurityValidate(t *testing.T) {
	from := "site@example.test"
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string // substring; empty = valid
	}{
		{"unset", map[string]string{"SMTP_HOST": "smtp.example.test", "SMTP_FROM": from}, ""},
		{"unknown value", map[string]string{"SMTP_SECURITY": "ssl"}, "SMTP_SECURITY"},
		{"upper-case tls", map[string]string{"SMTP_HOST": "smtp.example.test", "SMTP_FROM": from, "SMTP_SECURITY": "TLS"}, ""},
		{"none on a name", map[string]string{"SMTP_HOST": "mailpit", "SMTP_FROM": from, "SMTP_SECURITY": "none"}, ""},
		{"none on a private IP", map[string]string{"SMTP_HOST": "10.0.0.5", "SMTP_FROM": from, "SMTP_SECURITY": "none"}, ""},
		{"none with username", map[string]string{"SMTP_HOST": "mailpit", "SMTP_FROM": from, "SMTP_SECURITY": "none", "SMTP_USERNAME": "u"}, "SMTP_SECURITY=none"},
		{"none with password", map[string]string{"SMTP_HOST": "mailpit", "SMTP_FROM": from, "SMTP_SECURITY": "none", "SMTP_PASSWORD": "p"}, "SMTP_SECURITY=none"},
		{"none on a public IP", map[string]string{"SMTP_HOST": "203.0.113.5", "SMTP_FROM": from, "SMTP_SECURITY": "none"}, "SMTP_SECURITY=none"},
		{"none on localhost with credentials", map[string]string{"SMTP_HOST": "localhost", "SMTP_FROM": from, "SMTP_SECURITY": "none", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p"}, ""},
		{"none on localhost needs a sender", map[string]string{"SMTP_HOST": "localhost", "SMTP_SECURITY": "none"}, "SMTP_FROM"},
		{"localhost without none stays off", map[string]string{"SMTP_HOST": "localhost"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := smtpEnv(t, tc.env).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestSMTPEnabled(t *testing.T) {
	for _, tc := range []struct {
		host, sec string
		want      bool
	}{
		{"", "none", false},
		{"localhost", "", false},
		{"localhost", "starttls", false},
		{"localhost", "none", true},
		{"mailpit", "", true},
	} {
		if got := (SMTPConfig{Host: tc.host, Security: tc.sec}).Enabled(); got != tc.want {
			t.Errorf("Enabled(%q, %q) = %v, want %v", tc.host, tc.sec, got, tc.want)
		}
	}
}
